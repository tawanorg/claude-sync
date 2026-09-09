package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testOrg = "3d583d51-f79f-4cd8-a62d-780a3f79886c"

// testCLISessionID is the cliSessionId every ccdRecord fixture (and most
// hand-written record JSON in these tests) carries. newCCDMachine gives it a
// matching transcript so its records count as synced and actually push/pull.
const testCLISessionID = "11111111-2222-4333-8444-555555555555"

// ccdRecord builds a minimal desktop-session record like the app writes.
func ccdRecord(sessionID, title string, lastActivityAt int64) string {
	return `{"sessionId":"` + sessionID + `","cliSessionId":"` + testCLISessionID + `",` +
		`"cwd":"C:\\Users\\alice\\code\\proj","title":"` + title + `",` +
		`"isArchived":false,"lastActivityAt":` + itoa64(lastActivityAt) + `}`
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// newCCDMachine is newTestMachine plus a desktop-app session store with one
// install-id directory (different per machine, as in real installs), and a
// stub transcript for testCLISessionID so its records count as synced.
func newCCDMachine(t *testing.T, store *mockStorage, installID string) (*testEnv, string) {
	t.Helper()
	env := newTestMachine(t, store)
	ccdDir := filepath.Join(t.TempDir(), "claude-code-sessions")
	if err := os.MkdirAll(filepath.Join(ccdDir, installID, testOrg), 0700); err != nil {
		t.Fatal(err)
	}
	env.syncer.SetCCDSessionsDir(ccdDir)
	writeCCDTranscript(t, env.claudeDir, testCLISessionID)
	return env, ccdDir
}

// writeCCDTranscript creates a minimal transcript so cliSessionID counts as
// synced by ccdSyncedSessionIDs — required for its Desktop pointer to push
// or pull at all.
func writeCCDTranscript(t *testing.T, claudeDir, cliSessionID string) {
	t.Helper()
	writeFile(t, claudeDir, filepath.Join("projects", "-test-project", cliSessionID+".jsonl"), "{}\n")
}

func writeCCDRecord(t *testing.T, ccdDir, installID, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ccdDir, installID, testOrg, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// backdateCCDRecord ages a pointer file's mtime past ccdPointerFreshWindow,
// simulating one Desktop wrote a while ago rather than one this test just
// wrote — otherwise the pull freshness guard refuses to touch it.
func backdateCCDRecord(t *testing.T, ccdDir, installID, name string) {
	t.Helper()
	path := filepath.Join(ccdDir, installID, testOrg, name)
	old := time.Now().Add(-2 * ccdPointerFreshWindow)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func readCCDRecord(t *testing.T, ccdDir, installID, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ccdDir, installID, testOrg, name))
	if err != nil {
		t.Fatalf("record not found: %v", err)
	}
	return string(data)
}

// Detection must find the store in both real-world layouts: the direct
// install path under AppData\Roaming, and the MSIX-packaged physical path
// under AppData\Local\Packages (the Roaming path is an MSIX redirect there
// that WSL's drvfs cannot traverse — verified on a real machine).
func TestCCDSessionsDirDetection(t *testing.T) {
	// ccdSessionsDir branches on runtime.GOOS, so the expected direct-layout
	// path (and whether the MSIX fallback applies at all) must follow suit —
	// otherwise this only passes when run on Windows.
	t.Setenv("XDG_CONFIG_HOME", "")

	// Direct layout.
	profile := t.TempDir()
	var direct string
	switch runtime.GOOS {
	case "windows":
		direct = filepath.Join(profile, "AppData", "Roaming", "Claude", "claude-code-sessions")
	case "darwin":
		direct = filepath.Join(profile, "Library", "Application Support", "Claude", "claude-code-sessions")
	default:
		direct = filepath.Join(profile, ".config", "Claude", "claude-code-sessions")
	}
	if err := os.MkdirAll(direct, 0700); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{claudeDir: filepath.Join(profile, ".claude")}
	if got := s.ccdSessionsDir(); got != direct {
		t.Errorf("direct layout: got %q, want %q", got, direct)
	}

	// MSIX fallback is Windows-only: on other OSes ccdSessionsDir returns ""
	// immediately without checking the filesystem, so nothing to assert there.
	if runtime.GOOS == "windows" {
		profile2 := t.TempDir()
		msix := filepath.Join(profile2, "AppData", "Local", "Packages", "Claude_pzs8sxrjxfjjc",
			"LocalCache", "Roaming", "Claude", "claude-code-sessions")
		if err := os.MkdirAll(msix, 0700); err != nil {
			t.Fatal(err)
		}
		s2 := &Syncer{claudeDir: filepath.Join(profile2, ".claude")}
		if got := s2.ccdSessionsDir(); got != msix {
			t.Errorf("msix layout: got %q, want %q", got, msix)
		}
	}

	// No store at all.
	s3 := &Syncer{claudeDir: filepath.Join(t.TempDir(), ".claude")}
	if got := s3.ccdSessionsDir(); got != "" {
		t.Errorf("no store: got %q, want empty", got)
	}
}

// A record pushed from machine A must materialize on machine B under B's own
// install-id directory (install-id is machine-specific and remapped).
func TestCCDRecordSyncsAcrossMachines(t *testing.T) {
	store := newMockStorage()
	machineA, ccdA := newCCDMachine(t, store, "install-aaa")
	machineB, ccdB := newCCDMachine(t, store, "install-bbb")
	ctx := context.Background()

	rec := ccdRecord("local_abc", "Prod cutover", 1000)
	writeCCDRecord(t, ccdA, "install-aaa", "local_abc.json", rec)
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a")

	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}
	if _, err := machineB.syncer.Pull(ctx); err != nil {
		t.Fatalf("B pull: %v", err)
	}

	// Records are normalized in transit (sorted keys, local-only permission
	// fields stripped), so compare normalized forms.
	got := readCCDRecord(t, ccdB, "install-bbb", "local_abc.json")
	if got != string(ccdStripLocalFields([]byte(rec))) {
		t.Errorf("record content mismatch:\ngot  %s\nwant normalized %s", got, rec)
	}
}

// TestCCDRecordCwdTranslatedAcrossOSes reproduces the actual bug found via
// manual testing: a session record pushed from a Linux machine (cwd
// /home/user/claude/blink-re) and pulled onto a Windows machine must arrive
// with cwd rewritten to that machine's own path, and — since a Windows home
// path contains backslashes, a JSON string-escape character — the result
// must still be valid JSON. Before this fix, ccdRecord's cwd was carried
// across verbatim; Desktop then couldn't locate the project (wrong OS's
// path) or, worse, choked on a malformed record.
func TestCCDRecordCwdTranslatedAcrossOSes(t *testing.T) {
	store := newMockStorage()
	linuxMachine, ccdLinux := newCCDMachine(t, store, "install-linux")
	winMachine, ccdWin := newCCDMachine(t, store, "install-win")
	linuxMachine.syncer.paths = mustMapper(t, "/home/user", nil)
	winMachine.syncer.paths = mustMapper(t, `C:\Users\alice`, nil)
	ctx := context.Background()

	rec := `{"sessionId":"local_abc","cliSessionId":"11111111-2222-4333-8444-555555555555",` +
		`"cwd":"/home/user/claude/blink-re","originCwd":"/home/user/claude/blink-re",` +
		`"title":"Blink-re repository review","isArchived":false,"lastActivityAt":1000}`
	writeCCDRecord(t, ccdLinux, "install-linux", "local_abc.json", rec)
	writeFile(t, linuxMachine.claudeDir, "CLAUDE.md", "# a")

	if _, err := linuxMachine.syncer.Push(ctx); err != nil {
		t.Fatalf("linux push: %v", err)
	}
	if _, err := winMachine.syncer.Pull(ctx); err != nil {
		t.Fatalf("windows pull: %v", err)
	}

	got := readCCDRecord(t, ccdWin, "install-win", "local_abc.json")
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("pulled record is not valid JSON: %v\ngot: %s", err, got)
	}
	if cwd := v["cwd"]; cwd != `C:\Users\alice/claude/blink-re` {
		t.Errorf("cwd = %q, want %q", cwd, `C:\Users\alice/claude/blink-re`)
	}
	if origCwd := v["originCwd"]; origCwd != `C:\Users\alice/claude/blink-re` {
		t.Errorf("originCwd = %q, want %q", origCwd, `C:\Users\alice/claude/blink-re`)
	}
}

// When both sides hold the same record, the one with the higher
// lastActivityAt wins in both directions.
func TestCCDNewerRecordWins(t *testing.T) {
	store := newMockStorage()
	machineA, ccdA := newCCDMachine(t, store, "install-aaa")
	machineB, ccdB := newCCDMachine(t, store, "install-bbb")
	ctx := context.Background()

	older := ccdRecord("local_abc", "title v1", 1000)
	newer := ccdRecord("local_abc", "title v2", 2000)

	// A pushes the newer state first.
	writeCCDRecord(t, ccdA, "install-aaa", "local_abc.json", newer)
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	// B holds an older copy that isn't being actively written right now (its
	// mtime is outside ccdPointerFreshWindow); a pull must overwrite it with
	// the newer one.
	writeCCDRecord(t, ccdB, "install-bbb", "local_abc.json", older)
	backdateCCDRecord(t, ccdB, "install-bbb", "local_abc.json")
	if _, err := machineB.syncer.Pull(ctx); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if got := readCCDRecord(t, ccdB, "install-bbb", "local_abc.json"); got != string(ccdStripLocalFields([]byte(newer))) {
		t.Errorf("pull did not adopt newer remote record: %s", got)
	}

	// B now regresses its copy to the older record; a push must NOT clobber
	// the newer remote copy.
	writeCCDRecord(t, ccdB, "install-bbb", "local_abc.json", older)
	if _, err := machineB.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}
	if _, err := machineA.syncer.Pull(ctx); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	if got := readCCDRecord(t, ccdA, "install-aaa", "local_abc.json"); got != newer {
		t.Errorf("older record clobbered the newer remote copy:\n%s", got)
	}
}

// Deleting a record locally (or having none) must never delete bucket
// records: the registry only grows, like history.
func TestCCDPushDoesNotDeleteRemoteRecords(t *testing.T) {
	store := newMockStorage()
	machineA, ccdA := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdA, "install-aaa", "local_abc.json", ccdRecord("local_abc", "keep me", 1000))
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("push 1: %v", err)
	}

	if err := os.Remove(filepath.Join(ccdA, "install-aaa", testOrg, "local_abc.json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a2")
	res, err := machineA.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push 2: %v", err)
	}
	for _, d := range res.Deleted {
		if strings.Contains(d, "local_abc") {
			t.Errorf("ccd record deleted remotely: %s", d)
		}
	}
	if _, err := store.Head(ctx, "_ccd-sessions/"+testOrg+"/local_abc.json"); err != nil {
		t.Errorf("bucket record deleted: %v", err)
	}
}

// A machine without a desktop-app store (no install dir) must sync normally
// and simply skip the feature.
func TestCCDSyncSkippedWithoutStore(t *testing.T) {
	store := newMockStorage()
	machine := newTestMachine(t, store) // no SetCCDSessionsDir
	ctx := context.Background()

	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := machine.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
}

// Only local_*.json files are session records; anything else in the store
// directory stays local.
func TestCCDOnlySessionRecordsSync(t *testing.T) {
	store := newMockStorage()
	machineA, ccdA := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdA, "install-aaa", "local_abc.json", ccdRecord("local_abc", "real", 1000))
	writeCCDRecord(t, ccdA, "install-aaa", "notes.txt", "junk")
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	if _, err := store.Head(ctx, "_ccd-sessions/"+testOrg+"/local_abc.json"); err != nil {
		t.Errorf("session record not uploaded: %v", err)
	}
	if _, err := store.Head(ctx, "_ccd-sessions/"+testOrg+"/notes.txt"); err == nil {
		t.Error("non-record file was uploaded")
	}
}

// When two install dirs (reinstalls) hold the same record, the LWW winner is
// pushed — not whichever directory sorts last.
func TestCCDInstallDirCollisionKeepsNewest(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(ccdDir, "install-zzz", testOrg), 0700); err != nil {
		t.Fatal(err)
	}
	writeCCDRecord(t, ccdDir, "install-aaa", "local_abc.json", ccdRecord("local_abc", "newest", 2000))
	writeCCDRecord(t, ccdDir, "install-zzz", "local_abc.json", ccdRecord("local_abc", "stale", 1000))

	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	other, otherDir := newCCDMachine(t, store, "install-bbb")
	if _, err := other.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	got := readCCDRecord(t, otherDir, "install-bbb", "local_abc.json")
	if !strings.Contains(got, "newest") {
		t.Errorf("stale install-dir copy won the push: %s", got)
	}
}

// A crafted remote key must not be able to write outside the session store:
// org and file segments come from the bucket, which the threat model treats
// as hostile.
func TestCCDPullRejectsTraversalKeys(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	// Plant hostile keys directly in the bucket, encrypted with the right key
	// (a compromised bucket + leaked passphrase scenario).
	payload := []byte(`{"lastActivityAt":99999999}`)
	for _, key := range []string{
		"_ccd-sessions/../local_evil.json",
		"_ccd-sessions/..\\../local_evil2.json",
		"_ccd-sessions/org/../local_evil3.json",
	} {
		compressed, _ := gzipCompress(payload)
		encrypted, err := machine.syncer.encryptor.Encrypt(compressed)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Upload(ctx, key, encrypted); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := machine.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Nothing may exist outside <ccdDir>/install-aaa/<org>/ — in particular
	// nothing at the install dir's parent or inside install-aaa directly.
	if _, err := os.Stat(filepath.Join(ccdDir, "install-aaa", "local_evil.json")); err == nil {
		t.Error("traversal key wrote into the install dir")
	}
	if _, err := os.Stat(filepath.Join(ccdDir, "local_evil.json")); err == nil {
		t.Error("traversal key escaped to the store root")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ccdDir), "local_evil.json")); err == nil {
		t.Error("traversal key escaped the store entirely")
	}
	for _, n := range []string{"local_evil2.json", "local_evil3.json"} {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(ccdDir), "**", n))
		if len(matches) > 0 {
			t.Errorf("hostile key %s materialized: %v", n, matches)
		}
	}
}

// A pointer whose cliSessionId has no matching transcript under
// ~/.claude/projects must not be pushed: the other machine would only get a
// sidebar entry with nothing behind it.
func TestCCDPushSkipsPointerWithoutSyncedTranscript(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	// newCCDMachine already planted a transcript for testCLISessionID;
	// remove it so this machine has no synced transcript for the pointer.
	if err := os.RemoveAll(filepath.Join(machine.claudeDir, "projects")); err != nil {
		t.Fatal(err)
	}

	writeCCDRecord(t, ccdDir, "install-aaa", "local_abc.json", ccdRecord("local_abc", "orphan", 1000))
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	if _, err := store.Head(ctx, "_ccd-sessions/"+testOrg+"/local_abc.json"); err == nil {
		t.Error("pointer without a synced transcript was pushed")
	}
}

// A remote pointer whose cliSessionId has no matching transcript on this
// machine must not be materialized: it would be a dead sidebar entry here.
func TestCCDPullSkipsPointerWithoutSyncedTranscript(t *testing.T) {
	store := newMockStorage()
	pusher, ccdPusher := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdPusher, "install-aaa", "local_abc.json", ccdRecord("local_abc", "shared", 1000))
	writeFile(t, pusher.claudeDir, "CLAUDE.md", "# a")
	if _, err := pusher.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	puller, ccdPuller := newCCDMachine(t, store, "install-bbb")
	// This machine excludes the project the pointer's transcript lives in, so
	// it never has (and never will, even via the regular file pull that just
	// ran) a copy of it.
	puller.syncer.cfg.Exclude = []string{"projects/-test-project/**"}
	if _, err := puller.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}

	if _, err := os.Stat(filepath.Join(ccdPuller, "install-bbb", testOrg, "local_abc.json")); err == nil {
		t.Error("pointer without a synced transcript was materialized")
	}
}

// A local pointer modified very recently must not be overwritten by pull,
// even when the remote copy is logically newer: a fresh mtime most likely
// means Desktop has the session open on this machine right now.
func TestCCDPullSkipsFreshLocalPointer(t *testing.T) {
	store := newMockStorage()
	machineA, ccdA := newCCDMachine(t, store, "install-aaa")
	machineB, ccdB := newCCDMachine(t, store, "install-bbb")
	ctx := context.Background()

	writeCCDRecord(t, ccdA, "install-aaa", "local_abc.json", ccdRecord("local_abc", "title v2", 2000))
	writeFile(t, machineA.claudeDir, "CLAUDE.md", "# a")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	// B's copy is older by lastActivityAt but was just written (as if
	// Desktop is mid-edit on this machine right now).
	writeCCDRecord(t, ccdB, "install-bbb", "local_abc.json", ccdRecord("local_abc", "title v1", 1000))
	if _, err := machineB.syncer.Pull(ctx); err != nil {
		t.Fatalf("B pull: %v", err)
	}

	got := readCCDRecord(t, ccdB, "install-bbb", "local_abc.json")
	if !strings.Contains(got, "title v1") {
		t.Errorf("pull clobbered a freshly-written local pointer: %s", got)
	}
}
