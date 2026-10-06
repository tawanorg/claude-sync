package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testOrg = "3d583d51-f79f-4cd8-a62d-780a3f79886c"

// ccdRecord builds a minimal desktop-session record like the app writes.
func ccdRecord(sessionID, title string, lastActivityAt int64) string {
	return `{"sessionId":"` + sessionID + `","cliSessionId":"11111111-2222-4333-8444-555555555555",` +
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
// install-id directory (different per machine, as in real installs).
func newCCDMachine(t *testing.T, store *mockStorage, installID string) (*testEnv, string) {
	t.Helper()
	env := newTestMachine(t, store)
	ccdDir := filepath.Join(t.TempDir(), "claude-code-sessions")
	if err := os.MkdirAll(filepath.Join(ccdDir, installID, testOrg), 0700); err != nil {
		t.Fatal(err)
	}
	env.syncer.SetCCDSessionsDir(ccdDir)
	return env, ccdDir
}

func writeCCDRecord(t *testing.T, ccdDir, installID, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ccdDir, installID, testOrg, name), []byte(content), 0600); err != nil {
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
	// Direct layout.
	profile := t.TempDir()
	direct := filepath.Join(profile, "AppData", "Roaming", "Claude", "claude-code-sessions")
	if err := os.MkdirAll(direct, 0700); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{claudeDir: filepath.Join(profile, ".claude")}
	if got := s.ccdSessionsDir(); got != direct {
		t.Errorf("direct layout: got %q, want %q", got, direct)
	}

	// MSIX layout (no accessible Roaming path).
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

	// B holds an older copy; a pull must overwrite it with the newer one.
	writeCCDRecord(t, ccdB, "install-bbb", "local_abc.json", older)
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
