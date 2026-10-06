package sync

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// Cross-OS content mapping. A Windows device and a POSIX device spell the same
// project path differently, and .jsonl escapes a backslash as a pair, so the
// normalized remote form has to be separator- and escaping-neutral.

const (
	posixHome = `/Users/alice`
	posixRoot = `/Users/alice/projects`
	winHome   = `C:\Users\bob`
	winRoot   = `D:\projects`
	winRoot2  = `C:\work\projects`

	// Underscore and dash both belong to a path segment, so a name using them
	// exercises the boundary rules.
	project = `my_app-dev`

	sessionFile = "projects/whatever/s.jsonl"
	memoFile    = "projects/whatever/memory/note.md"
)

func mapperFor(t *testing.T, home, root string) *PathMapper {
	t.Helper()
	m, err := NewPathMapper(home, map[string]string{root: "WORK"})
	if err != nil {
		t.Fatalf("NewPathMapper(%q, %q): %v", home, root, err)
	}
	return m
}

func TestContentNormalizesToSameRemoteFormOnEveryOS(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)
	win := mapperFor(t, winHome, winRoot)

	want := `{"cwd":"${WORK}/my_app-dev"}`

	got := string(posix.NormalizeContent(sessionFile, []byte(`{"cwd":"/Users/alice/projects/my_app-dev"}`)))
	if got != want {
		t.Errorf("posix normalize = %s, want %s", got, want)
	}

	got = string(win.NormalizeContent(sessionFile, []byte(`{"cwd":"D:\\projects\\my_app-dev"}`)))
	if got != want {
		t.Errorf("windows normalize = %s, want %s", got, want)
	}

	// A Windows root spelled with forward slashes is deliberately left alone:
	// that form appears mostly in quoted error text and tool output, where
	// rewriting the device's own prose would be worse than leaving it unmapped.
	prose := `{"text":"failed at D:/projects/my_app-dev"}`
	if got := string(win.NormalizeContent(sessionFile, []byte(prose))); got != prose {
		t.Errorf("forward-slash root = %s, should be untouched", got)
	}
}

func TestContentRoundTripAcrossOS(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)
	win := mapperFor(t, winHome, winRoot2)

	cases := []struct {
		name     string
		from, to *PathMapper
		relPath  string
		in, want string
	}{
		{
			name:    "posix to windows, jsonl stays valid JSON",
			from:    posix,
			to:      win,
			relPath: sessionFile,
			in:      `{"cwd":"/Users/alice/projects/my_app-dev","home":"/Users/alice/.claude"}`,
			want:    `{"cwd":"C:\\work\\projects\\my_app-dev","home":"C:\\Users\\bob\\.claude"}`,
		},
		{
			name:    "windows to posix, jsonl",
			from:    win,
			to:      posix,
			relPath: sessionFile,
			in:      `{"cwd":"C:\\work\\projects\\my_app-dev","home":"C:\\Users\\bob\\.claude"}`,
			want:    `{"cwd":"/Users/alice/projects/my_app-dev","home":"/Users/alice/.claude"}`,
		},
		{
			name:    "windows to posix, markdown keeps raw separators",
			from:    win,
			to:      posix,
			relPath: memoFile,
			in:      `see C:\work\projects\my_app-dev for details`,
			want:    `see /Users/alice/projects/my_app-dev for details`,
		},
		{
			name:    "posix to windows, markdown",
			from:    posix,
			to:      win,
			relPath: memoFile,
			in:      `see /Users/alice/projects/my_app-dev for details`,
			want:    `see C:\work\projects\my_app-dev for details`,
		},
		{
			name:    "windows to windows is unchanged end to end",
			from:    win,
			to:      win,
			relPath: sessionFile,
			in:      `{"cwd":"C:\\work\\projects\\my_app-dev"}`,
			want:    `{"cwd":"C:\\work\\projects\\my_app-dev"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			norm := tc.from.NormalizeContent(tc.relPath, []byte(tc.in))
			got := string(tc.to.ResolveContent(tc.relPath, norm))
			if got != tc.want {
				t.Errorf("round trip = %s\nwant                = %s\n(remote form was %s)", got, tc.want, norm)
			}
		})
	}
}

func TestResolvedJSONStaysParseable(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)
	win := mapperFor(t, winHome, winRoot)

	in := []byte(`{"cwd":"/Users/alice/projects/my_app-dev","home":"/Users/alice/.claude"}`)
	out := win.ResolveContent(sessionFile, posix.NormalizeContent(sessionFile, in))

	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("resolved content is not valid JSON: %v\ncontent: %s", err, out)
	}
	if v["cwd"] != `D:\projects\my_app-dev` {
		t.Errorf("decoded cwd = %v", v["cwd"])
	}
}

func TestWindowsBoundariesNotOverMatched(t *testing.T) {
	win := mapperFor(t, winHome, winRoot)

	// A longer sibling directory must not match the mapped root.
	for _, s := range []string{
		`{"cwd":"D:\\projectsOther\\x"}`,
		`{"cwd":"D:\\projects-archive\\x"}`,
	} {
		if got := string(win.NormalizeContent(sessionFile, []byte(s))); got != s {
			t.Errorf("NormalizeContent(%s) = %s, should be untouched", s, got)
		}
	}
}

func TestRemoteKeysMapAcrossOS(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)
	win := mapperFor(t, winHome, winRoot2)

	local := "projects/" + EncodeClaudePath(posixRoot+"/"+project) + "/s.jsonl"
	remote := posix.NormalizeRelPath(local)
	got, ok := win.ResolveRelPath(remote)

	want := "projects/" + EncodeClaudePath(winRoot2+`\`+project) + "/s.jsonl"
	if !ok || got != want {
		t.Errorf("ResolveRelPath(%q) = %q (ok=%v), want %q", remote, got, ok, want)
	}
}

// A backslash only separates path segments on a Windows mapping. On a POSIX
// mapping it is an escape character, and transcripts are full of shell commands
// that rely on it, so it has to survive a round trip untouched.
func TestPosixBackslashIsEscapeNotSeparator(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)

	cases := []struct {
		name    string
		relPath string
		in      string
	}{
		{"escaped space in a jsonl command", sessionFile, `{"command":"cd /Users/alice/My\\ Documents"}`},
		{"escaped space in markdown", memoFile, `run cd /Users/alice/My\ Documents first`},
		{"newline escape after a mapped path", sessionFile, `{"t":"/Users/alice/projects\nnext"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := posix.NormalizeContent(tc.relPath, []byte(tc.in))
			got := string(posix.ResolveContent(tc.relPath, remote))
			if got != tc.in {
				t.Errorf("round trip changed content\n in     = %s\n out    = %s\n remote = %s", tc.in, got, remote)
			}
		})
	}
}

// WORK sits under HOME, so the longest local path has to win or the more
// specific token is never produced.
func TestNestedMappingPrefersLongestPath(t *testing.T) {
	m := mapperFor(t, posixHome, posixRoot)

	in := []byte(`{"cwd":"/Users/alice/projects/app","home":"/Users/alice/.claude/x"}`)
	want := `{"cwd":"${WORK}/app","home":"${HOME}/.claude/x"}`
	if got := string(m.NormalizeContent(sessionFile, in)); got != want {
		t.Errorf("NormalizeContent = %s, want %s", got, want)
	}
}

// Pull then push has to reproduce the remote bytes exactly. If it does not, every
// sync after a pull sees a changed hash and reports a phantom modification.
func TestResolveThenNormalizeIsStable(t *testing.T) {
	devices := []struct {
		name       string
		home, root string
	}{
		{"posix", posixHome, posixRoot},
		{"windows", winHome, winRoot2},
	}

	for _, d := range devices {
		t.Run(d.name, func(t *testing.T) {
			m := mapperFor(t, d.home, d.root)
			for _, relPath := range []string{sessionFile, memoFile} {
				remote := []byte(`{"cwd":"${WORK}/app","h":"${HOME}/.claude"}`)
				local := m.ResolveContent(relPath, remote)
				if again := m.NormalizeContent(relPath, local); string(again) != string(remote) {
					t.Errorf("%s is not stable\n remote = %s\n local  = %s\n again  = %s", relPath, remote, local, again)
				}
			}
		})
	}
}

func TestNormalizeContentIsIdempotent(t *testing.T) {
	posix := mapperFor(t, posixHome, posixRoot)

	once := posix.NormalizeContent(sessionFile, []byte(`{"cwd":"/Users/alice/projects/app"}`))
	if twice := posix.NormalizeContent(sessionFile, once); string(twice) != string(once) {
		t.Errorf("second pass changed content: %s then %s", once, twice)
	}
}

func TestConflictCopyInheritsContentKind(t *testing.T) {
	if got := pathContentKindFor("projects/x/s.jsonl.conflict.20260101-120000"); got != pathContentJSON {
		t.Errorf("conflict copy of .jsonl = %v, want JSON", got)
	}
	if got := pathContentKindFor("projects/x/n.md.conflict.20260101-120000"); got != pathContentPlain {
		t.Errorf("conflict copy of .md = %v, want plain", got)
	}
}

// A backslash is a legal character in a POSIX directory name, so "contains a
// backslash" is not the same question as "is a Windows path". Getting those
// confused rewrites a POSIX device's own "/" separators into backslashes.
func TestWindowsPathDetection(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\Users\bob`, true},
		{`d:\projects`, true},
		{`\\server\share`, true},
		{`C:`, true},
		{`/Users/alice`, false},
		{`/Users/al\ice`, false},
		{`/home/user/My\ Documents`, false},
	}

	for _, tc := range cases {
		if got := isWindowsLocalPath(tc.path); got != tc.want {
			t.Errorf("isWindowsLocalPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// A POSIX home holding a backslash has to be matched in its escaped spelling in
// .jsonl and emitted the same way, or the token expands to an invalid escape
// sequence and the transcript stops parsing.
func TestPosixHomeContainingBackslashRoundTrips(t *testing.T) {
	m, err := NewPathMapper(`/Users/al\ice`, nil)
	if err != nil {
		t.Fatalf("NewPathMapper: %v", err)
	}

	cases := []struct {
		name       string
		relPath    string
		in, remote string
	}{
		{"jsonl escapes the backslash", sessionFile, `{"cwd":"/Users/al\\ice/projects/app"}`, `{"cwd":"${HOME}/projects/app"}`},
		{"markdown holds it raw", memoFile, `see /Users/al\ice/projects/app`, `see ${HOME}/projects/app`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := m.NormalizeContent(tc.relPath, []byte(tc.in))
			if string(remote) != tc.remote {
				t.Errorf("NormalizeContent = %s, want %s", remote, tc.remote)
			}
			if got := string(m.ResolveContent(tc.relPath, remote)); got != tc.in {
				t.Errorf("round trip changed content\n in  = %s\n out = %s", tc.in, got)
			}
		})
	}

	// The resolved .jsonl still has to parse.
	remote := []byte(`{"cwd":"${HOME}/projects/app"}`)
	out := m.ResolveContent(sessionFile, remote)
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("resolved content is not valid JSON: %v\ncontent: %s", err, out)
	}
	if v["cwd"] != `/Users/al\ice/projects/app` {
		t.Errorf("decoded cwd = %v", v["cwd"])
	}
}

// A Windows root is often configured with forward slashes, because "C:\work" is
// not a valid escape in YAML. It must behave the same as the native spelling.
func TestWindowsRootConfiguredWithForwardSlashes(t *testing.T) {
	m, err := NewPathMapper(winHome, map[string]string{`C:/work/projects`: "WORK"})
	if err != nil {
		t.Fatalf("NewPathMapper: %v", err)
	}

	in := `{"cwd":"C:\\work\\projects\\app"}`
	remote := m.NormalizeContent(sessionFile, []byte(in))
	if string(remote) != `{"cwd":"${WORK}/app"}` {
		t.Errorf("NormalizeContent = %s, want %s", remote, `{"cwd":"${WORK}/app"}`)
	}
	if got := string(m.ResolveContent(sessionFile, remote)); got != in {
		t.Errorf("round trip = %s, want %s", got, in)
	}
}

//	/ mainResolve replicate the algorithm on main: replace only the
//
// root prefix, boundary-aware, and expand the token to the raw local path.
func rootOnlyNormalize(home string, data []byte) []byte {
	re := regexp.MustCompile(regexp.QuoteMeta(home) + `([^A-Za-z0-9_.-]|$)`)
	return re.ReplaceAll(data, []byte("$${HOME}${1}"))
}

func rootOnlyResolve(home string, data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "${HOME}", home))
}

// Separator-aware mapping must be byte-identical to the root-only mapping for a
// POSIX device whose home holds no backslash - that is every existing POSIX
// user. Anything else would change the meaning of content already on a bucket.
func TestPosixContentMappingIsBackwardCompatible(t *testing.T) {
	const home = "/Users/alice"
	m, err := NewPathMapper(home, nil)
	if err != nil {
		t.Fatal(err)
	}

	corpus := []string{
		`{"cwd":"/Users/alice/projects/app"}`,
		`{"cwd":"/Users/alice"}`,
		`/Users/alice`,
		`/Users/alicia/x`,
		`/Users/alice.bak/x`,
		`/Users/alice-old/x`,
		`{"f":"/Users/alice/.claude/settings.json","g":"/Users/alice/a/b/c.txt"}`,
		`see /Users/alice/Library/Application Support/Code/User`,
		`{"command":"cd /Users/alice/My\\ Documents && ls"}`,
		`{"t":"/Users/alice/projects\nnext"}`,
		`{"err":"no such file: /Users/alice/x/y.go:12"}`,
		`{"a":"/Users/alice/x","b":"/Users/alice/x"}`,
		`prefix/Users/alice/x`,
		`{"u":"https://example.com/Users/alice/x"}`,
		`{"p":"/Users/alice//double/slash"}`,
		`{"p":"/Users/alice/trailing/"}`,
		`{"home":"${HOME}/already/tokenized"}`,
		`/Users/alice/a_b-c.d/e`,
		`{"m":"/Users/alice/x", "n":"/Users/alicebob/y"}`,
	}

	for _, relPath := range []string{"projects/x/s.jsonl", "projects/x/n.md"} {
		for _, in := range corpus {
			wantRemote := string(rootOnlyNormalize(home, []byte(in)))
			gotRemote := string(m.NormalizeContent(relPath, []byte(in)))
			if gotRemote != wantRemote {
				t.Errorf("[%s] normalize diverges\n in   = %s\n old  = %s\n new  = %s", relPath, in, wantRemote, gotRemote)
			}

			wantLocal := string(rootOnlyResolve(home, []byte(wantRemote)))
			gotLocal := string(m.ResolveContent(relPath, []byte(gotRemote)))
			if gotLocal != wantLocal {
				t.Errorf("[%s] resolve diverges\n remote = %s\n old    = %s\n new    = %s", relPath, gotRemote, wantLocal, gotLocal)
			}
		}
	}
}

// The greedy tail consumes a root nested inside another path in a single match,
// so normalize has to repeat or the nested occurrence is uploaded verbatim and
// never resolves on the other device.
func TestNestedRootOccurrenceIsTokenized(t *testing.T) {
	m := mapperFor(t, posixHome, posixRoot)

	cases := []struct{ in, want string }{
		{`/Users/alice/a/Users/alice/b`, `${HOME}/a${HOME}/b`},
		{`/Users/alice/backup/Users/alice/old/x`, `${HOME}/backup${HOME}/old/x`},
		{`/Users/alice/projects/a/Users/alice/projects/b`, `${WORK}/a${WORK}/b`},
		// A longer sibling must still not match, and must not loop forever.
		{`/Users/alice/x /Users/alicexyz/y`, `${HOME}/x /Users/alicexyz/y`},
	}

	for _, tc := range cases {
		if got := string(m.NormalizeContent(memoFile, []byte(tc.in))); got != tc.want {
			t.Errorf("NormalizeContent(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// A trailing backslash is a separator to strip only on Windows. On POSIX it is
// part of the directory name, so stripping it would silently widen the token to
// a different directory.
func TestTrailingBackslashKeptOnPosixRoot(t *testing.T) {
	m, err := NewPathMapper("/Users/alice", map[string]string{`/Users/alice/odd\`: "ODD"})
	if err != nil {
		t.Fatalf("NewPathMapper: %v", err)
	}

	if got := string(m.NormalizeContent(memoFile, []byte(`/Users/alice/odd/sub/x`))); strings.Contains(got, "${ODD}") {
		t.Errorf("the odd/ subtree was captured by ODD: %s", got)
	}
	if got := string(m.NormalizeContent(memoFile, []byte(`/Users/alice/odd\/sub/x`))); !strings.Contains(got, "${ODD}") {
		t.Errorf(`the odd\ directory was not matched: %s`, got)
	}
}
