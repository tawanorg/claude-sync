package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The desktop index lives at claude-code-sessions/<account>/<workspace>/, and
// those two UUID segments differ between machines. Pull must discover the local
// pair rather than reusing the ones baked into synced records, or it writes
// into a directory the local app never reads.
func TestIndexDirFindsSingleLeaf(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "6dfc58e6-account", "580d1a7a-workspace")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := IndexDir(root)
	if err != nil {
		t.Fatalf("IndexDir() unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("IndexDir() = %q, want %q", got, want)
	}
}

// Silently picking one of several candidates would write records into a
// directory the running app may not read, producing a pull that reports success
// and changes nothing the user can see. Fail loudly and name the candidates.
func TestIndexDirRejectsAmbiguousLeaves(t *testing.T) {
	root := t.TempDir()
	for _, leaf := range []string{"acct-a/ws-one", "acct-b/ws-two"} {
		if err := os.MkdirAll(filepath.Join(root, leaf), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	_, err := IndexDir(root)
	if !errors.Is(err, ErrAmbiguousIndex) {
		t.Fatalf("IndexDir() error = %v, want ErrAmbiguousIndex", err)
	}
	for _, want := range []string{"ws-one", "ws-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name candidate %q", err, want)
		}
	}
}

// A machine that has never opened the desktop app has no index at all. That is
// an ordinary skip, not a failure, so callers need to distinguish it.
func TestIndexDirReportsMissingIndex(t *testing.T) {
	if _, err := IndexDir(t.TempDir()); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("IndexDir() error = %v, want ErrNoIndex", err)
	}
}

// A CLI-only machine has no claude-code-sessions directory whatsoever. That is
// the same "nothing to do" condition as an empty index and must not surface as
// an opaque filesystem error that aborts an otherwise healthy pull.
func TestIndexDirTreatsMissingRootAsNoIndex(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "claude-code-sessions")

	if _, err := IndexDir(missing); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("IndexDir() error = %v, want ErrNoIndex", err)
	}
}
