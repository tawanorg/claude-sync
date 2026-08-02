package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
)

func TestIsExcludedSkipsPerMachineDebris(t *testing.T) {
	s := &Syncer{cfg: &config.Config{}}

	excluded := []string{
		"tasks/6bf39315-3333-4394-86bb-2d57f8759b5a/.lock",
		".lock",
		"projects/p1/sess.jsonl.conflict.20260729-145427",
		"tasks/x/3.json.conflict.20260101-000000",
	}
	for _, p := range excluded {
		if !s.isExcluded(p) {
			t.Errorf("expected %q to be excluded as per-machine debris", p)
		}
	}

	// Real data must never be caught. The .conflict rule in particular is
	// anchored on the exact timestamp format this tool writes, because
	// excluding an already-tracked file prunes it from the bucket on the
	// next push — a loose pattern would delete user data remotely.
	kept := []string{
		"projects/p1/sess.jsonl",
		"tasks/x/3.json",
		"CLAUDE.md",
		"projects/notes.conflict.md",              // ".conflict." but not our artifact
		"projects/p1/my.conflict.2026.txt",        // wrong timestamp shape
		"projects/p1/sess.jsonl.conflict.2026072", // truncated timestamp
		"projects/lockfiles/package.lock",         // ends in .lock but is not a bare .lock
		"projects/p1/.lockfile",
	}
	for _, p := range kept {
		if s.isExcluded(p) {
			t.Errorf("expected %q NOT to be excluded", p)
		}
	}
}

func TestIsExcludedStillHonorsUserPatterns(t *testing.T) {
	s := &Syncer{cfg: &config.Config{Exclude: []string{"**/*.tmp"}}}
	if !s.isExcluded("projects/p1/scratch.tmp") {
		t.Error("user exclude patterns must still apply")
	}
	if s.isExcluded("projects/p1/keep.txt") {
		t.Error("unrelated file must not be excluded")
	}
}

func TestPushSkipsDebrisFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", "{\"uuid\":\"u1\"}\n")
	writeFile(t, env.claudeDir, "tasks/t1/.lock", "")
	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl.conflict.20260729-145427", "stale remote copy\n")

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if len(result.Uploaded) != 1 || result.Uploaded[0] != "projects/p1/sess.jsonl" {
		t.Errorf("only the transcript should upload, got %v", result.Uploaded)
	}

	objs, err := env.store.ListUserObjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("expected 1 remote object, got %d: %v", len(objs), objs)
	}

	// The debris stays on disk; it is simply never published.
	if _, err := os.Stat(filepath.Join(env.claudeDir, "tasks", "t1", ".lock")); err != nil {
		t.Errorf("local .lock must not be removed: %v", err)
	}
}
