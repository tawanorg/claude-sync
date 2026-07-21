package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHistoryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func readHistory(t *testing.T, claudeDir string) []HistoryEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeDir, HistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	var entries []HistoryEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e HistoryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid history line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

const sessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func sessionFixture() string {
	lines := []string{
		// Plain user prompt — should be recovered.
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:00:00.500Z","cwd":"/home/u/proj","message":{"role":"user","content":"fix the login bug"}}`,
		// Tool result — must be skipped.
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:00:05Z","cwd":"/home/u/proj","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":"ok"}]}}`,
		// Assistant line — skipped.
		`{"type":"assistant","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:00:06Z","cwd":"/home/u/proj","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		// Meta and sidechain lines — skipped.
		`{"type":"user","isMeta":true,"sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:00:07Z","cwd":"/home/u/proj","message":{"role":"user","content":"meta"}}`,
		`{"type":"user","isSidechain":true,"sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:00:08Z","cwd":"/home/u/proj","message":{"role":"user","content":"sidechain"}}`,
		// Slash command — display reconstructed as "/context extra".
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:01:00Z","cwd":"/home/u/proj","message":{"role":"user","content":"<command-name>/context</command-name><command-args>extra</command-args>"}}`,
		// Harness-injected content — skipped.
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:02:00Z","cwd":"/home/u/proj","message":{"role":"user","content":"<local-command-stdout>out</local-command-stdout>"}}`,
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:02:01Z","cwd":"/home/u/proj","message":{"role":"user","content":"Caveat: injected"}}`,
		// Content-block form — recovered.
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-01-02T10:03:00Z","cwd":"/home/u/proj","message":{"role":"user","content":[{"type":"text","text":"second prompt"}]}}`,
		// Malformed line — ignored without aborting.
		`{not json`,
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestRebuildHistoryFromSessions(t *testing.T) {
	claudeDir := t.TempDir()
	writeHistoryFixture(t, filepath.Join(claudeDir, "projects", "-home-u-proj", sessionID+".jsonl"), sessionFixture())

	result, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Existing != 0 || result.Reconstructed != 3 || result.Merged != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}

	entries := readHistory(t, claudeDir)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}
	wantDisplays := []string{"fix the login bug", "/context extra", "second prompt"}
	for i, want := range wantDisplays {
		if entries[i].Display != want {
			t.Errorf("entry %d display = %q, want %q", i, entries[i].Display, want)
		}
		if entries[i].SessionID != sessionID {
			t.Errorf("entry %d sessionId = %q", i, entries[i].SessionID)
		}
		if entries[i].Project != "/home/u/proj" {
			t.Errorf("entry %d project = %q", i, entries[i].Project)
		}
	}
	if entries[0].Timestamp != 1767348000500 {
		t.Errorf("timestamp = %d, want 1767348000500", entries[0].Timestamp)
	}
}

func TestRebuildHistoryMergesWithExisting(t *testing.T) {
	claudeDir := t.TempDir()
	writeHistoryFixture(t, filepath.Join(claudeDir, "projects", "-home-u-proj", sessionID+".jsonl"), sessionFixture())

	// Existing history: one entry that overlaps the session file (submitted a
	// few hundred ms before the session-file timestamp, with pastedContents
	// that must survive), one unique entry, and one junk line.
	existing := strings.Join([]string{
		`{"display":"fix the login bug","pastedContents":{"1":{"content":"paste"}},"timestamp":1767348000100,"project":"/home/u/proj","sessionId":"` + sessionID + `"}`,
		`{"display":"only in history","pastedContents":{},"timestamp":1767348100000,"project":"/home/u/other","sessionId":"other-session"}`,
		`garbage line`,
	}, "\n") + "\n"
	writeHistoryFixture(t, filepath.Join(claudeDir, HistoryFile), existing)

	result, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Existing != 2 {
		t.Errorf("Existing = %d, want 2", result.Existing)
	}
	// 3 reconstructed, but "fix the login bug" dedupes against the existing
	// entry (same session, same display, same 2-minute bucket).
	if result.Merged != 4 {
		t.Fatalf("Merged = %d, want 4 (result: %+v)", result.Merged, result)
	}

	entries := readHistory(t, claudeDir)
	byDisplay := map[string]HistoryEntry{}
	for _, e := range entries {
		byDisplay[e.Display] = e
	}
	// The existing entry wins: original timestamp and pastedContents kept.
	kept := byDisplay["fix the login bug"]
	if kept.Timestamp != 1767348000100 {
		t.Errorf("deduped entry timestamp = %d, want existing 1767348000100", kept.Timestamp)
	}
	if !strings.Contains(string(kept.PastedContents), "paste") {
		t.Errorf("pastedContents lost: %s", kept.PastedContents)
	}
	if _, ok := byDisplay["only in history"]; !ok {
		t.Error("entry unique to history.jsonl was dropped")
	}

	// Sorted by timestamp.
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp < entries[i-1].Timestamp {
			t.Errorf("entries not sorted at index %d", i)
		}
	}

	// Backup of the previous file exists.
	bak, err := os.ReadFile(filepath.Join(claudeDir, HistoryFile+".bak"))
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(bak) != existing {
		t.Error("backup does not match previous history content")
	}
}

func TestRebuildHistoryNoSourcesIsEmpty(t *testing.T) {
	claudeDir := t.TempDir()
	result, err := RebuildHistory(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Existing != 0 || result.Reconstructed != 0 || result.Merged != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(claudeDir, HistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty history file, got %q", data)
	}
}

func TestDisplayFromContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{"plain string", `"hello world"`, "hello world", true},
		{"whitespace only", `"   "`, "", false},
		{"tool result", `[{"type":"tool_result","content":"x"}]`, "", false},
		{"text blocks", `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, "a\nb", true},
		{"command no args", `"<command-name>/clear</command-name><command-args></command-args>"`, "/clear", true},
		{"command with args", `"<command-name>/model</command-name><command-args>opus</command-args>"`, "/model opus", true},
		{"system reminder", `"<system-reminder>x</system-reminder>"`, "", false},
		{"unknown xml", `"<something>x</something>"`, "", false},
		{"invalid json", `12345`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := displayFromContent(json.RawMessage(tc.content))
			if ok != tc.ok || got != tc.want {
				t.Errorf("displayFromContent(%s) = (%q, %v), want (%q, %v)", tc.content, got, ok, tc.want, tc.ok)
			}
		})
	}
}
