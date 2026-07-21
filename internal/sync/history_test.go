package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// extractDisplay is the core of history reconstruction; these cases are derived
// from the real formats in a live ~/.claude (history.jsonl + session files).
func TestExtractDisplay(t *testing.T) {
	tests := []struct {
		name     string
		content  string // raw JSON for message.content (string or block array)
		want     string
		wantKeep bool
	}{
		{
			name:     "plain string prompt",
			content:  `"fix the login bug"`,
			want:     "fix the login bug",
			wantKeep: true,
		},
		{
			name:     "string prompt trimmed",
			content:  `"  hello world  "`,
			want:     "hello world",
			wantKeep: true,
		},
		{
			name:     "text blocks joined",
			content:  `[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]`,
			want:     "line one\nline two",
			wantKeep: true,
		},
		{
			name:     "tool_result block is skipped",
			content:  `[{"type":"tool_result","content":"exit 0"}]`,
			want:     "",
			wantKeep: false,
		},
		{
			// The reference implementation returns "/clear" here; the real
			// history.jsonl stores "/clear " (trailing space). Must match or
			// dedup against existing entries fails and /resume shows dupes.
			name:     "slash command with empty args keeps trailing space",
			content:  `"<command-message>clear</command-message>\n<command-name>/clear</command-name>\n<command-args></command-args>"`,
			want:     "/clear ",
			wantKeep: true,
		},
		{
			name:     "slash command with args",
			content:  `"<command-name>/compact</command-name>\n<command-args>mock data</command-args>"`,
			want:     "/compact mock data",
			wantKeep: true,
		},
		{
			name:     "slash command inside text blocks",
			content:  `[{"type":"text","text":"<command-name>/init</command-name><command-args></command-args>"}]`,
			want:     "/init ",
			wantKeep: true,
		},
		{
			name:     "system-reminder is harness-injected, skipped",
			content:  `"<system-reminder>do this</system-reminder>"`,
			want:     "",
			wantKeep: false,
		},
		{
			name:     "local-command-stdout skipped",
			content:  `"<local-command-stdout>output</local-command-stdout>"`,
			want:     "",
			wantKeep: false,
		},
		{
			name:     "caveat preamble skipped",
			content:  `"Caveat: The messages below were generated"`,
			want:     "",
			wantKeep: false,
		},
		{
			name:     "unknown angle-bracket content skipped",
			content:  `"<some-tag>x</some-tag>"`,
			want:     "",
			wantKeep: false,
		},
		{
			name:     "empty string skipped",
			content:  `""`,
			want:     "",
			wantKeep: false,
		},
		{
			// A user typing an absolute path is NOT a slash command; kept verbatim.
			name:     "absolute path is not a command",
			content:  `"/Users/me/project/file.go"`,
			want:     "/Users/me/project/file.go",
			wantKeep: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keep := extractDisplay(json.RawMessage(tt.content))
			if keep != tt.wantKeep {
				t.Fatalf("keep = %v, want %v (got display %q)", keep, tt.wantKeep, got)
			}
			if keep && got != tt.want {
				t.Errorf("display = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEntriesFromSessionFile(t *testing.T) {
	dir := t.TempDir()
	sessionID := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(dir, sessionID+".jsonl")

	lines := []string{
		// real user prompt
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:04:21.914Z","cwd":"/home/me/proj","message":{"content":"first prompt"}}`,
		// tool result (content is a block) -> skipped
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:04:30.000Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}`,
		// sidechain -> skipped
		`{"type":"user","isSidechain":true,"sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:04:40.000Z","message":{"content":"sidechain prompt"}}`,
		// meta -> skipped
		`{"type":"user","isMeta":true,"sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:04:50.000Z","message":{"content":"meta"}}`,
		// assistant -> skipped
		`{"type":"assistant","sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:05:00.000Z","message":{"content":"answer"}}`,
		// second real prompt
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-07-20T02:05:10.000Z","cwd":"/home/me/proj","message":{"content":"second prompt"}}`,
	}
	writeLines(t, path, lines)

	entries, err := entriesFromSessionFile(path)
	if err != nil {
		t.Fatalf("entriesFromSessionFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (only real user prompts)", len(entries))
	}
	if entries[0].Display != "first prompt" || entries[1].Display != "second prompt" {
		t.Errorf("displays = %q,%q", entries[0].Display, entries[1].Display)
	}
	if entries[0].SessionID != sessionID {
		t.Errorf("sessionID = %q, want %q", entries[0].SessionID, sessionID)
	}
	if entries[0].Project != "/home/me/proj" {
		t.Errorf("project = %q, want /home/me/proj", entries[0].Project)
	}
	// RFC3339 2026-07-20T02:04:21.914Z -> UnixMilli
	if entries[0].Timestamp == 0 {
		t.Error("timestamp not parsed to unix millis")
	}
}

func TestEntriesFromSessionFile_SessionIDFallsBackToFilename(t *testing.T) {
	dir := t.TempDir()
	name := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	path := filepath.Join(dir, name+".jsonl")
	writeLines(t, path, []string{
		`{"type":"user","timestamp":"2026-07-20T02:04:21.914Z","message":{"content":"no session id line"}}`,
	})
	entries, err := entriesFromSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SessionID != name {
		t.Fatalf("expected sessionID fallback to filename %q, got %+v", name, entries)
	}
}

func TestRebuildHistory_MergesPreservesSortsBacksUp(t *testing.T) {
	claudeDir := t.TempDir()
	projects := filepath.Join(claudeDir, "projects", "-home-me-proj")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}

	// Existing history: one entry with a custom field that must survive verbatim.
	// Its timestamp sits ~1s before the matching session line (as it would in
	// reality: same submit time), so the reconstruction dedupes against it.
	existingLine := `{"display":"kept prompt","pastedContents":{"a":1},"timestamp":1784505600000,"project":"/home/me/proj","sessionId":"S1","customField":"preserve-me"}`
	writeLines(t, filepath.Join(claudeDir, HistoryFile), []string{existingLine})

	// Session file recovers a NEW prompt (ts later) plus a duplicate of the
	// existing one (same session+display, near timestamp) that must NOT dupe.
	sess := filepath.Join(projects, "S1.jsonl")
	writeLines(t, sess, []string{
		`{"type":"user","sessionId":"S1","timestamp":"2026-07-20T00:00:01.000Z","cwd":"/home/me/proj","message":{"content":"kept prompt"}}`,
		`{"type":"user","sessionId":"S1","timestamp":"2026-07-20T00:00:05.000Z","cwd":"/home/me/proj","message":{"content":"recovered prompt"}}`,
	})

	res, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatalf("RebuildHistory: %v", err)
	}

	entries := readHistoryRaw(t, claudeDir)
	// Expect exactly 2: the preserved existing + the recovered new one.
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), entries)
	}
	// Sorted by timestamp ascending: existing(ts=1000) first.
	if entries[0]["display"] != "kept prompt" || entries[1]["display"] != "recovered prompt" {
		t.Errorf("unexpected order/content: %v", entries)
	}
	// Existing entry preserved verbatim (custom field survives).
	if entries[0]["customField"] != "preserve-me" {
		t.Errorf("existing entry not preserved verbatim: %v", entries[0])
	}
	if res.Existing != 1 || res.Merged != 2 {
		t.Errorf("result = %+v, want Existing=1 Merged=2", res)
	}

	// Backup written with previous content.
	bak, err := os.ReadFile(filepath.Join(claudeDir, HistoryFile+".bak"))
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(bak) != existingLine+"\n" && string(bak) != existingLine {
		t.Errorf("backup content mismatch: %q", string(bak))
	}

	// Perms 0600 on the rewritten file (skip check on Windows).
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(claudeDir, HistoryFile))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("history perms = %o, want 600", perm)
		}
	}
}

// A prompt already in history and its reconstruction must dedupe even when their
// timestamps straddle a 2-minute boundary — the failure mode of bucket dedup.
func TestRebuildHistory_DedupStraddlesTimeBoundary(t *testing.T) {
	claudeDir := t.TempDir()
	projects := filepath.Join(claudeDir, "projects", "-p")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	// 2026-07-20T00:01:59Z vs 00:02:01Z — 2s apart, opposite 120s buckets.
	writeLines(t, filepath.Join(claudeDir, HistoryFile), []string{
		`{"display":"same prompt","pastedContents":{},"timestamp":1784505719000,"project":"/p","sessionId":"S1"}`,
	})
	writeLines(t, filepath.Join(projects, "S1.jsonl"), []string{
		`{"type":"user","sessionId":"S1","timestamp":"2026-07-20T00:02:01.000Z","cwd":"/p","message":{"content":"same prompt"}}`,
	})

	if _, err := RebuildHistory(claudeDir); err != nil {
		t.Fatal(err)
	}
	entries := readHistoryRaw(t, claudeDir)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (dedup across time boundary): %v", len(entries), entries)
	}
}

// RebuildHistory must never collapse two distinct existing lines, even when they
// share session + display and sit close in time.
func TestRebuildHistory_PreservesDuplicateExistingLines(t *testing.T) {
	claudeDir := t.TempDir()
	writeLines(t, filepath.Join(claudeDir, HistoryFile), []string{
		`{"display":"y","pastedContents":{},"timestamp":1784505600000,"project":"/p","sessionId":"S1"}`,
		`{"display":"y","pastedContents":{},"timestamp":1784505610000,"project":"/p","sessionId":"S1"}`,
	})
	res, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	entries := readHistoryRaw(t, claudeDir)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (both existing lines preserved): %v", len(entries), entries)
	}
	if res.Existing != 2 || res.Merged != 2 {
		t.Errorf("result = %+v, want Existing=2 Merged=2", res)
	}
}

// Empty-display entries (blank submissions) exist in real history.jsonl and
// must be preserved, not silently dropped, by a rebuild.
func TestRebuildHistory_PreservesEmptyDisplayEntries(t *testing.T) {
	claudeDir := t.TempDir()
	empty := `{"display":"","pastedContents":{},"timestamp":1784505600000,"project":"/p","sessionId":"S1"}`
	real := `{"display":"real","pastedContents":{},"timestamp":1784505601000,"project":"/p","sessionId":"S1"}`
	writeLines(t, filepath.Join(claudeDir, HistoryFile), []string{empty, real})

	res, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Existing != 2 {
		t.Fatalf("Existing = %d, want 2 (empty-display row counted)", res.Existing)
	}
	entries := readHistoryRaw(t, claudeDir)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (empty-display row preserved): %v", len(entries), entries)
	}
}

func TestRebuildHistory_GracefulWhenNothingToDo(t *testing.T) {
	claudeDir := t.TempDir() // no history.jsonl, no projects/
	res, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatalf("expected graceful no-op, got error: %v", err)
	}
	if res.Existing != 0 || res.Reconstructed != 0 || res.Merged != 0 {
		t.Errorf("result = %+v, want all zero", res)
	}
	// No history file should be created when there is nothing to write.
	if _, err := os.Stat(filepath.Join(claudeDir, HistoryFile)); !os.IsNotExist(err) {
		t.Error("history.jsonl should not be created when empty")
	}
}

// --- helpers ---

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readHistoryRaw(t *testing.T, claudeDir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeDir, HistoryFile))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var out []map[string]any
	for _, line := range splitNonEmptyLines(data) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("invalid history line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func splitNonEmptyLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	return out
}
