package sync

import (
	"context"
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
