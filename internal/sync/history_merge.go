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
//   - an identical line exists locally, compared with surrounding whitespace
//     trimmed (forEachLine does this, so line-ending differences between
//     machines don't defeat the match) — this is what dedupes blank
//     submissions and unknown-shape records, which have no prompt signature
//     and would otherwise multiply on every cycle, or
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
	needsLeadingNewline, err := fileEndsWithoutNewline(path)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if needsLeadingNewline {
		buf.WriteByte('\n')
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

// fileEndsWithoutNewline reports whether path names a non-empty file whose
// last byte isn't '\n'. It reads only that one byte via Stat+ReadAt instead
// of loading the whole file, since history/transcript files this merge
// targets can run into the gigabytes.
func fileEndsWithoutNewline(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}

	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}
