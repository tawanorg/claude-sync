package sync

import (
	"bytes"
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
			normRepl: []byte("$${" + name + "}${1}"),
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

// ResolveContent replaces portable tokens with this device's local paths.
func (m *PathMapper) ResolveContent(data []byte) []byte {
	if m == nil {
		return data
	}
	for _, mp := range m.mappings {
		data = bytes.ReplaceAll(data, []byte(pathToken(mp.name)), []byte(mp.localPath))
	}
	return data
}

// IsPortableContentPath reports whether content path translation applies to
// this relative path: text formats under projects/ plus the prompt history.
// Conflict copies (path.conflict.<timestamp>) inherit the base path's rule.
func IsPortableContentPath(relPath string) bool {
	if i := strings.Index(relPath, ".conflict."); i >= 0 {
		relPath = relPath[:i]
	}
	if relPath == "history.jsonl" {
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
