package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyPrefix(t *testing.T) {
	tests := []struct {
		name          string
		local, remote string
		want          PrefixRelation
	}{
		{"equal", "a\nb\n", "a\nb\n", PrefixEqual},
		{"remote ahead (truncated local)", "a\nb\n", "a\nb\nc\n", PrefixRemoteAhead},
		{"local ahead (stale remote)", "a\nb\nc\n", "a\nb\n", PrefixLocalAhead},
		{"diverged same length", "a\nb\nx\n", "a\nb\ny\n", PrefixNone},
		{"diverged, remote longer", "a\nx\n", "a\nb\nc\n", PrefixNone},
		{"diverged, local longer", "a\nx\ny\n", "a\nb\n", PrefixNone},
		{"diverged at byte 0", "x\n", "y\nz\n", PrefixNone},
		{"both empty", "", "", PrefixEqual},
		{"empty local, nonempty remote", "", "a\n", PrefixRemoteAhead},
		{"nonempty local, empty remote", "a\n", "", PrefixLocalAhead},
		// A live-session torn write: local ends mid-line, remote completed it.
		{"torn trailing line is still a prefix", `{"a":1}` + "\n" + `{"b`, `{"a":1}` + "\n" + `{"b":2}` + "\n", PrefixRemoteAhead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyPrefix([]byte(tt.local), []byte(tt.remote)); got != tt.want {
				t.Errorf("ClassifyPrefix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSessionJSONL(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"projects/p1/sess.jsonl", true},
		{"projects/p1/subagents/agent-abc.jsonl", true},
		{"projects/p1/history.jsonl", true}, // only the ROOT history.jsonl is excluded
		{"history.jsonl", false},
		{"tasks/x/3.json", false},
		{"CLAUDE.md", false},
		{"projects/p1/sess.jsonl.conflict.20260729-145427", false},
	}
	for _, tt := range tests {
		if got := isSessionJSONL(tt.path); got != tt.want {
			t.Errorf("isSessionJSONL(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// pushThenDesync seeds state with a real Push of v1, then rewrites the local
// file to localV and the bucket copy to remoteV with a LastModified in the
// future, reproducing the "both sides changed since state" trigger.
func pushThenDesync(t *testing.T, env *testEnv, name, v1, localV, remoteV string) {
	t.Helper()
	ctx := context.Background()
	writeFile(t, env.claudeDir, name, v1)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("seed push failed: %v", err)
	}
	writeFile(t, env.claudeDir, name, localV)

	compressed, err := gzipCompress([]byte(remoteV))
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	encrypted, err := env.syncer.encryptor.Encrypt(compressed)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	key := env.syncer.remoteKey(name)
	if err := env.store.Upload(ctx, key, encrypted); err != nil {
		t.Fatalf("upload: %v", err)
	}
	env.store.mu.Lock()
	obj := env.store.objects[key]
	obj.lastModified = time.Now().Add(2 * time.Second)
	env.store.objects[key] = obj
	env.store.mu.Unlock()
}

func conflictFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(filepath.Base(p), ".conflict.") {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func TestPullFastForwardsTruncatedLocal(t *testing.T) {
	env := setupTestEnv(t)
	mid := "{\"uuid\":\"u1\"}\n{\"uuid\":\"u2\"}\n"
	trunc := "{\"uuid\":\"u1\"}\n"
	full := "{\"uuid\":\"u1\"}\n{\"uuid\":\"u2\"}\n{\"uuid\":\"u3\"}\n"
	// Seed with mid so the truncated local differs from state, which is what
	// puts the file on the both-changed path at all.
	pushThenDesync(t, env, "projects/p1/sess.jsonl", mid, trunc, full)

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected no conflicts, got %v", result.Conflicts)
	}
	if got := readFile(t, env.claudeDir, "projects/p1/sess.jsonl"); got != full {
		t.Errorf("local not fast-forwarded:\ngot  %q\nwant %q", got, full)
	}
	if cf := conflictFilesIn(t, env.claudeDir); len(cf) != 0 {
		t.Errorf("a .conflict file was written for a fast-forward: %v", cf)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("fast-forward should count as downloaded, got %v", result.Downloaded)
	}
}

func TestPullKeepsLocalWhenAheadWithoutConflictFile(t *testing.T) {
	env := setupTestEnv(t)
	base := "{\"uuid\":\"u1\"}\n"
	ahead := base + "{\"uuid\":\"u2\"}\n"
	pushThenDesync(t, env, "projects/p1/sess.jsonl", base, ahead, base)

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected no conflicts, got %v", result.Conflicts)
	}
	if got := readFile(t, env.claudeDir, "projects/p1/sess.jsonl"); got != ahead {
		t.Errorf("local was modified, want untouched:\ngot %q", got)
	}
	if cf := conflictFilesIn(t, env.claudeDir); len(cf) != 0 {
		t.Errorf(".conflict written when local was simply ahead: %v", cf)
	}
}

func TestPullMergesDivergedSessionInsteadOfConflict(t *testing.T) {
	env := setupTestEnv(t)
	base := `{"uuid":"a","type":"user"}` + "\n"
	local := base + `{"uuid":"b","type":"assistant","parentUuid":"a"}` + "\n"
	remote := base + `{"uuid":"c","type":"assistant","parentUuid":"a"}` + "\n"
	pushThenDesync(t, env, "projects/p1/sess.jsonl", base, local, remote)

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("diverged session should merge, not conflict: %v", result.Conflicts)
	}
	got := readFile(t, env.claudeDir, "projects/p1/sess.jsonl")
	if !strings.HasPrefix(got, local) {
		t.Errorf("local content must be preserved verbatim as prefix, got %q", got)
	}
	if !strings.Contains(got, `"uuid":"c"`) {
		t.Errorf("remote-only event missing after merge, got %q", got)
	}
	if cf := conflictFilesIn(t, env.claudeDir); len(cf) != 0 {
		t.Errorf(".conflict written for a mergeable divergence: %v", cf)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("merge should count as downloaded, got %v", result.Downloaded)
	}

	// Applied idempotency at the pull level: a second pull must be a no-op
	// for this file (no new conflicts, no duplicate appends).
	before := readFile(t, env.claudeDir, "projects/p1/sess.jsonl")
	result2, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("second Pull failed: %v", err)
	}
	if len(result2.Conflicts) != 0 {
		t.Errorf("second pull must not conflict, got %v", result2.Conflicts)
	}
	if after := readFile(t, env.claudeDir, "projects/p1/sess.jsonl"); after != before {
		t.Errorf("second pull changed the file:\nbefore %q\nafter  %q", before, after)
	}
}

func TestPullNonJSONLConflictBehaviorUnchanged(t *testing.T) {
	env := setupTestEnv(t)
	pushThenDesync(t, env, "CLAUDE.md", "v1", "local-change", "remote-change")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %v", result.Conflicts)
	}
	if got := readFile(t, env.claudeDir, "CLAUDE.md"); got != "local-change" {
		t.Errorf("local must be kept on real conflict, got %q", got)
	}
	if cf := conflictFilesIn(t, env.claudeDir); len(cf) != 1 {
		t.Errorf("expected exactly one .conflict file, got %v", cf)
	}
}

func TestPushSkipsTruncatedJSONLWhenRemoteIsFuller(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	full := "{\"uuid\":\"u1\"}\n{\"uuid\":\"u2\"}\n{\"uuid\":\"u3\"}\n"
	trunc := "{\"uuid\":\"u1\"}\n"

	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", full)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("seed push failed: %v", err)
	}
	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", trunc)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	remote, err := env.syncer.fetchDecoded(ctx, "projects/p1/sess.jsonl",
		env.syncer.remoteKey("projects/p1/sess.jsonl"))
	if err != nil {
		t.Fatalf("fetch remote: %v", err)
	}
	if string(remote) != full {
		t.Errorf("truncated local clobbered fuller remote:\ngot  %q\nwant %q", string(remote), full)
	}
}

func TestPushUploadsGenuinelyRewrittenSmallerJSONL(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	v1 := "{\"uuid\":\"u1\"}\n{\"uuid\":\"u2\"}\n"
	rewritten := "{\"uuid\":\"u9\"}\n" // smaller but NOT a prefix: a legitimate rewrite

	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", v1)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("seed push failed: %v", err)
	}
	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", rewritten)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	remote, err := env.syncer.fetchDecoded(ctx, "projects/p1/sess.jsonl",
		env.syncer.remoteKey("projects/p1/sess.jsonl"))
	if err != nil {
		t.Fatalf("fetch remote: %v", err)
	}
	if string(remote) != rewritten {
		t.Errorf("legitimate rewrite was blocked:\ngot  %q\nwant %q", string(remote), rewritten)
	}
}

func TestPushSkipDoesNotCountAsUpload(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	full := "{\"uuid\":\"u1\"}\n{\"uuid\":\"u2\"}\n"
	trunc := "{\"uuid\":\"u1\"}\n"

	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", full)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("seed push failed: %v", err)
	}
	writeFile(t, env.claudeDir, "projects/p1/sess.jsonl", trunc)
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("skip must not count as upload, got %v", result.Uploaded)
	}
	if len(result.Errors) != 0 {
		t.Errorf("skip must not count as error, got %v", result.Errors)
	}
}
