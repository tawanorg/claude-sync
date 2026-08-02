package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"
)

func historyLine(display string, ts int64, session string) string {
	return `{"display":"` + display + `","pastedContents":{},"timestamp":` +
		strconv.FormatInt(ts, 10) + `,"project":"/home/u/proj","sessionId":"` + session + `"}`
}

func TestMergeHistoryPayloadsUnionsDistinctEntries(t *testing.T) {
	local := historyLine("local prompt", 2000, "s-local") + "\n"
	remote := historyLine("remote prompt", 1000, "s-remote") + "\n"

	merged, added, err := MergeHistoryPayloads([]byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("MergeHistoryPayloads failed: %v", err)
	}
	if len(added) != 1 {
		t.Errorf("added = %d, want 1", len(added))
	}
	out := string(merged)
	if !strings.Contains(out, "local prompt") || !strings.Contains(out, "remote prompt") {
		t.Errorf("merged payload missing entries:\n%s", out)
	}
}

// Local bytes are the prefix of the merge output, verbatim — the merge never
// rewrites or reorders what the local file already contains, so a concurrent
// appender or an unparseable line can never be destroyed by a rewrite.
func TestMergeHistoryPayloadsPreservesLocalVerbatimAndAppends(t *testing.T) {
	unparseable := `{"display":"torn line without closing brace`
	local := historyLine("newer local", 5000, "s1") + "\n" + unparseable + "\n"
	remote := historyLine("older remote", 1000, "s2") + "\n"

	merged, added, err := MergeHistoryPayloads([]byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("MergeHistoryPayloads failed: %v", err)
	}
	if len(added) != 1 {
		t.Errorf("added = %d, want 1", len(added))
	}
	out := string(merged)
	if !strings.HasPrefix(out, local) {
		t.Errorf("local payload is not a verbatim prefix of the merge output:\n%s", out)
	}
	if !strings.Contains(out, unparseable) {
		t.Errorf("unparseable local line dropped:\n%s", out)
	}
	if strings.Index(out, "older remote") < strings.Index(out, "newer local") {
		t.Errorf("remote entries must be appended after local content:\n%s", out)
	}
}

// Merging a payload with itself must be a no-op: this is the invariant that
// prevents entries from multiplying as the same content cycles between
// machines and the bucket.
func TestMergeHistoryPayloadsIsIdempotent(t *testing.T) {
	payload := historyLine("prompt one", 1000, "s1") + "\n" +
		`{"display":"","pastedContents":{},"timestamp":1500,"project":"/p","sessionId":"s1"}` + "\n" +
		`{"someFutureField":true}` + "\n" +
		historyLine("prompt two", 2000, "s2") + "\n"

	merged, added, err := MergeHistoryPayloads([]byte(payload), []byte(payload))
	if err != nil {
		t.Fatalf("MergeHistoryPayloads failed: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("Merge(x, x) added %d lines, want 0: %q", len(added), added)
	}
	if string(merged) != payload {
		t.Errorf("Merge(x, x) != x:\ngot  %q\nwant %q", merged, payload)
	}
}

// Empty-display lines (blank submissions) and JSON lines of unknown shape
// have no sessionId+display signature, so they dedupe by exact raw bytes.
// Without this they would be re-added on every sync cycle and multiply.
func TestMergeHistoryPayloadsDedupesEmptyDisplayByRawBytes(t *testing.T) {
	blank := `{"display":"","pastedContents":{},"timestamp":500,"project":"/p","sessionId":"s0"}`
	local := blank + "\n" + historyLine("real prompt", 1000, "s1") + "\n"
	remote := blank + "\n" + historyLine("remote prompt", 2000, "s2") + "\n"

	merged, added, err := MergeHistoryPayloads([]byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("MergeHistoryPayloads failed: %v", err)
	}
	if len(added) != 1 {
		t.Errorf("added = %d, want 1 (only the remote prompt): %q", len(added), added)
	}
	if strings.Count(string(merged), `"sessionId":"s0"`) != 1 {
		t.Errorf("blank-submission line duplicated:\n%s", merged)
	}
}

func TestMergeHistoryPayloadsDedupesAndKeepsLocalRaw(t *testing.T) {
	// Same prompt on both sides (same session + display, close timestamps);
	// the local line carries a payload the remote line lacks and must win.
	local := `{"display":"shared","pastedContents":{"1":{"content":"KEEP"}},"timestamp":3000,"project":"/p","sessionId":"s1"}` + "\n"
	remote := historyLine("shared", 3500, "s1") + "\n" +
		historyLine("remote only", 9000, "s2") + "\n"

	merged, added, err := MergeHistoryPayloads([]byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("MergeHistoryPayloads failed: %v", err)
	}
	if len(added) != 1 {
		t.Errorf("added = %d, want 1 (only the remote-only entry)", len(added))
	}
	out := string(merged)
	if strings.Count(out, `"shared"`) != 1 {
		t.Errorf("shared entry duplicated:\n%s", out)
	}
	if !strings.Contains(out, "KEEP") {
		t.Errorf("local raw line was not preserved verbatim:\n%s", out)
	}
	if !strings.Contains(out, "remote only") {
		t.Errorf("remote-only entry missing:\n%s", out)
	}
}

func TestMergeHistoryPayloadsEmptySides(t *testing.T) {
	line := historyLine("only", 1000, "s1") + "\n"

	merged, added, err := MergeHistoryPayloads(nil, []byte(line))
	if err != nil {
		t.Fatalf("empty local: %v", err)
	}
	if len(added) != 1 || !strings.Contains(string(merged), "only") {
		t.Errorf("empty local: added=%d merged=%q", len(added), merged)
	}

	merged, added, err = MergeHistoryPayloads([]byte(line), nil)
	if err != nil {
		t.Fatalf("empty remote: %v", err)
	}
	if len(added) != 0 || !strings.Contains(string(merged), "only") {
		t.Errorf("empty remote: added=%d merged=%q", len(added), merged)
	}
}

// newTestMachine builds a Syncer with its own claude/state dirs but a shared
// mock storage, simulating one of several machines syncing the same bucket.
// All machines derive the same key from the same passphrase, as in real use.
func newTestMachine(t *testing.T, store storage.Storage) *testEnv {
	t.Helper()
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	stateDir := filepath.Join(tmpDir, ".claude-sync")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(stateDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "test-passphrase-shared-1"); err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{
		syncer: &Syncer{
			storage:   store,
			encryptor: enc,
			state:     state,
			claudeDir: claudeDir,
			quiet:     true,
			cfg:       &config.Config{Scope: config.ScopeFull},
		},
		store:     nil,
		claudeDir: claudeDir,
		stateDir:  stateDir,
	}
}

func assertNoConflictFiles(t *testing.T, claudeDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(claudeDir, "*.conflict.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 0 {
		t.Errorf("conflict files created for history.jsonl: %v", matches)
	}
}

func TestPushMergesRemoteHistoryIntoUpload(t *testing.T) {
	store := newMockStorage()
	machineA := newTestMachine(t, store)
	machineB := newTestMachine(t, store)
	ctx := context.Background()

	writeFile(t, machineA.claudeDir, HistoryFile, historyLine("prompt from A", 1000, "s-a")+"\n")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	// B pushes its own history with no knowledge of A's. Without merge-on-push
	// this replaces A's entries in the bucket (last-writer-wins).
	writeFile(t, machineB.claudeDir, HistoryFile, historyLine("prompt from B", 2000, "s-b")+"\n")
	if _, err := machineB.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}

	// Merge-on-push must also fold A's entries into B's local file.
	bLocal := readFile(t, machineB.claudeDir, HistoryFile)
	if !strings.Contains(bLocal, "prompt from A") || !strings.Contains(bLocal, "prompt from B") {
		t.Errorf("B's local history is not the union after push:\n%s", bLocal)
	}

	// A pulls: the bucket copy must contain the union.
	res, err := machineA.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("A pull: %v", err)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("pull reported conflicts: %v", res.Conflicts)
	}
	aLocal := readFile(t, machineA.claudeDir, HistoryFile)
	if !strings.Contains(aLocal, "prompt from A") || !strings.Contains(aLocal, "prompt from B") {
		t.Errorf("bucket history was clobbered instead of merged:\n%s", aLocal)
	}
	assertNoConflictFiles(t, machineA.claudeDir)
}

func TestPullMergesHistoryOnFirstSync(t *testing.T) {
	store := newMockStorage()
	machineA := newTestMachine(t, store)
	machineB := newTestMachine(t, store)
	ctx := context.Background()

	writeFile(t, machineA.claudeDir, HistoryFile, historyLine("prompt from A", 1000, "s-a")+"\n")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	// B has pre-existing local history and no sync state (first sync). The
	// local file is newer than the remote object; it must still be merged,
	// not skipped and not overwritten.
	writeFile(t, machineB.claudeDir, HistoryFile, historyLine("prompt from B", 2000, "s-b")+"\n")
	res, err := machineB.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("pull reported conflicts: %v", res.Conflicts)
	}
	bLocal := readFile(t, machineB.claudeDir, HistoryFile)
	if !strings.Contains(bLocal, "prompt from A") || !strings.Contains(bLocal, "prompt from B") {
		t.Errorf("first-sync pull did not merge history:\n%s", bLocal)
	}
	assertNoConflictFiles(t, machineB.claudeDir)
}

func TestPullMergesHistoryWhenBothSidesChanged(t *testing.T) {
	store := newMockStorage()
	machineA := newTestMachine(t, store)
	machineB := newTestMachine(t, store)
	ctx := context.Background()

	// B establishes state, then A pushes more history, then B changes locally:
	// the classic both-sides-changed case that used to produce a .conflict file.
	writeFile(t, machineB.claudeDir, HistoryFile, historyLine("B first", 1000, "s-b1")+"\n")
	if _, err := machineB.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}
	writeFile(t, machineA.claudeDir, HistoryFile, historyLine("A first", 2000, "s-a1")+"\n")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}
	writeFile(t, machineB.claudeDir, HistoryFile,
		historyLine("B first", 1000, "s-b1")+"\n"+historyLine("B second", 3000, "s-b2")+"\n")

	res, err := machineB.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("history should merge, not conflict: %v", res.Conflicts)
	}
	assertNoConflictFiles(t, machineB.claudeDir)

	bLocal := readFile(t, machineB.claudeDir, HistoryFile)
	for _, want := range []string{"A first", "B first", "B second"} {
		if !strings.Contains(bLocal, want) {
			t.Errorf("merged history missing %q:\n%s", want, bLocal)
		}
	}

	// B's next push must publish the union to the bucket.
	if _, err := machineB.syncer.Push(ctx); err != nil {
		t.Fatalf("B push 2: %v", err)
	}
	if _, err := machineA.syncer.Pull(ctx); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	aLocal := readFile(t, machineA.claudeDir, HistoryFile)
	for _, want := range []string{"A first", "B first", "B second"} {
		if !strings.Contains(aLocal, want) {
			t.Errorf("bucket union missing %q after round-trip:\n%s", want, aLocal)
		}
	}
}

// A fleet-wide cycle must converge: once every machine holds the union,
// further pushes and pulls must not modify any file or re-add any line.
func TestHistorySyncCycleConverges(t *testing.T) {
	store := newMockStorage()
	machineA := newTestMachine(t, store)
	machineB := newTestMachine(t, store)
	ctx := context.Background()

	// Include a blank submission — the classic multiplication trigger.
	blank := `{"display":"","pastedContents":{},"timestamp":1500,"project":"/p","sessionId":"s-a"}`
	writeFile(t, machineA.claudeDir, HistoryFile,
		historyLine("from A", 1000, "s-a")+"\n"+blank+"\n")
	writeFile(t, machineB.claudeDir, HistoryFile, historyLine("from B", 2000, "s-b")+"\n")

	// Two full sync cycles for each machine.
	for i := 0; i < 2; i++ {
		if _, err := machineA.syncer.Push(ctx); err != nil {
			t.Fatalf("A push %d: %v", i, err)
		}
		if _, err := machineB.syncer.Pull(ctx); err != nil {
			t.Fatalf("B pull %d: %v", i, err)
		}
		if _, err := machineB.syncer.Push(ctx); err != nil {
			t.Fatalf("B push %d: %v", i, err)
		}
		if _, err := machineA.syncer.Pull(ctx); err != nil {
			t.Fatalf("A pull %d: %v", i, err)
		}
	}

	for name, dir := range map[string]string{"A": machineA.claudeDir, "B": machineB.claudeDir} {
		content := readFile(t, dir, HistoryFile)
		if got := strings.Count(content, `"timestamp":1500`); got != 1 {
			t.Errorf("machine %s: blank-submission line multiplied to %d copies:\n%s", name, got, content)
		}
		for _, want := range []string{"from A", "from B"} {
			if got := strings.Count(content, want); got != 1 {
				t.Errorf("machine %s: %q appears %d times, want 1", name, want, got)
			}
		}
	}
}

// A remote history object that exists but cannot be decoded must abort the
// history upload: uploading local-only content would clobber the union of
// entries that could not be merged.
func TestPushAbortsWhenRemoteHistoryUnreadable(t *testing.T) {
	store := newMockStorage()
	machine := newTestMachine(t, store)
	ctx := context.Background()

	garbage := []byte("not an age-encrypted payload")
	if err := store.Upload(ctx, "history.jsonl.age", garbage); err != nil {
		t.Fatal(err)
	}

	localContent := historyLine("local prompt", 1000, "s1") + "\n"
	writeFile(t, machine.claudeDir, HistoryFile, localContent)

	res, err := machine.syncer.Push(ctx)
	if err == nil && len(res.Errors) == 0 {
		t.Fatal("push should report an error when the remote history cannot be decoded")
	}

	remote, err := store.Download(ctx, "history.jsonl.age")
	if err != nil {
		t.Fatal(err)
	}
	if string(remote) != string(garbage) {
		t.Error("remote history object was overwritten despite the merge being impossible")
	}
	if got := readFile(t, machine.claudeDir, HistoryFile); got != localContent {
		t.Errorf("local history modified on aborted push: %q", got)
	}
}

// headFailStore simulates a transient storage failure on Head (timeout,
// throttle, 5xx) — indistinguishable from not-found before storage.ErrNotFound,
// which made the push silently degrade to last-writer-wins.
type headFailStore struct {
	*mockStorage
}

func (h *headFailStore) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	return nil, errors.New("connection timed out")
}

func TestPushAbortsOnTransientHeadError(t *testing.T) {
	store := newMockStorage()
	seeder := newTestMachine(t, store)
	ctx := context.Background()

	writeFile(t, seeder.claudeDir, HistoryFile, historyLine("existing union", 1000, "s0")+"\n")
	if _, err := seeder.syncer.Push(ctx); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	remoteBefore, err := store.Download(ctx, "history.jsonl.age")
	if err != nil {
		t.Fatal(err)
	}

	machine := newTestMachine(t, &headFailStore{mockStorage: store})
	writeFile(t, machine.claudeDir, HistoryFile, historyLine("local only", 2000, "s1")+"\n")

	res, err := machine.syncer.Push(ctx)
	if err == nil && len(res.Errors) == 0 {
		t.Fatal("push should report an error when Head fails transiently")
	}

	remoteAfter, err := store.Download(ctx, "history.jsonl.age")
	if err != nil {
		t.Fatal(err)
	}
	if string(remoteAfter) != string(remoteBefore) {
		t.Error("transient Head failure let the push clobber the bucket union")
	}
}

// Deleting the local history.jsonl must not delete the bucket union: the
// bucket copy only ever grows, and the next pull restores the file locally.
func TestPushDoesNotDeleteRemoteHistory(t *testing.T) {
	store := newMockStorage()
	machine := newTestMachine(t, store)
	ctx := context.Background()

	writeFile(t, machine.claudeDir, HistoryFile, historyLine("precious", 1000, "s1")+"\n")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	if err := os.Remove(filepath.Join(machine.claudeDir, HistoryFile)); err != nil {
		t.Fatal(err)
	}
	res, err := machine.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push after delete: %v", err)
	}
	for _, d := range res.Deleted {
		if d == HistoryFile {
			t.Error("history.jsonl reported as remotely deleted")
		}
	}
	if _, err := store.Download(ctx, "history.jsonl.age"); err != nil {
		t.Errorf("bucket history union was deleted: %v", err)
	}

	// The next pull restores the file locally.
	if _, err := machine.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := readFile(t, machine.claudeDir, HistoryFile); !strings.Contains(got, "precious") {
		t.Errorf("pull did not restore deleted local history: %q", got)
	}
}

// The merge paths must never rewrite the local file — unparseable local lines
// (e.g. a line torn by a crash) survive every sync untouched.
func TestPullMergePreservesUnparseableLocalLines(t *testing.T) {
	store := newMockStorage()
	machineA := newTestMachine(t, store)
	machineB := newTestMachine(t, store)
	ctx := context.Background()

	writeFile(t, machineA.claudeDir, HistoryFile, historyLine("from A", 1000, "s-a")+"\n")
	if _, err := machineA.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	torn := `{"display":"half a li`
	localBefore := historyLine("from B", 2000, "s-b") + "\n" + torn + "\n"
	writeFile(t, machineB.claudeDir, HistoryFile, localBefore)

	if _, err := machineB.syncer.Pull(ctx); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	got := readFile(t, machineB.claudeDir, HistoryFile)
	if !strings.HasPrefix(got, localBefore) {
		t.Errorf("local file was rewritten instead of appended to:\ngot  %q\nwant prefix %q", got, localBefore)
	}
	if !strings.Contains(got, "from A") {
		t.Errorf("remote entry not appended:\n%q", got)
	}
}
