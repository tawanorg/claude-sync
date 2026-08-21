package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionIDFromPath(t *testing.T) {
	cases := []struct {
		path   string
		want   string
		wantOK bool
	}{
		{"projects/-Users-x-proj/abc-123.jsonl", "abc-123", true},
		{"projects/-Users-x-proj/nested/abc-123.jsonl", "", false},
		{"history.jsonl", "", false},
		{"projects/-Users-x-proj/notes.txt", "", false},
		{"tasks/abc.json", "", false},
	}
	for _, tc := range cases {
		got, ok := sessionIDFromPath(tc.path)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("sessionIDFromPath(%q) = (%q,%v), want (%q,%v)", tc.path, got, ok, tc.want, tc.wantOK)
		}
	}
}

// --skip-archived exists to keep finished conversations out of the upload. The
// archived transcript body must stay local while an active one still syncs.
func TestPushSkipArchivedExcludesArchivedTranscripts(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)

	projDir := filepath.Join(env.claudeDir, "projects", "-Users-x-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"archived-one", "active-one"} {
		if err := os.WriteFile(filepath.Join(projDir, id+".jsonl"), []byte(`{"type":"user"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	env.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_a.json": `{"sessionId":"local_a","cliSessionId":"archived-one","isArchived":true}`,
		"local_b.json": `{"sessionId":"local_b","cliSessionId":"active-one","isArchived":false}`,
	})
	env.syncer.skipArchived = true

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}

	uploaded := strings.Join(result.Uploaded, "\n")
	if strings.Contains(uploaded, "archived-one") {
		t.Errorf("archived transcript was uploaded despite --skip-archived:\n%s", uploaded)
	}
	if !strings.Contains(uploaded, "active-one") {
		t.Errorf("active transcript was not uploaded:\n%s", uploaded)
	}
}

// Without the flag, nothing changes: archived transcripts still sync.
func TestPushWithoutSkipArchivedUploadsEverything(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)

	projDir := filepath.Join(env.claudeDir, "projects", "-Users-x-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "archived-one.jsonl"), []byte(`{"type":"user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env.syncer.desktopDir = makeIndex(t, map[string]string{
		"local_a.json": `{"sessionId":"local_a","cliSessionId":"archived-one","isArchived":true}`,
	})

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}
	if !strings.Contains(strings.Join(result.Uploaded, "\n"), "archived-one") {
		t.Errorf("archived transcript was skipped without the flag: %v", result.Uploaded)
	}
}
