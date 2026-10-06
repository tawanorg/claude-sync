package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The record schema belongs to the desktop app, is undocumented, and gains
// fields across app versions. Modelling it as a fixed struct would silently drop
// anything this tool has not been taught about, corrupting records on every
// round-trip. Carry unknown fields through untouched.
func TestRecordRoundTripPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "local_source.json")
	original := `{"sessionId":"local_a","cliSessionId":"abc-123","isArchived":false,` +
		`"fieldFromANewerApp":{"nested":[1,2]}}`
	if err := os.WriteFile(src, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, err := LoadRecord(src)
	if err != nil {
		t.Fatalf("LoadRecord() error: %v", err)
	}

	dst := filepath.Join(dir, "local_dest.json")
	if err := rec.Save(dst); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved record is not valid JSON: %v", err)
	}
	if _, ok := got["fieldFromANewerApp"]; !ok {
		t.Errorf("unknown field dropped on round-trip; got keys %v", keysOf(got))
	}
	if got["cliSessionId"] != "abc-123" {
		t.Errorf("cliSessionId = %v, want abc-123", got["cliSessionId"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Archive state exists only in the desktop index; nothing in a CLI transcript
// encodes it. A machine that never opened the desktop app therefore knows
// nothing about a session's archive state, which is NOT the same as knowing it
// is active. Collapsing the two into a bool would let a CLI-only push report
// every session as active and un-archive them on every other machine.
func TestArchiveStatusDistinguishesUnknownFromActive(t *testing.T) {
	cases := []struct {
		name string
		body string
		want ArchiveStatus
	}{
		{"explicitly archived", `{"isArchived":true}`, ArchiveArchived},
		{"explicitly active", `{"isArchived":false}`, ArchiveActive},
		{"field absent entirely", `{"cliSessionId":"x"}`, ArchiveUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local_r.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			rec, err := LoadRecord(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := rec.ArchiveStatus(); got != tc.want {
				t.Errorf("ArchiveStatus() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Records travel to and from remote storage inside a sync payload, so they must
// survive encoding/json in both directions with unknown fields intact.
func TestRecordSurvivesEncodingJSONRoundTrip(t *testing.T) {
	src := []byte(`{"cliSessionId":"abc","isArchived":true,"unknownField":42}`)

	var rec Record
	if err := json.Unmarshal(src, &rec); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if rec.CLISessionID() != "abc" {
		t.Errorf("CLISessionID() = %q, want abc", rec.CLISessionID())
	}
	if rec.ArchiveStatus() != ArchiveArchived {
		t.Errorf("ArchiveStatus() = %v, want archived", rec.ArchiveStatus())
	}

	out, err := json.Marshal(&rec)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["unknownField"] != float64(42) {
		t.Errorf("unknownField = %v, want 42 (dropped in transit)", got["unknownField"])
	}
}
