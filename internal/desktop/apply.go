package desktop

import (
	"os"
	"path/filepath"
	"time"
)

// Incoming is an index record from the remote together with the moment the
// machine that pushed it last observed that record. ObservedAt is what makes
// archive reconciliation last-writer-wins; it is carried alongside the record
// rather than inside it because the record schema belongs to the desktop app
// and must not gain fields this tool invented.
type Incoming struct {
	Record     *Record
	ObservedAt time.Time
}

// Result summarises what Apply changed.
type Result struct {
	Written int
	Updated int
	Skipped int
}

// existingSessionIDs maps cliSessionId to the record file already representing
// it, so Apply can tell which sessions the local sidebar already knows about.
func existingSessionIDs(dir string) (map[string]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "local_*.json"))
	if err != nil {
		return nil, err
	}
	known := make(map[string]string, len(matches))
	for _, path := range matches {
		rec, err := LoadRecord(path)
		if err != nil {
			// A record the app wrote in a format we cannot parse is not a
			// reason to abort a pull; treat it as opaque and move on.
			continue
		}
		if id := rec.CLISessionID(); id != "" {
			known[id] = path
		}
	}
	return known, nil
}

// Apply writes index records into dir for sessions the local sidebar does not
// already know about.
//
// Dedupe keys on cliSessionId, never on the record's own sessionId: sessionId
// is minted per machine, so the same conversation carries a different one on
// every device and matching on it would append a duplicate sidebar row on each
// pull.
func Apply(dir string, incoming []Incoming) (Result, error) {
	known, err := existingSessionIDs(dir)
	if err != nil {
		return Result{}, err
	}

	var result Result
	for _, in := range incoming {
		if in.Record == nil {
			result.Skipped++
			continue
		}
		id := in.Record.CLISessionID()
		if id == "" {
			result.Skipped++
			continue
		}

		if path, exists := known[id]; exists {
			updated, err := reconcileArchive(path, in)
			if err != nil {
				return result, err
			}
			if updated {
				result.Updated++
			} else {
				result.Skipped++
			}
			continue
		}

		name, err := in.Record.fileName()
		if err != nil {
			result.Skipped++
			continue
		}
		path := filepath.Join(dir, name)
		if err := in.Record.Save(path); err != nil {
			return result, err
		}
		stampObservedAt(path, in.ObservedAt)
		known[id] = path
		result.Written++
	}
	return result, nil
}

// reconcileArchive settles the archive state of a session the local sidebar
// already knows about, on a last-writer-wins basis.
//
// The local record's mtime stands in for "when this machine last decided": the
// desktop app rewrites the record when a session is archived or unarchived, so
// no field needs to be added to an app-owned schema to track it.
func reconcileArchive(path string, in Incoming) (bool, error) {
	status := in.Record.ArchiveStatus()
	if status == ArchiveUnknown {
		// The pushing machine had no desktop index and so expressed no
		// decision. Silence must never overwrite a known local state.
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !in.ObservedAt.After(info.ModTime()) {
		return false, nil
	}

	local, err := LoadRecord(path)
	if err != nil {
		return false, nil
	}
	if local.ArchiveStatus() == status {
		return false, nil
	}
	local.setArchiveStatus(status)
	if err := local.Save(path); err != nil {
		return false, err
	}
	stampObservedAt(path, in.ObservedAt)
	return true, nil
}

// stampObservedAt sets the record's mtime to the moment the decision it
// carries was made. The mtime doubles as the observation time on the next
// push, so leaving a freshly written record at "now" would report another
// device's decision as though it were made here just now — an inflated time
// that could then beat a genuinely later change on the originating device.
// A zero ObservedAt carries no timing information and leaves the mtime alone.
func stampObservedAt(path string, observed time.Time) {
	if observed.IsZero() {
		return
	}
	// Best effort: a failure here degrades to the previous behaviour rather
	// than failing a pull that has already written the record correctly.
	_ = os.Chtimes(path, observed, observed)
}
