package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRecord(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func countRecords(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "local_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

// A record's own sessionId is machine-local, so the same conversation carries a
// different sessionId on every device. Matching on it would add a duplicate
// sidebar row for every session on every pull. cliSessionId is the stable
// cross-machine identity and is what dedupe must key on.
func TestApplySkipsSessionsThatAlreadyHaveARecord(t *testing.T) {
	dir := t.TempDir()
	writeRecord(t, dir, "local_already-here.json",
		`{"sessionId":"local_already-here","cliSessionId":"abc-123","isArchived":false}`)

	incoming := Incoming{Record: &Record{fields: map[string]any{
		"sessionId":    "local_from-other-machine",
		"cliSessionId": "abc-123",
		"isArchived":   false,
	}}}

	result, err := Apply(dir, []Incoming{incoming})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result.Written != 0 {
		t.Errorf("Written = %d, want 0 (session already had a record)", result.Written)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (nothing newer to apply)", result.Updated)
	}
	if n := countRecords(t, dir); n != 1 {
		t.Errorf("directory holds %d records, want 1 (duplicate created)", n)
	}
}

// A session with no local record is the case worth acting on: write it.
func TestApplyWritesRecordsForUnknownSessions(t *testing.T) {
	dir := t.TempDir()
	incoming := Incoming{Record: &Record{fields: map[string]any{
		"sessionId":    "local_incoming",
		"cliSessionId": "brand-new",
		"isArchived":   false,
	}}}

	result, err := Apply(dir, []Incoming{incoming})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result.Written != 1 {
		t.Errorf("Written = %d, want 1", result.Written)
	}
	if n := countRecords(t, dir); n != 1 {
		t.Errorf("directory holds %d records, want 1", n)
	}
}

// sessionId arrives from the remote and is used to name the file on disk, which
// makes it attacker-influenced input to a filesystem write. A traversing value
// must never escape the index directory.
func TestApplyRejectsTraversingSessionID(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, hostile := range []string{"../escaped", "../../escaped", "nested/escaped"} {
		incoming := Incoming{Record: &Record{fields: map[string]any{
			"sessionId":    hostile,
			"cliSessionId": "session-" + hostile,
		}}}

		result, err := Apply(dir, []Incoming{incoming})
		if err == nil && result.Written != 0 {
			t.Errorf("sessionId %q was written; want refusal", hostile)
		}
	}

	escaped, err := filepath.Glob(filepath.Join(parent, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(escaped) != 0 {
		t.Errorf("records escaped the index directory: %v", escaped)
	}
}

func archiveStatusOnDisk(t *testing.T, path string) ArchiveStatus {
	t.Helper()
	rec, err := LoadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ArchiveStatus()
}

// Archiving is a deliberate user action on whichever machine performed it, so
// the more recent decision wins. Here the remote archived a session the local
// sidebar still shows as active.
func TestApplyAdoptsRemoteArchiveStatusWhenRemoteIsNewer(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, "local_a.json",
		`{"sessionId":"local_a","cliSessionId":"abc","isArchived":false}`)
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}

	incoming := Incoming{
		Record: &Record{fields: map[string]any{
			"sessionId": "local_remote", "cliSessionId": "abc", "isArchived": true,
		}},
		ObservedAt: time.Now(),
	}

	result, err := Apply(dir, []Incoming{incoming})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if got := archiveStatusOnDisk(t, path); got != ArchiveArchived {
		t.Errorf("on-disk status = %v, want archived", got)
	}
	if n := countRecords(t, dir); n != 1 {
		t.Errorf("directory holds %d records, want 1", n)
	}
}

// The mirror case: a local archive decision made after the remote observation
// must not be undone by an older remote label.
func TestApplyKeepsLocalArchiveStatusWhenLocalIsNewer(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, "local_a.json",
		`{"sessionId":"local_a","cliSessionId":"abc","isArchived":true}`)

	incoming := Incoming{
		Record: &Record{fields: map[string]any{
			"sessionId": "local_remote", "cliSessionId": "abc", "isArchived": false,
		}},
		ObservedAt: time.Now().Add(-2 * time.Hour),
	}

	result, err := Apply(dir, []Incoming{incoming})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (local decision is newer)", result.Updated)
	}
	if got := archiveStatusOnDisk(t, path); got != ArchiveArchived {
		t.Errorf("on-disk status = %v, want archived (local state was overwritten)", got)
	}
}

// An incoming label of unknown provenance carries no decision at all. It must
// never overwrite a known local state, however recent it appears.
func TestApplyNeverLetsUnknownOverwriteKnownStatus(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, "local_a.json",
		`{"sessionId":"local_a","cliSessionId":"abc","isArchived":true}`)

	incoming := Incoming{
		Record:     &Record{fields: map[string]any{"sessionId": "local_r", "cliSessionId": "abc"}},
		ObservedAt: time.Now().Add(time.Hour),
	}

	if _, err := Apply(dir, []Incoming{incoming}); err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if got := archiveStatusOnDisk(t, path); got != ArchiveArchived {
		t.Errorf("on-disk status = %v, want archived (unknown label clobbered it)", got)
	}
}

// A record's mtime is its observation time on the next push. If Apply leaves
// a newly written record stamped "now", this machine later reports the other
// device's decision as though it were made here, just now — and that inflated
// time can beat a genuinely later change back on the originating device.
func TestApplyStampsNewRecordWithObservedAt(t *testing.T) {
	dir := t.TempDir()
	observed := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	in := Incoming{
		Record:     &Record{fields: map[string]any{"sessionId": "local_a", "cliSessionId": "abc", "isArchived": true}},
		ObservedAt: observed,
	}

	if _, err := Apply(dir, []Incoming{in}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "local_a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(observed) {
		t.Errorf("record mtime = %v, want the incoming ObservedAt %v", info.ModTime(), observed)
	}
}

// The same holds when an existing record adopts a remote archive decision:
// the record should carry the time of that decision, not the time of the pull.
func TestApplyStampsReconciledRecordWithObservedAt(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, "local_a.json",
		`{"sessionId":"local_a","cliSessionId":"abc","isArchived":false}`)
	stale := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	observed := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	in := Incoming{
		Record:     &Record{fields: map[string]any{"sessionId": "local_r", "cliSessionId": "abc", "isArchived": true}},
		ObservedAt: observed,
	}

	res, err := Apply(dir, []Incoming{in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Fatalf("Updated = %d, want 1", res.Updated)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(observed) {
		t.Errorf("reconciled record mtime = %v, want ObservedAt %v", info.ModTime(), observed)
	}
}
