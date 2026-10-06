package sync

import (
	"bytes"
	"strings"
)

// PrefixRelation classifies how the local and remote payloads of one file
// relate, for append-only files where "one side is simply ahead" must not be
// treated as a conflict.
//
// Claude Code session transcripts (*.jsonl) are append-only. In a corpus of 92
// conflict artifacts collected across three synced machines, every single one
// was a strict byte-prefix relation: one side was exactly the other plus an
// appended tail. Byte comparison is exact for this file class — it cannot
// misorder records, because it never interleaves content.
type PrefixRelation int

const (
	// PrefixNone means neither payload contains the other: genuinely diverged.
	PrefixNone PrefixRelation = iota
	// PrefixEqual means the payloads are byte-identical.
	PrefixEqual
	// PrefixLocalAhead means remote is a strict prefix of local: local has an
	// appended tail the bucket has not seen.
	PrefixLocalAhead
	// PrefixRemoteAhead means local is a strict prefix of remote: the local
	// copy is truncated relative to the bucket.
	PrefixRemoteAhead
)

// ClassifyPrefix reports how local relates to remote byte-wise: identical, one
// a strict prefix of the other, or diverged. Equality is checked first, so the
// two Ahead results always denote STRICT extension.
func ClassifyPrefix(local, remote []byte) PrefixRelation {
	switch {
	case bytes.Equal(local, remote):
		return PrefixEqual
	case bytes.HasPrefix(remote, local):
		return PrefixRemoteAhead
	case bytes.HasPrefix(local, remote):
		return PrefixLocalAhead
	default:
		return PrefixNone
	}
}

// isSessionJSONL reports whether relativePath is an append-only session
// transcript eligible for fast-forward resolution. history.jsonl is a single
// file shared by every machine with different merge semantics, so only the
// root one is excluded; a nested projects/**/history.jsonl is a session file.
func isSessionJSONL(relativePath string) bool {
	return strings.HasSuffix(relativePath, ".jsonl") && relativePath != HistoryFile
}
