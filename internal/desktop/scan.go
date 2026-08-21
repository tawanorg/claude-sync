package desktop

import (
	"os"
	"path/filepath"
	"time"
)

// Label is the archive state of one session together with when that state was
// last observed. ObservedAt comes from the record's modification time: the
// desktop app rewrites a record when the session is archived or unarchived, so
// the mtime tracks the decision without adding a field to an app-owned schema.
type Label struct {
	Status     ArchiveStatus
	ObservedAt time.Time
}

// Scan reads the local index and returns each session's archive label, keyed by
// cliSessionId — the identity that is stable across machines.
//
// A missing index is not an error: a machine that has never opened the desktop
// app simply contributes no labels, leaving push otherwise unchanged.
func Scan(dir string) (map[string]Label, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "local_*.json"))
	if err != nil {
		return nil, err
	}

	labels := make(map[string]Label, len(matches))
	for _, path := range matches {
		rec, err := LoadRecord(path)
		if err != nil {
			// An unparseable record is opaque, not fatal; it simply
			// contributes no opinion about its session.
			continue
		}
		id := rec.CLISessionID()
		if id == "" {
			continue
		}
		var observed time.Time
		if info, err := os.Stat(path); err == nil {
			observed = info.ModTime()
		}
		labels[id] = Label{Status: rec.ArchiveStatus(), ObservedAt: observed}
	}
	return labels, nil
}

// Snapshot is one index record together with when it was last written.
type Snapshot struct {
	Record     *Record
	ObservedAt time.Time
}

// Snapshots loads every index record in dir. A missing index yields no
// snapshots rather than an error, so a CLI-only machine stays a normal machine.
func Snapshots(dir string) ([]Snapshot, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "local_*.json"))
	if err != nil {
		return nil, err
	}

	out := make([]Snapshot, 0, len(matches))
	for _, path := range matches {
		rec, err := LoadRecord(path)
		if err != nil {
			continue
		}
		if rec.CLISessionID() == "" {
			continue
		}
		var observed time.Time
		if info, err := os.Stat(path); err == nil {
			observed = info.ModTime()
		}
		out = append(out, Snapshot{Record: rec, ObservedAt: observed})
	}
	return out, nil
}
