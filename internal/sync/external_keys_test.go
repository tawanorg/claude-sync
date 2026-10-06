package sync

import (
	"context"
	"strings"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
)

// PushDesktop and PushMCP record their remote objects in state.Files so an
// unchanged payload is not re-uploaded. Those keys never correspond to a file
// under ~/.claude, so DetectChanges must not read their absence from the local
// tree as a deletion. Otherwise every push deletes the object and re-uploads
// it — and if the desktop app is absent when push runs, nothing re-uploads it
// and the remote index is simply gone.
func TestPushDoesNotDeleteExternalIndexOnSubsequentPush(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)
	env.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_one.json": `{"sessionId":"local_one","cliSessionId":"aaa","isArchived":false}`,
	})

	if _, err := env.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	key := config.DesktopRemoteKey + ".age"
	if _, err := env.store.Download(ctx, key); err != nil {
		t.Fatalf("precondition: desktop index not on remote after PushDesktop: %v", err)
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}
	for _, d := range result.Deleted {
		if strings.HasPrefix(d, "_external/") {
			t.Errorf("Push scheduled a delete of external key %q", d)
		}
	}
	if _, err := env.store.Download(ctx, key); err != nil {
		t.Errorf("desktop index was removed from the remote by an ordinary push: %v", err)
	}
	if env.syncer.state.GetFile(config.DesktopRemoteKey) == nil {
		t.Error("desktop index entry was dropped from sync state by an ordinary push")
	}
}

// Same invariant at the unit level, and it covers the pre-existing MCP key too.
func TestDetectChangesIgnoresExternalKeys(t *testing.T) {
	env := setupTestEnv(t)
	env.syncer.state.mu.Lock()
	env.syncer.state.Files[config.DesktopRemoteKey] = &FileState{Path: config.DesktopRemoteKey, Hash: "x"}
	env.syncer.state.Files[config.MCPRemoteKey] = &FileState{Path: config.MCPRemoteKey, Hash: "y"}
	env.syncer.state.mu.Unlock()

	changes, err := env.syncer.state.DetectChanges(env.claudeDir, env.syncer.syncPaths(), env.syncer.isExcluded)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if strings.HasPrefix(c.Path, "_external/") {
			t.Errorf("DetectChanges reported %s for external key %q", c.Action, c.Path)
		}
	}
}
