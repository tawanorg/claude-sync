package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Record is one desktop sidebar index record.
//
// It is deliberately backed by a generic field map rather than a typed struct.
// The schema is owned by the desktop app and gains fields across versions; a
// struct would drop everything it has not been taught about each time a record
// is rewritten. Only the fields this tool reasons about are accessed by name.
type Record struct {
	fields map[string]any
}

// LoadRecord reads an index record from disk, preserving every field verbatim.
func LoadRecord(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("parse index record %s: %w", path, err)
	}
	return &Record{fields: fields}, nil
}

// Save writes the record, carrying unknown fields through unchanged. Records
// hold conversation titles and working directories, so they are written with
// the same owner-only permissions the desktop app uses.
func (r *Record) Save(path string) error {
	data, err := json.Marshal(r.fields)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ArchiveStatus is the tri-state archive state of a session.
//
// Archive state lives only in the desktop index: no record type in a CLI
// transcript encodes it. A machine without the desktop app therefore has no
// information about a session, which is distinct from knowing it is active.
// ArchiveUnknown is the zero value so a status that was never populated can
// never be mistaken for "active" and un-archive the session elsewhere.
type ArchiveStatus int

const (
	ArchiveUnknown ArchiveStatus = iota
	ArchiveActive
	ArchiveArchived
)

func (s ArchiveStatus) String() string {
	switch s {
	case ArchiveActive:
		return "active"
	case ArchiveArchived:
		return "archived"
	default:
		return "unknown"
	}
}

// ArchiveStatus reports whether the session is archived. A missing or
// non-boolean isArchived field yields ArchiveUnknown rather than a guess.
func (r *Record) ArchiveStatus() ArchiveStatus {
	value, ok := r.fields["isArchived"]
	if !ok {
		return ArchiveUnknown
	}
	archived, ok := value.(bool)
	if !ok {
		return ArchiveUnknown
	}
	if archived {
		return ArchiveArchived
	}
	return ArchiveActive
}

// CLISessionID returns the transcript this record points at. It is the stable
// identity across machines; the record's own sessionId is machine-local and
// must never be used to match records between devices.
func (r *Record) CLISessionID() string {
	id, _ := r.fields["cliSessionId"].(string)
	return id
}

// fileName returns the on-disk name for this record.
//
// sessionId reaches this tool from remote storage and is used to name a file,
// which makes it untrusted input to a filesystem write. Anything that is not a
// plain file name is refused rather than cleaned, so a malformed or hostile
// record can never place a file outside the index directory.
func (r *Record) fileName() (string, error) {
	id, _ := r.fields["sessionId"].(string)
	if id == "" {
		id = "local_" + r.CLISessionID()
	}
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("record has no usable session id")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("record session id %q is not a plain file name", id)
	}
	name := id + ".json"
	if name != filepath.Base(name) {
		return "", fmt.Errorf("record session id %q is not a plain file name", id)
	}
	return name, nil
}

// setArchiveStatus records a known archive decision. ArchiveUnknown is ignored:
// it carries no decision and must not write a value into the record.
func (r *Record) setArchiveStatus(status ArchiveStatus) {
	switch status {
	case ArchiveArchived:
		r.fields["isArchived"] = true
	case ArchiveActive:
		r.fields["isArchived"] = false
	}
}

// UnmarshalJSON stores every field verbatim so a record can be carried through
// a sync payload without losing anything this tool does not model.
func (r *Record) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.fields = fields
	return nil
}

// MarshalJSON writes the record back out exactly as it came in.
func (r *Record) MarshalJSON() ([]byte, error) {
	if r.fields == nil {
		return []byte("null"), nil
	}
	return json.Marshal(r.fields)
}

// NewRecord builds a record from an already-decoded field map. It exists for
// callers outside this package that assemble records from a sync payload.
func NewRecord(fields map[string]any) *Record {
	return &Record{fields: fields}
}
