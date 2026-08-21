package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/desktop"
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
	syncerB := NewSyncerWith(envA.syncer.cfg, envA.store, envA.syncer.encryptor, stateB, t.TempDir(), true)
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
