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
//
// Content rewriting is separator- and escaping-aware, because a Windows device
// and a POSIX device do not spell the same path the same way. Remotely, path
// tails are always canonicalized to "/" separators; on pull they are rendered
// with the local separator, escaped for the file format being written.
type PathMapper struct {
	// mappings ordered longest local path first so the most specific prefix wins
	mappings []pathMapping
}

// pathContentKind describes how a file format spells a path, which decides both
// what to match on push and what to emit on pull.
type pathContentKind int

const (
	// pathContentPlain holds paths verbatim (.md, .txt).
	pathContentPlain pathContentKind = iota
	// pathContentJSON escapes a backslash as a pair (.json, .jsonl), so a Windows
	// path reads as C:\\Users\\bob. Writing a raw backslash into one of these
	// produces an invalid escape sequence and the file stops parsing.
	pathContentJSON
	numPathContentKinds
)

// pathSegment matches one path component. Deliberately conservative: a tail
// stops at the first character that is not plainly part of a file name, so
// prose following a path is never rewritten.
//
// Known limitation: the class is narrower than a real file name. A space, "@",
// "(", ")", "+", "~", "=", "," and every non-ASCII rune all end a segment, so
// the tail stops there and the remainder keeps the pushing device's separators.
// A Windows device pushing node_modules\@babel\core therefore hands a POSIX
// device ".../node_modules/@babel\core", whose backslashes are literal - the
// path is wrong, not merely spelled oddly, and scoped packages make this the
// common case rather than a corner. Widening the class is not obviously safe
// either: the tail would reach further into quoted tool output and prose, which
// is how an earlier revision came to rewrite a PowerShell error message. Left
// deliberately narrow pending a decision on how aggressive matching should be.
const pathSegment = `[A-Za-z0-9_.-]*`

type pathMapping struct {
	name      string // token name, e.g. "HOME", "WORK"
	localPath string // absolute local path, no trailing separator
	encLocal  string // localPath in Claude Code's directory encoding
	windows   bool   // localPath uses backslash separators

	// normRe matches this device's spelling of localPath (plus any path tail)
	// for a given content kind; localIn is how localPath is written back out.
	normRe  [numPathContentKinds]*regexp.Regexp
	localIn [numPathContentKinds]string

	// resolveRe matches the portable token and its canonical "/" tail. The
	// token is self-delimiting, so no trailing boundary is needed.
	resolveRe *regexp.Regexp
}

var pathTokenNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// NewPathMapper builds a mapper for this device. userMap maps local absolute
// paths (already ~-expanded) to token names shared across devices.
func NewPathMapper(homeDir string, userMap map[string]string) (*PathMapper, error) {
	m := &PathMapper{}

	add := func(name, localPath string) error {
		// Trim a trailing separator. A backslash only separates on a Windows
		// path; on POSIX it is a legal character in a directory name, so a
		// directory genuinely named `odd\` keeps its backslash rather than
		// collapsing onto the unrelated `odd/` subtree.
		windows := isWindowsLocalPath(localPath)
		if windows {
			localPath = strings.TrimRight(localPath, `/\`)
		} else {
			localPath = strings.TrimRight(localPath, "/")
		}
		if localPath == "" {
			return nil
		}
		if !pathTokenNameRe.MatchString(name) {
			return fmt.Errorf("invalid path_map token %q: use uppercase letters, digits, underscores (e.g. WORK)", name)
		}

		// A Windows path is often configured with forward slashes, because
		// "C:\work" is not a valid escape in YAML. Canonicalize it to the native
		// separator so the matched root, the separator emitted on pull and the
		// canonical remote tail all agree. EncodeClaudePath flattens both
		// separators to "-", so remote keys are unaffected.
		if windows {
			localPath = strings.ReplaceAll(localPath, "/", `\`)
		}

		mp := pathMapping{
			name:      name,
			localPath: localPath,
			encLocal:  EncodeClaudePath(localPath),
			windows:   windows,
		}

		for kind := pathContentKind(0); kind < numPathContentKinds; kind++ {
			// Only the root's native spelling is matched. Windows also accepts
			// C:/like/this, but that form shows up mostly inside quoted error
			// text and tool output, and rewriting a device's own prose is worse
			// than leaving one uncommon spelling unmapped.
			root := regexp.QuoteMeta(renderLocalPath(localPath, kind))
			tail := `((?:` + separatorPattern(kind, mp.windows) + pathSegment + `)*)`
			// Boundary-aware: only replace the path when it is not followed by a
			// name character, so /Users/merv never matches inside /Users/mervynlally.
			boundary := `([^A-Za-z0-9_.-]|$)`
			mp.normRe[kind] = regexp.MustCompile(root + tail + boundary)
			mp.localIn[kind] = renderLocalPath(localPath, kind)
		}

		mp.resolveRe = regexp.MustCompile(regexp.QuoteMeta(pathToken(name)) + `((?:/` + pathSegment + `)*)`)

		m.mappings = append(m.mappings, mp)
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

// isWindowsLocalPath reports whether an absolute path is spelled the Windows
// way: a drive letter ("C:\\...") or a UNC share ("\\\\server\\share"). A plain
// "contains a backslash" test would be wrong, because a backslash is a legal
// character in a POSIX directory name and claiming such a path is Windows
// rewrites its "/" separators into backslashes.
func isWindowsLocalPath(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}

// separatorPattern matches a path separator as it appears in this content kind,
// for a mapping rooted on this kind of device. A backslash is only a separator
// on a Windows mapping; on a POSIX one it is an escape character, so treating it
// as a separator would rewrite "~/My\ Documents" into "~/My/ Documents" and
// corrupt any shell command quoted in a transcript.
func separatorPattern(kind pathContentKind, windows bool) string {
	if !windows {
		return "/"
	}
	if kind == pathContentJSON {
		return `(?:\\\\|/)`
	}
	return `(?:\\|/)`
}

// renderLocalPath spells an absolute local path for the given content kind.
// Escaping follows the file format, not the platform: a backslash is legal in a
// POSIX directory name too, and writing it raw into .jsonl would produce an
// invalid escape sequence and stop the file parsing.
func renderLocalPath(p string, kind pathContentKind) string {
	if kind == pathContentJSON {
		return strings.ReplaceAll(p, `\`, `\\`)
	}
	return p
}

// localSeparator is the separator to emit for this device and content kind.
func localSeparator(kind pathContentKind, windows bool) string {
	if !windows {
		return "/"
	}
	if kind == pathContentJSON {
		return `\\`
	}
	return `\`
}

// pathContentKindFor reports how the file at relPath spells paths. Conflict copies
// (path.conflict.<timestamp>) inherit the base path's format.
func pathContentKindFor(relPath string) pathContentKind {
	if i := strings.Index(relPath, ".conflict."); i >= 0 {
		relPath = relPath[:i]
	}
	switch path.Ext(relPath) {
	case ".json", ".jsonl":
		return pathContentJSON
	}
	return pathContentPlain
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
// tokens in the content of the file at relPath. Replacement is boundary-aware
// so one user's home path never matches inside a longer username, and the
// matched path tail is canonicalized to "/" separators so a Windows device and
// a POSIX device produce byte-identical remote content.
func (m *PathMapper) NormalizeContent(relPath string, data []byte) []byte {
	if m == nil {
		return data
	}
	kind := pathContentKindFor(relPath)
	for i := range m.mappings {
		data = m.mappings[i].normalize(data, kind)
	}
	return data
}

// normalize rewrites every occurrence of this mapping's root in data.
//
// The pass repeats while the literal root is still present, because the tail is
// greedy: "~/a/Users/alice/b" is consumed by a single match and scanning resumes
// past it, so a root nested inside another path would otherwise be uploaded
// verbatim and fail to resolve on the other device. The bytes.Contains guard
// keeps the repeat off the hot path - content holding one occurrence per match,
// which is nearly all of it, is done after a single pass.
func (mp *pathMapping) normalize(data []byte, kind pathContentKind) []byte {
	re := mp.normRe[kind]
	root := []byte(mp.localIn[kind])
	token := []byte(pathToken(mp.name))

	for {
		matches := re.FindAllSubmatchIndex(data, -1)
		if len(matches) == 0 {
			return data
		}

		out := make([]byte, 0, len(data))
		prev := 0
		for _, mi := range matches {
			out = append(out, data[prev:mi[0]]...)
			out = append(out, token...)
			out = append(out, canonicalizeTail(data[mi[2]:mi[3]], kind, mp.windows)...)
			out = append(out, data[mi[4]:mi[5]]...)
			prev = mi[1]
		}
		data = append(out, data[prev:]...)

		// Every match replaced one literal root with a token, so each pass makes
		// progress and a pass that matches nothing ends the loop.
		if !bytes.Contains(data, root) {
			return data
		}
	}
}

// ResolveContent replaces portable tokens with this device's local paths,
// rendering separators and escaping for the format of the file at relPath.
func (m *PathMapper) ResolveContent(relPath string, data []byte) []byte {
	if m == nil {
		return data
	}
	kind := pathContentKindFor(relPath)
	for i := range m.mappings {
		mp := &m.mappings[i]
		re := mp.resolveRe
		local := []byte(mp.localIn[kind])
		sep := []byte(localSeparator(kind, mp.windows))

		matches := re.FindAllSubmatchIndex(data, -1)
		if len(matches) == 0 {
			continue
		}

		out := make([]byte, 0, len(data))
		prev := 0
		for _, mi := range matches {
			out = append(out, data[prev:mi[0]]...)
			out = append(out, local...)
			tail := data[mi[2]:mi[3]]
			if mp.windows {
				out = append(out, bytes.ReplaceAll(tail, []byte("/"), sep)...)
			} else {
				out = append(out, tail...)
			}
			prev = mi[1]
		}
		data = append(out, data[prev:]...)
	}
	return data
}

// canonicalizeTail rewrites the separators of a matched path tail to "/" so the
// remote form does not depend on which OS pushed it. A POSIX tail is already
// "/"-separated; its backslashes are escapes and must survive untouched.
func canonicalizeTail(tail []byte, kind pathContentKind, windows bool) []byte {
	if !windows {
		return tail
	}
	if kind == pathContentJSON {
		return bytes.ReplaceAll(tail, []byte(`\\`), []byte("/"))
	}
	return bytes.ReplaceAll(tail, []byte(`\`), []byte("/"))
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
