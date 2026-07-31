package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// PathMapper translates machine-specific project paths to portable tokens on
// push and back to local paths on pull, so sessions started on one device are
// resumable on another even when home directories or project layouts differ.
//
// Claude Code stores sessions under ~/.claude/projects/<encoded-cwd>/ where
// <encoded-cwd> is the working directory with every non-alphanumeric character
// replaced by "-" (e.g. /Users/alice/my-app -> -Users-alice-my-app). Because
// the encoding is keyed to the absolute path, a transcript synced verbatim to
// a machine with a different username or layout lands in a directory that
// `claude --resume` never looks at.
//
// The mapper rewrites two things:
//   - remote keys:   projects/-Users-alice-my-app/... -> projects/${HOME}-my-app/...
//   - file content:  /Users/alice -> ${HOME} (cwd fields, tool paths)
//
// HOME is always mapped. Additional prefixes (e.g. ~/work on one machine,
// ~/Projects on another) can be mapped via the path_map config, with both
// machines pointing their own local path at the same token name.
type PathMapper struct {
	// mappings ordered longest local path first so the most specific prefix wins
	mappings []pathMapping
}

type pathMapping struct {
	name      string // token name, e.g. "HOME", "WORK"
	localPath string // absolute local path, no trailing slash
	encLocal  string // localPath in Claude Code's directory encoding
	normRe    *regexp.Regexp
	normRepl  []byte // replacement template: token ($-escaped) + boundary group
	// resolveRe captures the token and its path tail so the tail's separators
	// can follow localPath's convention; otherwise a foreign-OS separator
	// survives after the prefix (e.g. C:\Users\bob/foo) and cwd matching fails.
	resolveRe *regexp.Regexp
}

var pathTokenNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// NewPathMapper builds a mapper for this device. userMap maps local absolute
// paths (already ~-expanded) to token names shared across devices.
func NewPathMapper(homeDir string, userMap map[string]string) (*PathMapper, error) {
	m := &PathMapper{}

	add := func(name, localPath string) error {
		localPath = strings.TrimRight(localPath, "/")
		if localPath == "" {
			return nil
		}
		if !pathTokenNameRe.MatchString(name) {
			return fmt.Errorf("invalid path_map token %q: use uppercase letters, digits, underscores (e.g. WORK)", name)
		}
		// Boundary-aware: only replace the path when it is not followed by a
		// name character, so /Users/merv never matches inside /Users/mervynlally.
		re := regexp.MustCompile(regexp.QuoteMeta(localPath) + `([^A-Za-z0-9_.-]|$)`)
		m.mappings = append(m.mappings, pathMapping{
			name:      name,
			localPath: localPath,
			encLocal:  EncodeClaudePath(localPath),
			normRe:    re,
			// "$$" = literal "$" in a regexp replacement template; without it
			// "${HOME}" would itself be read as a group reference
			normRepl:  []byte("$${" + name + "}${1}"),
			resolveRe: regexp.MustCompile(regexp.QuoteMeta(pathToken(name)) + `([/\\][^"\s]*)?`),
		})
		return nil
	}

	for localPath, name := range userMap {
		if strings.EqualFold(name, "HOME") {
			return nil, fmt.Errorf("path_map token HOME is reserved (the home directory is mapped automatically)")
		}
		if err := add(name, localPath); err != nil {
			return nil, err
		}
	}
	if homeDir != "" {
		if err := add("HOME", homeDir); err != nil {
			return nil, err
		}
	}

	// Longest local path first so ~/work maps to its own token before ~ does.
	sort.SliceStable(m.mappings, func(i, j int) bool {
		return len(m.mappings[i].localPath) > len(m.mappings[j].localPath)
	})

	return m, nil
}

// EncodeClaudePath applies Claude Code's project directory encoding: every
// character outside [A-Za-z0-9] becomes "-".
func EncodeClaudePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Tokens use the ${NAME} form in both remote keys and file content. This
// matches the format already written to existing buckets; note that a literal
// "${HOME}" in transcript content (e.g. a quoted shell snippet) is therefore
// indistinguishable from a normalized path and resolves to the local home
// directory on pull.
func pathToken(name string) string { return "${" + name + "}" }

const tokenPrefix = "${"

// splitProjectsPath splits "projects/<seg>/rest" into seg and "/rest".
// ok is false for paths not under projects/.
func splitProjectsPath(relPath string) (seg, rest string, ok bool) {
	const prefix = "projects/"
	if !strings.HasPrefix(relPath, prefix) {
		return "", "", false
	}
	remainder := relPath[len(prefix):]
	if i := strings.IndexByte(remainder, '/'); i >= 0 {
		return remainder[:i], remainder[i:], true
	}
	return remainder, "", true
}

// NormalizeRelPath rewrites a local relative path to its portable remote form.
// Only project directory segments are affected; everything else is unchanged.
// A nil mapper performs no translation (legacy behavior).
func (m *PathMapper) NormalizeRelPath(relPath string) string {
	if m == nil {
		return relPath
	}
	seg, rest, ok := splitProjectsPath(relPath)
	if !ok || strings.HasPrefix(seg, tokenPrefix) {
		return relPath
	}
	for _, mp := range m.mappings {
		if seg == mp.encLocal || strings.HasPrefix(seg, mp.encLocal+"-") {
			return "projects/" + pathToken(mp.name) + seg[len(mp.encLocal):] + rest
		}
	}
	return relPath
}

// ResolveRelPath rewrites a portable remote path back to a local relative
// path. ok is false when the path uses a token this device has no mapping
// for (the caller should skip the file and tell the user to extend path_map).
func (m *PathMapper) ResolveRelPath(relPath string) (string, bool) {
	if m == nil {
		return relPath, true
	}
	seg, rest, isProject := splitProjectsPath(relPath)
	if !isProject || !strings.HasPrefix(seg, tokenPrefix) {
		return relPath, true
	}
	for _, mp := range m.mappings {
		token := pathToken(mp.name)
		if strings.HasPrefix(seg, token) {
			return "projects/" + mp.encLocal + seg[len(token):] + rest, true
		}
	}
	return relPath, false
}

// NormalizeContent replaces this device's mapped path prefixes with portable
// tokens in file content. Replacement is boundary-aware so one user's home
// path never matches inside a longer username.
func (m *PathMapper) NormalizeContent(data []byte) []byte {
	if m == nil {
		return data
	}
	for _, mp := range m.mappings {
		data = mp.normRe.ReplaceAll(data, mp.normRepl)
	}
	return data
}

// ResolveContent replaces portable tokens with this device's local paths,
// rewriting the following path tail's separators to localPath's convention so a
// path authored on another OS resolves to a valid native path.
func (m *PathMapper) ResolveContent(data []byte) []byte {
	if m == nil {
		return data
	}
	for _, mp := range m.mappings {
		sep := pathSep(mp.localPath)
		data = mp.resolveRe.ReplaceAllFunc(data, func(match []byte) []byte {
			tail := match[len(pathToken(mp.name)):]
			return append([]byte(mp.localPath), replaceSeps(string(tail), sep)...)
		})
	}
	return data
}

// portableStatePaths are files outside projects/ that embed absolute paths.
// A JSON file is translated by decoding it and rewriting string values: a
// Windows path is escaped in the source ("C:\\Users\\bob"), so raw byte
// replacement would eat an escape and leave the document invalid.
var portableStatePaths = map[string]bool{
	"history.jsonl":                   true,
	"plugins/known_marketplaces.json": true,
	"plugins/installed_plugins.json":  true,
}

// basePath is the path a translation rule applies to: a conflict copy follows
// the file it was made from, so its content is resolved for this device and
// stays comparable against the local version.
func basePath(relPath string) string {
	base, _, _ := SplitConflictPath(relPath)
	return path.Clean(base)
}

// isPortableJSONPath reports whether relPath is translated as a single JSON
// document rather than raw bytes.
func isPortableJSONPath(relPath string) bool {
	return path.Ext(relPath) == ".json"
}

// isPortableJSONLPath reports whether relPath is JSON-lines, translated one
// JSON document per line.
func isPortableJSONLPath(relPath string) bool {
	return path.Ext(relPath) == ".jsonl"
}

// IsPortableContentPath reports whether content path translation applies to
// this relative path: text formats under projects/, the prompt history, and
// the plugin state files.
// Conflict copies (path.conflict.<timestamp>) inherit the base path's rule.
func IsPortableContentPath(relPath string) bool {
	relPath = basePath(relPath)
	if portableStatePaths[relPath] {
		return true
	}
	if !strings.HasPrefix(relPath, "projects/") {
		return false
	}
	switch path.Ext(relPath) {
	case ".jsonl", ".json", ".md", ".txt":
		return true
	}
	return false
}

// NormalizeFile replaces this device's mapped path prefixes with portable
// tokens in the content of relPath. JSON and JSON-lines files are translated
// through a JSON decode/encode so an inserted path stays correctly escaped and
// re-separated; other text is translated with boundary-aware byte replacement.
func (m *PathMapper) NormalizeFile(relPath string, data []byte) []byte {
	return m.mapFile(relPath, data, false)
}

// ResolveFile replaces portable tokens with this device's local paths in the
// content of relPath, mirroring NormalizeFile.
func (m *PathMapper) ResolveFile(relPath string, data []byte) []byte {
	return m.mapFile(relPath, data, true)
}

func (m *PathMapper) mapFile(relPath string, data []byte, resolve bool) []byte {
	if m == nil {
		return data
	}
	base := basePath(relPath)
	switch {
	case isPortableJSONPath(base):
		return m.mapJSON(data, resolve, true)
	case isPortableJSONLPath(base):
		return m.mapJSONL(data, resolve)
	case resolve:
		return m.ResolveContent(data)
	default:
		return m.NormalizeContent(data)
	}
}

// mapJSON translates every mapping in a single JSON document. resolve picks the
// direction: false normalizes local paths to tokens, true resolves tokens back.
func (m *PathMapper) mapJSON(data []byte, resolve, indent bool) []byte {
	for _, mp := range m.mappings {
		from, to := mp.localPath, pathToken(mp.name)
		if resolve {
			from, to = to, from
		}
		data = mapJSONPaths(data, from, to, indent)
	}
	return data
}

// mapJSONL translates a JSON-lines file one document per line, preserving line
// endings. Lines that are not valid JSON pass through unchanged.
func (m *PathMapper) mapJSONL(data []byte, resolve bool) []byte {
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(trimmed)) == 0 {
			continue
		}
		mapped := m.mapJSON(trimmed, resolve, false)
		mapped = append(mapped, line[len(trimmed):]...)
		lines[i] = mapped
	}
	return bytes.Join(lines, []byte("\n"))
}

// pathSep reports the separator p is written with, so a translated path keeps
// the target's convention rather than the running platform's.
func pathSep(p string) byte {
	if strings.Contains(p, `\`) {
		return '\\'
	}
	return '/'
}

// replaceSeps rewrites every separator in p to sep.
func replaceSeps(p string, sep byte) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return rune(sep)
		}
		return r
	}, p)
}

// mapJSONPaths decodes data as JSON and rewrites every string value that
// begins with from, swapping that prefix for to and re-separating the
// remainder in to's convention. Escaping is left to the encoder, so a Windows
// path stays valid; content that is not valid JSON is returned unchanged.
// Indented output re-indents state files for readability; JSON-lines callers
// pass indent=false to keep each document on a single line.
func mapJSONPaths(data []byte, from, to string, indent bool) []byte {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return data
	}

	mapPath := func(s string) string {
		if !strings.HasPrefix(s, from) {
			return s
		}
		rest := s[len(from):]
		if rest != "" && rest[0] != '/' && rest[0] != '\\' {
			return s
		}
		return to + replaceSeps(rest, pathSep(to))
	}

	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			return mapPath(t)
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
			return t
		case map[string]any:
			for k, e := range t {
				t[k] = walk(e)
			}
			return t
		}
		return v
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(walk(doc)); err != nil {
		return data
	}
	out := buf.Bytes()
	if !indent {
		// A JSON-lines document is one line; Encode always appends a newline the
		// caller rejoins itself. State files keep the newline they had.
		out = bytes.TrimSuffix(out, []byte("\n"))
	}
	return out
}
