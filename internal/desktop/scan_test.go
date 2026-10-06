package desktop

import (
	"os"
	"testing"
	"time"
)

// Push needs the archive state of every session the local sidebar knows about,
// keyed by the cross-machine identity, together with when that state was last
// touched so the other end can settle conflicts.
func TestScanLabelsSessionsByCLISessionID(t *testing.T) {
	dir := t.TempDir()
	writeRecord(t, dir, "local_one.json",
		`{"sessionId":"local_one","cliSessionId":"aaa","isArchived":true}`)
	writeRecord(t, dir, "local_two.json",
		`{"sessionId":"local_two","cliSessionId":"bbb","isArchived":false}`)

	labels, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("Scan() returned %d labels, want 2", len(labels))
	}
	if got := labels["aaa"].Status; got != ArchiveArchived {
		t.Errorf("aaa status = %v, want archived", got)
	}
	if got := labels["bbb"].Status; got != ArchiveActive {
		t.Errorf("bbb status = %v, want active", got)
	}
	if labels["aaa"].ObservedAt.IsZero() {
		t.Error("label carries no observation time; conflicts cannot be settled")
	}
}

// A machine with no desktop index must report no labels rather than an error,
// so a CLI-only push stays a normal push.
func TestScanOnMissingDirReportsNoLabels(t *testing.T) {
	labels, err := Scan(t.TempDir() + "/absent")
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(labels) != 0 {
		t.Errorf("Scan() returned %d labels, want 0", len(labels))
	}
}

// The observation time must track the record, not the moment of the scan, or
// every push would look like the freshest decision and always win.
func TestScanUsesRecordModificationTime(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, "local_one.json",
		`{"sessionId":"local_one","cliSessionId":"aaa","isArchived":true}`)
	stale := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}

	labels, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels["aaa"].ObservedAt; got.After(time.Now().Add(-time.Hour)) {
		t.Errorf("ObservedAt = %v, want the record's stale mtime", got)
	}
}
