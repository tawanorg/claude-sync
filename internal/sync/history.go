package sync

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// HistoryFile is the prompt-history file inside the Claude directory.
const HistoryFile = "history.jsonl"

// history.jsonl is synced as a single opaque file, so concurrent pushes from
// two devices are last-writer-wins and one device's entries get lost. Session
// files under projects/ sync cleanly (one uniquely-named file per session),
// so the prompt history can always be reconstructed from them.

// HistoryEntry mirrors one line of ~/.claude/history.jsonl.
type HistoryEntry struct {
	Display        string          `json:"display"`
	PastedContents json.RawMessage `json:"pastedContents"`
	Timestamp      int64           `json:"timestamp"`
	Project        string          `json:"project"`
	SessionID      string          `json:"sessionId"`
}

// RebuildHistoryResult reports what RebuildHistory did.
type RebuildHistoryResult struct {
	Existing      int // valid entries already in history.jsonl
	Reconstructed int // entries recovered from session files
	Merged        int // entries written after dedup
}

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// sessionLine is the subset of a session-file line needed for reconstruction.
type sessionLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	Cwd         string `json:"cwd"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// mergedEntry keeps the original raw line for existing entries so unknown
// fields (e.g. pastedContents payloads) survive the rewrite.
type mergedEntry struct {
	raw       []byte
	timestamp int64
}

// RebuildHistory reconstructs history.jsonl by merging its current entries
// with user prompts extracted from session files under projects/. Existing
// entries win on conflict (they preserve exact text and pasted contents).
// The previous file is kept as history.jsonl.bak and the new file is written
// atomically.
func RebuildHistory(claudeDir string) (*RebuildHistoryResult, error) {
	historyPath := filepath.Join(claudeDir, HistoryFile)
	result := &RebuildHistoryResult{}
	merged := make(map[string]mergedEntry)

	reconstructed, err := reconstructHistoryEntries(filepath.Join(claudeDir, "projects"))
	if err != nil {
		return nil, err
	}
	result.Reconstructed = len(reconstructed)
	for _, e := range reconstructed {
		key := historyDedupeKey(e.SessionID, e.Display, e.Timestamp)
		if _, ok := merged[key]; !ok {
			raw, err := json.Marshal(e)
			if err != nil {
				return nil, fmt.Errorf("marshalling history entry: %w", err)
			}
			merged[key] = mergedEntry{raw: raw, timestamp: e.Timestamp}
		}
	}

	existing, err := loadHistoryLines(historyPath)
	if err != nil {
		return nil, err
	}
	result.Existing = len(existing)
	for _, e := range existing {
		key := historyDedupeKey(e.entry.SessionID, e.entry.Display, e.entry.Timestamp)
		merged[key] = mergedEntry{raw: e.raw, timestamp: e.entry.Timestamp}
	}

	entries := make([]mergedEntry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].timestamp < entries[j].timestamp
	})
	result.Merged = len(entries)

	if err := writeHistoryFile(historyPath, entries); err != nil {
		return nil, err
	}
	return result, nil
}

// historyDedupeKey treats the same prompt in the same session within a
// 2-minute bucket as one entry: reconstructed timestamps come from session
// files and can differ slightly from the original submit time.
func historyDedupeKey(sessionID, display string, timestamp int64) string {
	return fmt.Sprintf("%s\x00%s\x00%d", sessionID, display, timestamp/120000)
}

type existingLine struct {
	raw   []byte
	entry HistoryEntry
}

func loadHistoryLines(historyPath string) ([]existingLine, error) {
	f, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open %s: %w", historyPath, err)
	}
	defer f.Close()

	var lines []existingLine
	err = forEachLine(f, func(line []byte) {
		var entry HistoryEntry
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if entry.Display == "" || entry.Timestamp == 0 {
			return
		}
		raw := make([]byte, len(line))
		copy(raw, line)
		lines = append(lines, existingLine{raw: raw, entry: entry})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", historyPath, err)
	}
	return lines, nil
}

func reconstructHistoryEntries(projectsDir string) ([]HistoryEntry, error) {
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", projectsDir, err)
	}

	var entries []HistoryEntry
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(projectsDir, project.Name())
		sessions, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if session.IsDir() || !strings.HasSuffix(session.Name(), ".jsonl") {
				continue
			}
			sessionPath := filepath.Join(projectDir, session.Name())
			fileEntries, err := entriesFromSessionFile(sessionPath)
			if err != nil {
				continue // an unreadable session file shouldn't abort the rebuild
			}
			entries = append(entries, fileEntries...)
		}
	}
	return entries, nil
}

func entriesFromSessionFile(path string) ([]HistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fallbackSessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	var entries []HistoryEntry
	err = forEachLine(f, func(line []byte) {
		var sl sessionLine
		if json.Unmarshal(line, &sl) != nil {
			return
		}
		if sl.Type != "user" || sl.IsMeta || sl.IsSidechain {
			return
		}
		display, ok := displayFromContent(sl.Message.Content)
		if !ok {
			return
		}
		ts, err := time.Parse(time.RFC3339Nano, sl.Timestamp)
		if err != nil {
			return
		}
		sessionID := sl.SessionID
		if sessionID == "" {
			sessionID = fallbackSessionID
		}
		entries = append(entries, HistoryEntry{
			Display:        display,
			PastedContents: json.RawMessage("{}"),
			Timestamp:      ts.UnixMilli(),
			Project:        sl.Cwd,
			SessionID:      sessionID,
		})
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// displayFromContent extracts what the user typed from a user message's
// content, which is either a plain string or a list of content blocks.
// Returns false for tool results and harness-injected content.
func displayFromContent(content json.RawMessage) (string, bool) {
	var text string
	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		text = asString
	} else {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(content, &blocks); err != nil {
			return "", false
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return "", false
			}
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	for _, prefix := range []string{"<local-command-stdout>", "<system-reminder>", "Caveat:"} {
		if strings.HasPrefix(text, prefix) {
			return "", false
		}
	}
	// Slash commands are stored as <command-name>/foo</command-name><command-args>...
	if m := commandNameRe.FindStringSubmatch(text); m != nil {
		cmd := strings.TrimSpace(m[1])
		if cmd == "" {
			return "", false
		}
		if args := commandArgsRe.FindStringSubmatch(text); args != nil {
			if a := strings.TrimSpace(args[1]); a != "" {
				return cmd + " " + a, true
			}
		}
		return cmd, true
	}
	if strings.HasPrefix(text, "<") {
		return "", false
	}
	return text, true
}

func writeHistoryFile(historyPath string, entries []mergedEntry) error {
	dir := filepath.Dir(historyPath)

	if data, err := os.ReadFile(historyPath); err == nil {
		if err := os.WriteFile(historyPath+".bak", data, 0600); err != nil {
			return fmt.Errorf("failed to back up %s: %w", historyPath, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".history-*.jsonl")
	if err != nil {
		return fmt.Errorf("creating temp history file: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name()) // no-op after successful rename
	}()

	w := bufio.NewWriter(tmp)
	for _, e := range entries {
		if _, err := w.Write(e.raw); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), historyPath); err != nil {
		return fmt.Errorf("replacing %s: %w", historyPath, err)
	}
	return nil
}

// forEachLine calls fn for every non-empty line. It reads unbounded line
// lengths (session-file lines with tool results can be many MB).
func forEachLine(r io.Reader, fn func(line []byte)) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			fn([]byte(trimmed))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
