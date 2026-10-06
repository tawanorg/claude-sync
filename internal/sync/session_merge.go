package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// sessionStateTypes are transcript record types that are MUTABLE session
// state rather than append-only events: they carry no uuid and no timestamp,
// and Claude Code treats the LAST occurrence in the file as current.
// Verified against 1,180 transcripts (12,679 such records, zero timestamps).
var sessionStateTypes = map[string]bool{
	"mode":            true,
	"custom-title":    true,
	"ai-title":        true,
	"permission-mode": true,
	"last-prompt":     true,
	"worktree-state":  true,
}

type sessionRecord struct {
	UUID string `json:"uuid"`
	Type string `json:"type"`
}

// MergeSessionPayloads unions two genuinely diverged session transcripts.
// Like MergeHistoryPayloads, it returns ONLY lines to append — the local file
// is never rewritten, so a live session's concurrent appends survive, and
// applying the merge is idempotent.
//
// Record classes are handled by their write semantics:
//   - uuid-keyed events: immutable DAG nodes (parentUuid links); remote-only
//     events are appended in remote order. Claude Code already reads forked
//     DAGs (782 of 1,180 real files contain forks), so append order is safe.
//   - uuid-less last-wins state (sessionStateTypes): these carry no
//     timestamp, so per-record ordering across machines is impossible. Each
//     side's FINAL record per type is tracked independently (last one in the
//     file wins on that side) — a state line is never raw-consumed or
//     appended directly, only compared as a whole final value. Remote's
//     final record per type is appended when local never had that type at
//     all (nothing local to protect, so remote's value is preserved
//     regardless of file times), or when the remote file is newer and its
//     final value differs from local's final value. Everything else —
//     local is newer and has its own value, or the two finals are
//     byte-identical — appends nothing, so a stale intermediate remote
//     record (e.g. remote wrote mode=plan then mode=normal, matching
//     local's mode=normal) can never resurrect as a spurious append.
//   - other uuid-less records (queue-operation, ...): exact-raw-line multiset
//     union, the same rule that makes the history merge idempotent.
//
// Unparseable remote lines with no exact local match are dropped, not
// propagated.
func MergeSessionPayloads(local, remote []byte, localNewer bool) (addedLines [][]byte, err error) {
	rawCount := make(map[string]int)
	uuids := make(map[string]bool)
	localFinal := make(map[string][]byte) // last local state record per type

	err = forEachLine(bytes.NewReader(local), func(line []byte) {
		rawCount[string(line)]++
		var r sessionRecord
		if json.Unmarshal(line, &r) != nil {
			return
		}
		if r.UUID != "" {
			uuids[r.UUID] = true
			return
		}
		if sessionStateTypes[r.Type] {
			localFinal[r.Type] = append([]byte(nil), line...)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("parsing local session: %w", err)
	}

	remoteFinal := make(map[string][]byte) // last remote state record per type
	var remoteStateOrder []string

	err = forEachLine(bytes.NewReader(remote), func(line []byte) {
		var r sessionRecord
		parseErr := json.Unmarshal(line, &r)
		if parseErr == nil && r.UUID == "" && sessionStateTypes[r.Type] {
			// State lines never raw-consume and never append directly: only
			// the final value per type (tracked here) is ever considered.
			if _, seen := remoteFinal[r.Type]; !seen {
				remoteStateOrder = append(remoteStateOrder, r.Type)
			}
			remoteFinal[r.Type] = append([]byte(nil), line...)
			return
		}
		if rawCount[string(line)] > 0 {
			rawCount[string(line)]--
			return
		}
		if parseErr != nil {
			return // unparseable remote-only line: drop
		}
		if r.UUID != "" {
			if uuids[r.UUID] {
				return // same event, different serialization: local wins
			}
			addedLines = append(addedLines, append([]byte(nil), line...))
			return
		}
		addedLines = append(addedLines, append([]byte(nil), line...))
	})
	if err != nil {
		return nil, fmt.Errorf("parsing remote session: %w", err)
	}

	for _, t := range remoteStateOrder {
		localVal, hadLocal := localFinal[t]
		if !hadLocal || (!localNewer && !bytes.Equal(remoteFinal[t], localVal)) {
			addedLines = append(addedLines, remoteFinal[t])
		}
	}
	return addedLines, nil
}
