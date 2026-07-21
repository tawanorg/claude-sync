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
// two devices are last-writer-wins and one device's prompt-history entries get
// lost — which breaks the /resume session picker. Session files under
// projects/ sync cleanly (one uniquely-named file per session), so the prompt
// history can always be reconstructed from them and merged back.

// maxHistoryLineBytes bounds how much of a single JSONL line is buffered.
// Typed prompts are small; session files also contain multi-megabyte tool
// results on their own lines, which we skip — so a line larger than this is
// never a prompt we care about and is drained without being buffered.
const maxHistoryLineBytes = 8 * 1024 * 1024

// historyDedupeWindowMs is how close in time a reconstructed prompt must be to
// an existing one (same session + display) to count as the same submission. A
// prompt already in history.jsonl and its reconstruction from the session file
// share a submit time to within a second; the generous window absorbs that
// jitter without a bucket boundary that could split two near-identical times,
// while still keeping genuinely separate re-submissions of the same text.
const historyDedupeWindowMs = 5 * 60 * 1000

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

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

// RebuildHistory reconstructs history.jsonl by merging its current entries with
// user prompts extracted from session files under projects/. Existing entries
// win on conflict — they preserve the exact display text and pastedContents
// payloads. The previous file is backed up to history.jsonl.bak and the new
// file is written atomically. When there is nothing to write (no history and no
// recoverable prompts) the file is left untouched.
func RebuildHistory(claudeDir string) (*RebuildHistoryResult, error) {
	historyPath := filepath.Join(claudeDir, HistoryFile)
	result := &RebuildHistoryResult{}

	existing, err := loadHistoryLines(historyPath)
	if err != nil {
		return nil, err
	}
	result.Existing = len(existing)

	reconstructed, err := reconstructHistoryEntries(filepath.Join(claudeDir, "projects"))
	if err != nil {
		return nil, err
	}
	result.Reconstructed = len(reconstructed)

	// Every existing line is kept verbatim (preserving exact text, pasted
	// contents, and unknown fields). Reconstructed entries are added only when
	// they don't match an already-recorded prompt — matched by session +
	// display within historyDedupeWindowMs — so recovered prompts fill the gaps
	// without duplicating or dropping anything already present.
	type promptSig struct{ session, display string }
	seen := make(map[promptSig][]int64)
	out := make([]mergedEntry, 0, len(existing)+len(reconstructed))

	for _, e := range existing {
		// Only index real prompts for dedup; empty-display rows carry no prompt
		// to match against and are just preserved.
		if e.entry.Display != "" {
			sig := promptSig{e.entry.SessionID, e.entry.Display}
			seen[sig] = append(seen[sig], e.entry.Timestamp)
		}
		out = append(out, mergedEntry{raw: e.raw, timestamp: e.entry.Timestamp})
	}
	for _, e := range reconstructed {
		sig := promptSig{e.SessionID, e.Display}
		if withinWindow(seen[sig], e.Timestamp) {
			continue
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("marshalling reconstructed history entry: %w", err)
		}
		seen[sig] = append(seen[sig], e.Timestamp)
		out = append(out, mergedEntry{raw: raw, timestamp: e.Timestamp})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].timestamp < out[j].timestamp
	})
	result.Merged = len(out)

	if len(out) == 0 {
		return result, nil
	}
	if err := writeHistoryFile(historyPath, out); err != nil {
		return nil, err
	}
	return result, nil
}

// mergedEntry keeps the original raw line for existing entries so unknown
// fields (e.g. pastedContents payloads) survive the rewrite untouched.
type mergedEntry struct {
	raw       []byte
	timestamp int64
}

// withinWindow reports whether any timestamp in times is within
// historyDedupeWindowMs of ts.
func withinWindow(times []int64, ts int64) bool {
	for _, t := range times {
		d := t - ts
		if d < 0 {
			d = -d
		}
		if d <= historyDedupeWindowMs {
			return true
		}
	}
	return false
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
	defer func() { _ = f.Close() }()

	var lines []existingLine
	err = forEachLine(f, func(line []byte) {
		// Every parseable line is preserved verbatim, including empty-display
		// entries (blank submissions) — a rebuild must not drop existing data.
		// Only genuinely unparseable JSON is skipped.
		var entry HistoryEntry
		if json.Unmarshal(line, &entry) != nil {
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
			fileEntries, err := entriesFromSessionFile(filepath.Join(projectDir, session.Name()))
			if err != nil {
				continue // one unreadable session file must not abort the rebuild
			}
			entries = append(entries, fileEntries...)
		}
	}
	return entries, nil
}

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

func entriesFromSessionFile(path string) ([]HistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

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
		display, ok := extractDisplay(sl.Message.Content)
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

// extractDisplay derives what the user typed from a user message's content
// (either a plain string or a list of content blocks). It returns false for
// tool results and harness-injected content.
//
// Slash commands are stored in the session as
// <command-name>/foo</command-name><command-args>bar</command-args> and appear
// in history.jsonl as "/foo bar" — crucially with a trailing space even when
// there are no args ("/clear "). Reconstructing them the same way is what lets
// recovered entries dedupe against the ones already in history.jsonl.
func extractDisplay(content json.RawMessage) (string, bool) {
	text, ok := contentText(content)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}

	if m := commandNameRe.FindStringSubmatch(text); m != nil {
		name := strings.TrimSpace(m[1])
		if name == "" {
			return "", false
		}
		var args string
		if a := commandArgsRe.FindStringSubmatch(text); a != nil {
			args = a[1]
		}
		return name + " " + args, true
	}

	for _, prefix := range []string{"<local-command-stdout>", "<system-reminder>", "Caveat:"} {
		if strings.HasPrefix(text, prefix) {
			return "", false
		}
	}
	if strings.HasPrefix(text, "<") {
		return "", false
	}
	return text, true
}

// contentText flattens a message's content to text. A string is returned as-is;
// a block list is joined from its text blocks. The presence of any tool_result
// block means this is not a user prompt.
func contentText(content json.RawMessage) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		return asString, true
	}
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
	return strings.Join(parts, "\n"), true
}

func writeHistoryFile(historyPath string, entries []mergedEntry) error {
	dir := filepath.Dir(historyPath)

	if data, err := os.ReadFile(historyPath); err == nil {
		if err := os.WriteFile(historyPath+".bak", data, 0o600); err != nil {
			return fmt.Errorf("failed to back up %s: %w", historyPath, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".history-*.jsonl")
	if err != nil {
		return fmt.Errorf("creating temp history file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) // no-op after a successful rename
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
	if err := tmp.Chmod(0o600); err != nil {
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

// forEachLine calls fn for every non-empty line, buffering at most
// maxHistoryLineBytes per line. Longer lines (e.g. large tool-result payloads,
// never prompts) are drained and skipped so memory stays bounded.
func forEachLine(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		var line []byte
		tooLong := false
		for {
			chunk, err := br.ReadSlice('\n')
			if !tooLong && len(line)+len(chunk) > maxHistoryLineBytes {
				tooLong = true
				line = nil
			}
			if !tooLong {
				line = append(line, chunk...)
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err == io.EOF {
				if !tooLong {
					if t := strings.TrimSpace(string(line)); t != "" {
						fn([]byte(t))
					}
				}
				return nil
			}
			if err != nil {
				return err
			}
			if !tooLong {
				if t := strings.TrimSpace(string(line)); t != "" {
					fn([]byte(t))
				}
			}
			break
		}
	}
}
