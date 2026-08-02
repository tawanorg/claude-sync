package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// MergeHistoryPayloads unions two history.jsonl payloads.
//
// history.jsonl is append-only per machine, so a two-way union is a complete
// merge. The local payload is preserved verbatim as the prefix of the result —
// including unparseable lines (torn writes) and unknown record shapes — and is
// never reordered or rewritten; remote lines the local payload lacks are
// returned in addedLines and appended after it. This makes the merge
// idempotent (Merge(x, x) adds nothing) and lets callers apply it to the
// local file with O_APPEND instead of a rewrite, so a concurrent appender
// (a live Claude Code session) can never lose lines to a stale-read rewrite.
//
// Remote lines are dropped as duplicates when either:
//   - an identical raw line exists locally (exact-byte multiset match — this
//     is what dedupes blank submissions and unknown-shape records, which have
//     no prompt signature and would otherwise multiply on every cycle), or
//   - they parse to a prompt whose sessionId+display matches a local prompt
//     within historyDedupeWindowMs (same rule as RebuildHistory; the local
//     line wins and keeps its pastedContents).
//
// Remote lines that fail to parse and have no exact local match are dropped,
// not propagated.
func MergeHistoryPayloads(local, remote []byte) (merged []byte, addedLines [][]byte, err error) {
	type promptSig struct{ session, display string }
	seen := make(map[promptSig][]int64)
	rawCount := make(map[string]int)

	err = forEachLine(bytes.NewReader(local), func(line []byte) {
		rawCount[string(line)]++
		var entry HistoryEntry
		if json.Unmarshal(line, &entry) != nil || entry.Display == "" {
			return
		}
		sig := promptSig{entry.SessionID, entry.Display}
		seen[sig] = append(seen[sig], entry.Timestamp)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("parsing local history: %w", err)
	}

	err = forEachLine(bytes.NewReader(remote), func(line []byte) {
		if rawCount[string(line)] > 0 {
			rawCount[string(line)]--
			return
		}
		var entry HistoryEntry
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if entry.Display != "" {
			sig := promptSig{entry.SessionID, entry.Display}
			if withinWindow(seen[sig], entry.Timestamp) {
				return
			}
			seen[sig] = append(seen[sig], entry.Timestamp)
		}
		raw := make([]byte, len(line))
		copy(raw, line)
		addedLines = append(addedLines, raw)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("parsing remote history: %w", err)
	}

	if len(addedLines) == 0 {
		return local, nil, nil
	}

	var buf bytes.Buffer
	buf.Write(local)
	if len(local) > 0 && local[len(local)-1] != '\n' {
		buf.WriteByte('\n')
	}
	for _, line := range addedLines {
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), addedLines, nil
}

// appendHistoryLines appends lines to the history file with O_APPEND, never
// truncating or rewriting existing content: lines a live Claude Code session
// appends concurrently are preserved, and a crash mid-append can at worst
// leave one partial trailing line (which later merges keep verbatim).
func appendHistoryLines(path string, lines [][]byte) error {
	var buf bytes.Buffer
	if data, err := os.ReadFile(path); err == nil {
		if len(data) > 0 && data[len(data)-1] != '\n' {
			buf.WriteByte('\n')
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, line := range lines {
		buf.Write(line)
		buf.WriteByte('\n')
	}

	// Transcripts can contain secrets echoed by tools: keep them user-only
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
