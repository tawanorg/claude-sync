package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustMapper(t *testing.T, home string, userMap map[string]string) *PathMapper {
	t.Helper()
	m, err := NewPathMapper(home, userMap)
	if err != nil {
		t.Fatalf("NewPathMapper: %v", err)
	}
	return m
}

func TestEncodeClaudePath(t *testing.T) {
	cases := map[string]string{
		"/Users/alice/my-app":      "-Users-alice-my-app",
		"/Users/merv/.config/brc":  "-Users-merv--config-brc",
		"C:\\Users\\merv\\app_1":   "C--Users-merv-app-1",
		"/home/bob/Projects/RedXY": "-home-bob-Projects-RedXY",
	}
	for in, want := range cases {
		if got := EncodeClaudePath(in); got != want {
			t.Errorf("EncodeClaudePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeResolveRelPath(t *testing.T) {
	alice := mustMapper(t, "/Users/alice", map[string]string{"/Users/alice/work": "WORK"})
	bob := mustMapper(t, "/Users/bob", map[string]string{"/Users/bob/Projects": "WORK"})

	cases := []struct {
		local      string // on alice's machine
		normalized string
		onBob      string
	}{
		{
			"projects/-Users-alice-my-app/sess.jsonl",
			"projects/${HOME}-my-app/sess.jsonl",
			"projects/-Users-bob-my-app/sess.jsonl",
		},
		{
			// most specific mapping wins over HOME
			"projects/-Users-alice-work-api/sess.jsonl",
			"projects/${WORK}-api/sess.jsonl",
			"projects/-Users-bob-Projects-api/sess.jsonl",
		},
		{
			// exact project at the mapped root
			"projects/-Users-alice-work/sess.jsonl",
			"projects/${WORK}/sess.jsonl",
			"projects/-Users-bob-Projects/sess.jsonl",
		},
		{
			// non-projects paths untouched
			"settings.json",
			"settings.json",
			"settings.json",
		},
		{
			// foreign machine's directory left alone
			"projects/-Users-zed-app/sess.jsonl",
			"projects/-Users-zed-app/sess.jsonl",
			"projects/-Users-zed-app/sess.jsonl",
		},
	}

	for _, c := range cases {
		norm := alice.NormalizeRelPath(c.local)
		if norm != c.normalized {
			t.Errorf("NormalizeRelPath(%q) = %q, want %q", c.local, norm, c.normalized)
			continue
		}
		resolved, ok := bob.ResolveRelPath(norm)
		if !ok {
			t.Errorf("ResolveRelPath(%q): unexpectedly unresolvable", norm)
			continue
		}
		if resolved != c.onBob {
			t.Errorf("ResolveRelPath(%q) = %q, want %q", norm, resolved, c.onBob)
		}
	}
}

func TestNormalizeRelPathUsernamePrefixTrap(t *testing.T) {
	// /Users/merv must not match inside /Users/mervynlally's encoded dirs
	merv := mustMapper(t, "/Users/merv", nil)
	in := "projects/-Users-mervynlally-nexura/sess.jsonl"
	if got := merv.NormalizeRelPath(in); got != in {
		t.Errorf("NormalizeRelPath(%q) = %q, want unchanged", in, got)
	}
	// but its own dirs do match
	own := "projects/-Users-merv-nexura/sess.jsonl"
	if got := merv.NormalizeRelPath(own); got != "projects/${HOME}-nexura/sess.jsonl" {
		t.Errorf("NormalizeRelPath(%q) = %q", own, got)
	}
}

func TestResolveRelPathUnknownToken(t *testing.T) {
	m := mustMapper(t, "/Users/bob", nil)
	if _, ok := m.ResolveRelPath("projects/${WORK}-api/sess.jsonl"); ok {
		t.Error("expected unknown token to be unresolvable")
	}
}

func TestContentRoundTrip(t *testing.T) {
	alice := mustMapper(t, "/Users/merv", nil)
	bob := mustMapper(t, "/Users/mervynlally", nil)

	in := []byte(`{"cwd":"/Users/merv/nexura","note":"see /Users/mervynlally/nexura and /Users/merv"}`)
	norm := alice.NormalizeContent(in)

	want := `{"cwd":"${HOME}/nexura","note":"see /Users/mervynlally/nexura and ${HOME}"}`
	if string(norm) != want {
		t.Fatalf("NormalizeContent = %s, want %s", norm, want)
	}

	resolved := bob.ResolveContent(norm)
	wantResolved := `{"cwd":"/Users/mervynlally/nexura","note":"see /Users/mervynlally/nexura and /Users/mervynlally"}`
	if string(resolved) != wantResolved {
		t.Fatalf("ResolveContent = %s, want %s", resolved, wantResolved)
	}
}

func TestResolveContentCrossOSSeparators(t *testing.T) {
	win := mustMapper(t, `C:\Users\bob`, nil)

	got := string(win.ResolveContent([]byte(`{"cwd":"${HOME}/Developer/TypeScript/hivemind","bare":"${HOME}"}`)))
	want := `{"cwd":"C:\Users\bob\Developer\TypeScript\hivemind","bare":"C:\Users\bob"}`
	if got != want {
		t.Fatalf("ResolveContent cross-OS = %s, want %s", got, want)
	}

	mac := mustMapper(t, "/Users/bob", nil)
	got = string(mac.ResolveContent([]byte(`{"cwd":"${HOME}\Developer\hivemind"}`)))
	want = `{"cwd":"/Users/bob/Developer/hivemind"}`
	if got != want {
		t.Fatalf("ResolveContent win->posix = %s, want %s", got, want)
	}
}

func TestResolveFileJSONLEscaping(t *testing.T) {
	win := mustMapper(t, `C:\Users\bob`, nil)

	line := []byte(`{"type":"user","cwd":"${HOME}/Developer/hivemind","home":"${HOME}"}`)
	got := win.ResolveFile("projects/-x/sess.jsonl", line)

	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("resolved JSONL line is not valid JSON: %v\n%s", err, got)
	}
	if doc["cwd"] != `C:\Users\bob\Developer\hivemind` {
		t.Fatalf("cwd = %q, want native Windows path", doc["cwd"])
	}
	if doc["home"] != `C:\Users\bob` {
		t.Fatalf("home = %q", doc["home"])
	}

	// Round trip: normalizing the resolved line on macOS returns the token form.
	mac := mustMapper(t, "/Users/bob", nil)
	back := mac.NormalizeFile("projects/-x/sess.jsonl", mac.ResolveFile("projects/-x/sess.jsonl", line))
	var backDoc map[string]any
	if err := json.Unmarshal(back, &backDoc); err != nil {
		t.Fatalf("round-tripped line invalid: %v", err)
	}
	if backDoc["cwd"] != "${HOME}/Developer/hivemind" {
		t.Fatalf("round-trip cwd = %q, want token form", backDoc["cwd"])
	}
}

func TestMapJSONLPreservesLines(t *testing.T) {
	win := mustMapper(t, `C:\Users\bob`, nil)

	in := []byte("{\"cwd\":\"${HOME}/a\"}\n{\"cwd\":\"${HOME}/b\"}\n")
	got := win.ResolveFile("projects/-x/sess.jsonl", in)

	lines := bytes.Split(bytes.TrimRight(got, "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got)
	}
	for _, l := range lines {
		if !json.Valid(l) {
			t.Fatalf("line not valid JSON: %s", l)
		}
	}
	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Fatal("trailing newline not preserved")
	}
}

func TestContentBoundaries(t *testing.T) {
	m := mustMapper(t, "/Users/merv", nil)

	// dotted and dashed continuations are part of a different name, not a boundary
	for _, s := range []string{"/Users/merv.bak/x", "/Users/merv-old/x", "/Users/mervyn/x"} {
		if got := m.NormalizeContent([]byte(s)); string(got) != s {
			t.Errorf("NormalizeContent(%q) = %q, should be untouched", s, got)
		}
	}
	// end of data is a boundary
	if got := m.NormalizeContent([]byte("/Users/merv")); string(got) != "${HOME}" {
		t.Errorf("NormalizeContent at EOF = %q", got)
	}
}

// mustSymlinkedHome creates a realPath directory and a symlink pointing at it,
// returning both paths. It skips the test when this OS/user cannot create
// symlinks (unprivileged Windows without developer mode).
func mustSymlinkedHome(t *testing.T) (link, realPath string) {
	t.Helper()
	tmp := t.TempDir()
	realPath = filepath.Join(tmp, "Projects")
	if err := os.MkdirAll(realPath, 0700); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(tmp, "Developer")
	if err := os.Symlink(realPath, link); err != nil {
		t.Skipf("cannot create symlinks on this system: %v", err)
	}
	// EvalSymlinks also canonicalizes the parent chain (e.g. macOS /var ->
	// /private/var), so compare against the same canonical form the mapper uses.
	resolved, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatal(err)
	}
	return link, resolved
}

func TestSymlinkedHomeNormalizesToRealPath(t *testing.T) {
	link, realPath := mustSymlinkedHome(t)
	m := mustMapper(t, link, nil)

	// A cwd recorded through the symlink and the same cwd recorded through the
	// realPath path must produce the same token.
	viaLink := m.NormalizeFile("projects/-x/sess.jsonl", []byte(`{"cwd":`+mustJSONString(t, filepath.Join(link, "app"))+`}`))
	viaReal := m.NormalizeFile("projects/-x/sess.jsonl", []byte(`{"cwd":`+mustJSONString(t, filepath.Join(realPath, "app"))+`}`))

	for _, got := range [][]byte{viaLink, viaReal} {
		var doc map[string]string
		if err := json.Unmarshal(got, &doc); err != nil {
			t.Fatalf("normalized line invalid: %v (%s)", err, got)
		}
		if want := "${HOME}/app"; doc["cwd"] != want {
			t.Errorf("cwd = %q, want %q", doc["cwd"], want)
		}
	}

	// Raw (non-JSON) content takes the same route.
	if got := string(m.NormalizeContent([]byte(filepath.Join(link, "app") + " "))); got != "${HOME}"+string(filepath.Separator)+"app " {
		t.Errorf("NormalizeContent via symlink = %q", got)
	}
}

func TestSymlinkedHomeResolvesToRealPath(t *testing.T) {
	link, realPath := mustSymlinkedHome(t)
	m := mustMapper(t, link, nil)

	got := m.ResolveFile("projects/-x/sess.jsonl", []byte(`{"cwd":"${HOME}/app"}`))
	var doc map[string]string
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("resolved line invalid: %v (%s)", err, got)
	}
	want := filepath.Join(realPath, "app")
	if doc["cwd"] != want {
		t.Errorf("cwd = %q, want realPath path %q", doc["cwd"], want)
	}
	if strings.HasPrefix(doc["cwd"], link) {
		t.Errorf("cwd kept the symlink prefix: %q", doc["cwd"])
	}
}

func TestSymlinkedHomeRelPathBothEncodings(t *testing.T) {
	link, realPath := mustSymlinkedHome(t)
	m := mustMapper(t, link, nil)

	viaLink := "projects/" + EncodeClaudePath(link) + "-app/sess.jsonl"
	viaReal := "projects/" + EncodeClaudePath(realPath) + "-app/sess.jsonl"
	want := "projects/${HOME}-app/sess.jsonl"

	for _, in := range []string{viaLink, viaReal} {
		if got := m.NormalizeRelPath(in); got != want {
			t.Errorf("NormalizeRelPath(%q) = %q, want %q", in, got, want)
		}
	}

	// Pull writes under the realPath path, where `claude --resume` looks.
	got, ok := m.ResolveRelPath(want)
	if !ok {
		t.Fatalf("ResolveRelPath(%q) unresolvable", want)
	}
	if got != viaReal {
		t.Errorf("ResolveRelPath(%q) = %q, want %q", want, got, viaReal)
	}
}

func TestSymlinkedPathMapEntry(t *testing.T) {
	link, realPath := mustSymlinkedHome(t)
	m := mustMapper(t, filepath.Join(t.TempDir(), "home"), map[string]string{link: "WORK"})

	in := "projects/" + EncodeClaudePath(link) + "-api/sess.jsonl"
	want := "projects/${WORK}-api/sess.jsonl"
	if got := m.NormalizeRelPath(in); got != want {
		t.Errorf("NormalizeRelPath(%q) = %q, want %q", in, got, want)
	}

	got, ok := m.ResolveRelPath(want)
	if !ok {
		t.Fatalf("ResolveRelPath(%q) unresolvable", want)
	}
	if wantReal := "projects/" + EncodeClaudePath(realPath) + "-api/sess.jsonl"; got != wantReal {
		t.Errorf("ResolveRelPath(%q) = %q, want %q", want, got, wantReal)
	}
}

func TestMissingDirectoryKeepsLiteralPath(t *testing.T) {
	// A prefix that does not exist on this device must still map, unchanged.
	absent := filepath.Join(t.TempDir(), "no-such-dir")
	m := mustMapper(t, absent, nil)

	in := "projects/" + EncodeClaudePath(absent) + "-app/sess.jsonl"
	if got := m.NormalizeRelPath(in); got != "projects/${HOME}-app/sess.jsonl" {
		t.Errorf("NormalizeRelPath(%q) = %q", in, got)
	}
	got, ok := m.ResolveRelPath("projects/${HOME}-app/sess.jsonl")
	if !ok || got != in {
		t.Errorf("ResolveRelPath = (%q, %v), want (%q, true)", got, ok, in)
	}
}

func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPathMapperValidation(t *testing.T) {
	if _, err := NewPathMapper("/Users/a", map[string]string{"/x": "home"}); err == nil {
		t.Error("expected reserved-name error for HOME (case-insensitive)")
	}
	if _, err := NewPathMapper("/Users/a", map[string]string{"/x": "my-work"}); err == nil {
		t.Error("expected invalid token name error")
	}
	if _, err := NewPathMapper("/Users/a", map[string]string{"/x": "WORK_2"}); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
}

func TestIsPortableContentPath(t *testing.T) {
	cases := map[string]bool{
		"history.jsonl":                                           true,
		"projects/-Users-a-x/sess.jsonl":                          true,
		"projects/-Users-a-x/memory/notes.md":                     true,
		"projects/-Users-a-x/sess.jsonl.conflict.20260610-120000": true,
		"projects/-Users-a-x/img.png":                             false,
		"settings.json":                                           false,
		"agents/foo.md":                                           false,
		"plugins/known_marketplaces.json":                         true,
		"plugins/installed_plugins.json":                          true,
		"plugins/cache/foo/plugin.json":                           false,
		"plugins/marketplaces/foo/.git/config":                    false,
	}
	for in, want := range cases {
		if got := IsPortableContentPath(in); got != want {
			t.Errorf("IsPortableContentPath(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestCrossDeviceSessionSync simulates two devices with different usernames
// sharing one bucket: a session pushed from alice's machine must land on
// bob's machine under bob's encoded project directory with rewritten content.
func TestCrossDeviceSessionSync(t *testing.T) {
	syncerA, store, claudeDirA := testSyncer(t)
	syncerA.paths = mustMapper(t, "/Users/alice", nil)

	sessDir := filepath.Join(claudeDirA, "projects", "-Users-alice-my-app")
	if err := os.MkdirAll(sessDir, 0700); err != nil {
		t.Fatal(err)
	}
	content := `{"cwd":"/Users/alice/my-app","type":"user"}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "sess.jsonl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := syncerA.Push(context.Background()); err != nil {
		t.Fatalf("push: %v", err)
	}

	wantKey := "projects/${HOME}-my-app/sess.jsonl.age"
	if _, err := store.Download(context.Background(), wantKey); err != nil {
		t.Fatalf("expected normalized remote key %s: %v", wantKey, err)
	}

	// Second device: same bucket and key, different username
	tmpB := t.TempDir()
	claudeDirB := filepath.Join(tmpB, ".claude")
	if err := os.MkdirAll(claudeDirB, 0755); err != nil {
		t.Fatal(err)
	}
	stateB, err := LoadStateFromDir(tmpB)
	if err != nil {
		t.Fatal(err)
	}
	syncerB := NewSyncerWith(syncerA.cfg, store, syncerA.encryptor, stateB, claudeDirB, true)
	syncerB.paths = mustMapper(t, "/Users/bob", nil)

	result, err := syncerB.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("pull errors: %v", result.Errors)
	}

	localPath := filepath.Join(claudeDirB, "projects", "-Users-bob-my-app", "sess.jsonl")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("expected session under bob's project dir: %v", err)
	}
	want := `{"cwd":"/Users/bob/my-app","type":"user"}` + "\n"
	if string(data) != want {
		t.Errorf("content = %s, want %s", data, want)
	}

	info, _ := os.Stat(localPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("downloaded file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestMigratePaths(t *testing.T) {
	syncer, store, claudeDir := testSyncer(t)
	ctx := context.Background()

	// Create the local session file
	sessDir := filepath.Join(claudeDir, "projects", "-Users-alice-my-app")
	if err := os.MkdirAll(sessDir, 0700); err != nil {
		t.Fatal(err)
	}
	relPath := "projects/-Users-alice-my-app/sess.jsonl"
	if err := os.WriteFile(filepath.Join(claudeDir, relPath), []byte(`{"cwd":"/Users/alice/my-app"}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Push under legacy (identity) keys, as an old version would have
	syncer.paths = mustMapper(t, "/nonexistent-home-zz", nil)
	if _, err := syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Download(ctx, relPath+".age"); err != nil {
		t.Fatalf("legacy key missing after push: %v", err)
	}

	// A key owned by another device: no local copy here
	if err := store.Upload(ctx, "projects/-Users-zed-other/x.jsonl.age", []byte("opaque")); err != nil {
		t.Fatal(err)
	}

	// Upgrade: mapper now knows this machine is alice's
	syncer.paths = mustMapper(t, "/Users/alice", nil)

	result, err := syncer.MigratePaths(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("migrate errors: %v", result.Errors)
	}
	if len(result.Migrated) != 1 || result.Migrated[0] != relPath {
		t.Errorf("Migrated = %v, want [%s]", result.Migrated, relPath)
	}
	if len(result.Foreign) != 1 || result.Foreign[0] != "projects/-Users-zed-other/x.jsonl" {
		t.Errorf("Foreign = %v", result.Foreign)
	}

	if _, err := store.Download(ctx, relPath+".age"); err == nil {
		t.Error("legacy key still present after migrate")
	}
	if _, err := store.Download(ctx, "projects/${HOME}-my-app/sess.jsonl.age"); err != nil {
		t.Errorf("normalized key missing after migrate: %v", err)
	}
	if _, err := store.Download(ctx, "projects/-Users-zed-other/x.jsonl.age"); err != nil {
		t.Errorf("foreign key should be untouched: %v", err)
	}

	// Second run is a no-op for this device
	result2, err := syncer.MigratePaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Migrated) != 0 {
		t.Errorf("second migrate should migrate nothing, got %v", result2.Migrated)
	}
}

const marketplacesPath = "plugins/known_marketplaces.json"

func TestPluginStateJSONRoundTrip(t *testing.T) {
	mac := mustMapper(t, "/Users/alice", nil)
	local := []byte(`{"m":{"installLocation":"/Users/alice/.claude/plugins/marketplaces/x"}}`)

	normalized := mac.NormalizeFile(marketplacesPath, local)
	if !bytes.Contains(normalized, []byte("${HOME}/.claude/plugins/marketplaces/x")) {
		t.Fatalf("normalize did not tokenize home: %s", normalized)
	}

	back := mac.ResolveFile(marketplacesPath, normalized)
	var got map[string]map[string]string
	if err := json.Unmarshal(back, &got); err != nil {
		t.Fatalf("resolved content is not valid JSON: %v (%s)", err, back)
	}
	want := "/Users/alice/.claude/plugins/marketplaces/x"
	if got["m"]["installLocation"] != want {
		t.Errorf("round trip = %q, want %q", got["m"]["installLocation"], want)
	}
}

func TestNormalizeJSONPreservesEscaping(t *testing.T) {
	win := mustMapper(t, `C:\Users\bob`, nil)
	local, err := json.Marshal(map[string]string{
		"installPath": `C:\Users\bob\.claude\plugins\cache\p`,
	})
	if err != nil {
		t.Fatal(err)
	}

	normalized := win.NormalizeFile(marketplacesPath, local)

	var got map[string]string
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatalf("normalized content is not valid JSON: %v (%s)", err, normalized)
	}
	// The token form is separator-neutral; resolve applies the target's.
	if want := "${HOME}/.claude/plugins/cache/p"; got["installPath"] != want {
		t.Errorf("normalized = %q, want %q", got["installPath"], want)
	}
}

func TestPluginStateWindowsToUnix(t *testing.T) {
	win := mustMapper(t, `C:\Users\bob`, nil)
	mac := mustMapper(t, "/Users/alice", nil)

	local, err := json.Marshal(map[string]string{
		"installLocation": `C:\Users\bob\.claude\plugins\marketplaces\x`,
	})
	if err != nil {
		t.Fatal(err)
	}

	onMac := mac.ResolveFile(marketplacesPath, win.NormalizeFile(marketplacesPath, local))

	var got map[string]string
	if err := json.Unmarshal(onMac, &got); err != nil {
		t.Fatalf("not valid JSON: %v (%s)", err, onMac)
	}
	if want := "/Users/alice/.claude/plugins/marketplaces/x"; got["installLocation"] != want {
		t.Errorf("windows -> unix = %q, want %q", got["installLocation"], want)
	}
	if strings.Contains(got["installLocation"], `\`) {
		t.Errorf("result kept windows separators: %q", got["installLocation"])
	}
}

func TestNormalizeJSONLeavesUnrelatedBytes(t *testing.T) {
	m := mustMapper(t, "/Users/alice", nil)
	in := []byte(`{"url":"https://x.test/r?a=1&b=2","note":"a < b && c","port":49152}`)

	normalized := m.NormalizeFile(marketplacesPath, in)

	// HTML metacharacters and integers must survive verbatim: only path
	// prefixes are translated, and nothing here begins with a mapped path.
	for _, want := range []string{`"https://x.test/r?a=1&b=2"`, `"a < b && c"`, `"port": 49152`} {
		if !bytes.Contains(normalized, []byte(want)) {
			t.Errorf("normalize altered unrelated content, missing %s:\n%s", want, normalized)
		}
	}
}

func TestJSONPathBoundaries(t *testing.T) {
	m := mustMapper(t, "/Users/merv", nil)

	// a longer username must not match the shorter mapped prefix
	in, err := json.Marshal(map[string]string{"p": "/Users/mervynlally/x"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(m.NormalizeFile(marketplacesPath, in), &got); err != nil {
		t.Fatal(err)
	}
	if got["p"] != "/Users/mervynlally/x" {
		t.Errorf("normalized = %q, want unchanged", got["p"])
	}

	// the mapped path itself, with no remainder, still tokenizes
	in, err = json.Marshal(map[string]string{"p": "/Users/merv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(m.NormalizeFile(marketplacesPath, in), &got); err != nil {
		t.Fatal(err)
	}
	if got["p"] != "${HOME}" {
		t.Errorf("normalized = %q, want ${HOME}", got["p"])
	}
}

func TestSplitConflictPath(t *testing.T) {
	cases := []struct {
		in        string
		base      string
		timestamp string
		ok        bool
	}{
		{"plugins/known_marketplaces.json", "plugins/known_marketplaces.json", "", false},
		{"a/sess.jsonl.conflict.20260610-120000", "a/sess.jsonl", "20260610-120000", true},
		// a conflict copy of a conflict copy still reports the original file
		{"a/x.json.conflict.A.conflict.B", "a/x.json", "A.conflict.B", true},
		// a parent directory containing the marker is not a conflict copy
		{"a.conflict.b/sess.jsonl", "a.conflict.b/sess.jsonl", "", false},
		{`a.conflict.b\sess.jsonl`, `a.conflict.b\sess.jsonl`, "", false},
	}
	for _, c := range cases {
		base, ts, ok := SplitConflictPath(c.in)
		if base != c.base || ts != c.timestamp || ok != c.ok {
			t.Errorf("SplitConflictPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, base, ts, ok, c.base, c.timestamp, c.ok)
		}
	}
}

func TestConflictPathRoundTrip(t *testing.T) {
	at := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	p := ConflictPath("plugins/known_marketplaces.json", at)

	base, ts, ok := SplitConflictPath(p)
	if !ok {
		t.Fatalf("SplitConflictPath(%q) reported no conflict", p)
	}
	if base != "plugins/known_marketplaces.json" || ts != "20260610-120000" {
		t.Errorf("round trip = (%q, %q)", base, ts)
	}

	// the copy is translated as JSON, like the file it came from: raw byte
	// replacement would leave the escaped Windows path invalid.
	win := mustMapper(t, `C:\Users\bob`, nil)
	local, err := json.Marshal(map[string]string{"installLocation": `C:\Users\bob\x`})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(win.NormalizeFile(p, local), &got); err != nil {
		t.Fatalf("conflict copy not valid JSON: %v", err)
	}
	if got["installLocation"] != "${HOME}/x" {
		t.Errorf("conflict copy not translated: %q", got["installLocation"])
	}
}
