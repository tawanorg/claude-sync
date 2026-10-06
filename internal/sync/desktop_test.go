package sync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/desktop"
	"github.com/tawanorg/claude-sync/internal/storage"
)

func makeIndex(t *testing.T, records map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acct", "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range records {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The desktop sidebar renders from an index this tool did not previously sync.
// A conversation that reaches a second machine through ~/.claude alone is
// resumable from the CLI but invisible in the sidebar; carrying the index
// records is what closes that gap.
func TestDesktopPushPullRoundTrip(t *testing.T) {
	ctx := context.Background()

	envA := setupTestEnv(t)
	envA.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_one.json": `{"sessionId":"local_one","cliSessionId":"aaa","isArchived":false,"quirk":7}`,
	})

	pushed, err := envA.syncer.PushDesktop(ctx)
	if err != nil {
		t.Fatalf("PushDesktop() error: %v", err)
	}
	if pushed.RecordsPushed != 1 {
		t.Fatalf("RecordsPushed = %d, want 1", pushed.RecordsPushed)
	}

	// A second machine sharing the same bucket and key, with an empty index.
	stateB, err := LoadStateFromDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Machine B must hold the transcript, or the record is correctly skipped as
	// one that would produce a dead sidebar row.
	claudeB := t.TempDir()
	projB := filepath.Join(claudeB, "projects", "-Users-x-proj")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, "aaa.jsonl"), []byte(`{"type":"user"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	syncerB := NewSyncerWith(envA.syncer.cfg, envA.store, envA.syncer.encryptor, stateB, claudeB, true)
	indexB := makeIndex(t, nil)
	syncerB.desktopDir = indexB

	pulled, err := syncerB.PullDesktop(ctx)
	if err != nil {
		t.Fatalf("PullDesktop() error: %v", err)
	}
	if pulled.Written != 1 {
		t.Errorf("Written = %d, want 1", pulled.Written)
	}

	labels, err := desktop.Scan(indexB)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := labels["aaa"]; !ok {
		t.Fatalf("session aaa did not reach the second machine; got %v", labels)
	}

	// The app owns this schema: fields we do not model must survive the trip.
	got, err := os.ReadFile(filepath.Join(indexB, "local_one.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsKey(got, "quirk") {
		t.Errorf("unknown field dropped in transit: %s", got)
	}
}

// Pushing an unchanged index must not re-upload, matching how MCP push behaves.
func TestDesktopPushSkipsUnchangedIndex(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)
	env.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_one.json": `{"sessionId":"local_one","cliSessionId":"aaa","isArchived":false}`,
	})

	if _, err := env.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := env.syncer.PushDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged {
		t.Errorf("second PushDesktop() Unchanged = false, want true")
	}
}

// A machine with no desktop app must push and pull without error.
func TestDesktopOpsAreNoOpWithoutIndex(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)
	env.syncer.desktopDir = filepath.Join(t.TempDir(), "absent")

	pushed, err := env.syncer.PushDesktop(ctx)
	if err != nil {
		t.Fatalf("PushDesktop() on missing index: %v", err)
	}
	if pushed.RecordsPushed != 0 {
		t.Errorf("RecordsPushed = %d, want 0", pushed.RecordsPushed)
	}

	if _, err := env.syncer.PullDesktop(ctx); err != nil {
		t.Fatalf("PullDesktop() on missing index: %v", err)
	}
}

func containsKey(data []byte, key string) bool {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// A sidebar entry points at a transcript by id. Writing one for a transcript
// this machine does not have produces a row that opens onto "Session not found
// on disk" — the index is fine, the conversation behind it simply is not here.
// Skip those and say so, rather than manufacturing dead rows.
func TestPullDesktopSkipsSessionsWithoutLocalTranscripts(t *testing.T) {
	ctx := context.Background()

	envA := setupTestEnv(t)
	envA.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_here.json": `{"sessionId":"local_here","cliSessionId":"present-one","isArchived":false}`,
		"local_gone.json": `{"sessionId":"local_gone","cliSessionId":"absent-one","isArchived":false}`,
	})
	if _, err := envA.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}

	// Machine B has the transcript for only one of the two sessions.
	stateB, err := LoadStateFromDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claudeB := t.TempDir()
	projB := filepath.Join(claudeB, "projects", "-Users-x-proj")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, "present-one.jsonl"), []byte(`{"type":"user"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	syncerB := NewSyncerWith(envA.syncer.cfg, envA.store, envA.syncer.encryptor, stateB, claudeB, true)
	indexB := makeIndex(t, nil)
	syncerB.desktopDir = indexB

	result, err := syncerB.PullDesktop(ctx)
	if err != nil {
		t.Fatalf("PullDesktop() error: %v", err)
	}
	if result.Written != 1 {
		t.Errorf("Written = %d, want 1 (only the session whose transcript is present)", result.Written)
	}
	if result.MissingTranscript != 1 {
		t.Errorf("MissingTranscript = %d, want 1", result.MissingTranscript)
	}

	labels, err := desktop.Scan(indexB)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := labels["absent-one"]; ok {
		t.Error("wrote a sidebar entry for a transcript this machine does not have")
	}
	if _, ok := labels["present-one"]; !ok {
		t.Error("failed to write the entry whose transcript is present")
	}
}

// failingStorage wraps the mock so a test can inject the kinds of failure a
// real bucket produces — an unreachable endpoint, a rejected credential — as
// distinct from the object simply not existing.
type failingStorage struct {
	*mockStorage
	listErr     error
	downloadErr error
}

func (f *failingStorage) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.mockStorage.List(ctx, prefix)
}

func (f *failingStorage) Download(ctx context.Context, key string) ([]byte, error) {
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	return f.mockStorage.Download(ctx, key)
}

// "Nothing pushed yet" is a specific, benign condition. A storage that cannot
// be reached, or that rejects the credentials, is neither — reporting it as
// "nothing pushed yet" sends the user to check the other machine when the
// problem is in front of them.
func TestPullDesktopDistinguishesStorageFailureFromAbsence(t *testing.T) {
	ctx := context.Background()

	t.Run("key genuinely absent is NoRemote, not an error", func(t *testing.T) {
		env := setupTestEnv(t)
		env.syncer.desktopDir = makeIndex(t, nil)
		res, err := env.syncer.PullDesktop(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.NoRemote {
			t.Error("NoRemote = false, want true when nothing has been pushed")
		}
	})

	t.Run("listing failure surfaces as an error", func(t *testing.T) {
		env := setupTestEnv(t)
		env.syncer.desktopDir = makeIndex(t, nil)
		env.syncer.storage = &failingStorage{mockStorage: env.store, listErr: errors.New("dial tcp: connection refused")}
		res, err := env.syncer.PullDesktop(ctx)
		if err == nil {
			t.Fatalf("got nil error and NoRemote=%v; want the storage failure surfaced", res.NoRemote)
		}
	})

	t.Run("download failure of an existing key surfaces as an error", func(t *testing.T) {
		env := setupTestEnv(t)
		env.syncer.desktopDir = makeIndex(t, map[string]string{
			"local_a.json": `{"sessionId":"local_a","cliSessionId":"aaa","isArchived":false}`,
		})
		if _, err := env.syncer.PushDesktop(ctx); err != nil {
			t.Fatal(err)
		}
		env.syncer.storage = &failingStorage{mockStorage: env.store, downloadErr: errors.New("403 Forbidden")}
		res, err := env.syncer.PullDesktop(ctx)
		if err == nil {
			t.Fatalf("got nil error and NoRemote=%v; want the download failure surfaced", res.NoRemote)
		}
	})
}

// A missing index directory is the ordinary "no desktop app here" case. Any
// other stat failure — a permission problem, a file where a directory should
// be — is a real error on a machine that may well have the app installed.
func TestPullDesktopOnlyTreatsNotExistAsNoIndex(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)

	// A path routed *through* a regular file stats with ENOTDIR, not ENOENT.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.syncer.desktopDir = filepath.Join(blocker, "acct", "ws")

	res, err := env.syncer.PullDesktop(ctx)
	if err == nil {
		t.Fatalf("got nil error and NoIndex=%v; want the stat failure surfaced", res.NoIndex)
	}
}
