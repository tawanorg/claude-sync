package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	norm := alice.NormalizeContent(in, true)

	want := `{"cwd":"${HOME}/nexura","note":"see /Users/mervynlally/nexura and ${HOME}"}`
	if string(norm) != want {
		t.Fatalf("NormalizeContent = %s, want %s", norm, want)
	}

	resolved := bob.ResolveContent(norm, true)
	wantResolved := `{"cwd":"/Users/mervynlally/nexura","note":"see /Users/mervynlally/nexura and /Users/mervynlally"}`
	if string(resolved) != wantResolved {
		t.Fatalf("ResolveContent = %s, want %s", resolved, wantResolved)
	}
}

func TestContentBoundaries(t *testing.T) {
	m := mustMapper(t, "/Users/merv", nil)

	// dotted and dashed continuations are part of a different name, not a boundary
	for _, s := range []string{"/Users/merv.bak/x", "/Users/merv-old/x", "/Users/mervyn/x"} {
		if got := m.NormalizeContent([]byte(s), false); string(got) != s {
			t.Errorf("NormalizeContent(%q) = %q, should be untouched", s, got)
		}
	}
	// end of data is a boundary
	if got := m.NormalizeContent([]byte("/Users/merv"), false); string(got) != "${HOME}" {
		t.Errorf("NormalizeContent at EOF = %q", got)
	}
}

// TestResolveContentJSONMode_ProducesValidJSON reproduces the actual bug
// found via manual testing: a Linux session (home /home/user, no backslash)
// pulled onto Windows (home C:\Users\austi, backslash — a JSON string-escape
// character). Resolving ${HOME} with the raw path corrupts the JSON on every
// line that embeds a cwd — 44 of 59 lines in the real transcript that
// surfaced this. jsonMode=true must produce valid, round-trippable JSON.
func TestResolveContentJSONMode_ProducesValidJSON(t *testing.T) {
	linux := mustMapper(t, "/home/user", nil)
	windows := mustMapper(t, `C:\Users\austi`, nil)

	// A realistic transcript line: cwd appears standalone, and again inside a
	// larger string (a tool command echoing the path), both common in
	// real transcripts and both needing JSON-safe substitution.
	in := []byte(`{"cwd":"/home/user/claude/blink-re","message":{"content":"find /home/user/claude/blink-re -maxdepth 1"}}`)

	norm := linux.NormalizeContent(in, true)
	wantNorm := `{"cwd":"${HOME}/claude/blink-re","message":{"content":"find ${HOME}/claude/blink-re -maxdepth 1"}}`
	if string(norm) != wantNorm {
		t.Fatalf("NormalizeContent = %s, want %s", norm, wantNorm)
	}

	resolved := windows.ResolveContent(norm, true)

	// The fix's job is JSON validity, not separator cosmetics: only the
	// ${HOME} prefix is substituted, so the rest of the path — written by
	// the Linux source — keeps its forward slashes. Mixed separators in the
	// resulting string are syntactically fine (forward slash needs no JSON
	// escaping), which is exactly why this must still parse cleanly.
	var v map[string]interface{}
	if err := json.Unmarshal(resolved, &v); err != nil {
		t.Fatalf("resolved content is not valid JSON: %v\ngot: %s", err, resolved)
	}
	if got := v["cwd"]; got != `C:\Users\austi/claude/blink-re` {
		t.Errorf("cwd = %q, want %q", got, `C:\Users\austi/claude/blink-re`)
	}
	msg, _ := v["message"].(map[string]interface{})
	wantContent := `find C:\Users\austi/claude/blink-re -maxdepth 1`
	if got := msg["content"]; got != wantContent {
		t.Errorf("message.content = %q, want %q", got, wantContent)
	}
}

// TestResolveContentNonJSONMode_StaysRaw guards the .md/.txt path: those are
// freeform text, not JSON, so the substituted path must stay in raw form —
// JSON-escaping it there would incorrectly double every backslash in a
// human-readable file.
func TestResolveContentNonJSONMode_StaysRaw(t *testing.T) {
	windows := mustMapper(t, `C:\Users\austi`, nil)
	in := []byte(`See ${HOME}\claude\blink-re for details.`)
	resolved := windows.ResolveContent(in, false)
	want := `See C:\Users\austi\claude\blink-re for details.`
	if string(resolved) != want {
		t.Errorf("ResolveContent(jsonMode=false) = %q, want %q", resolved, want)
	}
}

// TestNormalizeContentJSONMode_MatchesAlreadyEscapedPath covers the reverse
// direction: content serialized by encoding/json on a Windows source already
// has doubled backslashes before it ever reaches NormalizeContent (e.g. a
// Desktop session record round-tripped through json.Marshal). The raw-path
// regex used for .md/.txt can never match that doubled form, so JSON content
// must search for the JSON-escaped form specifically.
func TestNormalizeContentJSONMode_MatchesAlreadyEscapedPath(t *testing.T) {
	windows := mustMapper(t, `C:\Users\austi`, nil)
	// This is what json.Marshal(map[string]string{"cwd": `C:\Users\austi\blink-re`})
	// actually produces on disk: doubled backslashes.
	in := []byte(`{"cwd":"C:\\Users\\austi\\blink-re"}`)
	norm := windows.NormalizeContent(in, true)
	// Only the matched home prefix is replaced; the untouched "\blink-re"
	// remainder keeps its own JSON escaping (two literal bytes for that one
	// logical backslash) exactly as it appeared in the input.
	want := `{"cwd":"${HOME}\\blink-re"}`
	if string(norm) != want {
		t.Fatalf("NormalizeContent(jsonMode=true) = %s, want %s", norm, want)
	}
}

// TestContentRoundTrip_LinuxToWindowsToLinux mirrors the actual jumpbox ->
// laptop -> jumpbox path a session takes in practice, confirming the fix is
// symmetric: content pushed from Linux, resolved on Windows, must itself
// still be valid, and pushing that Windows-resolved content back must
// recover the original Linux-form content exactly.
func TestContentRoundTrip_LinuxToWindowsToLinux(t *testing.T) {
	linux := mustMapper(t, "/home/user", nil)
	windows := mustMapper(t, `C:\Users\austi`, nil)

	original := []byte(`{"cwd":"/home/user/claude/blink-re","cliSessionId":"8e3fefc8"}`)

	onWire := linux.NormalizeContent(original, true)
	onWindows := windows.ResolveContent(onWire, true)

	var v map[string]interface{}
	if err := json.Unmarshal(onWindows, &v); err != nil {
		t.Fatalf("content materialized on Windows is not valid JSON: %v\ngot: %s", err, onWindows)
	}

	backOnWire := windows.NormalizeContent(onWindows, true)
	backOnLinux := linux.ResolveContent(backOnWire, true)
	if string(backOnLinux) != string(original) {
		t.Errorf("round trip mismatch:\n got: %s\nwant: %s", backOnLinux, original)
	}
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
