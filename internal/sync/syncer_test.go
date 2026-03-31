package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
)

// testSyncer creates a Syncer with in-memory mock storage and temp dirs using NewSyncerWith.
func testSyncer(t *testing.T) (*Syncer, *mockStorage, string) {
	t.Helper()
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	stateDir := filepath.Join(tmpDir, ".claude-sync")

	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create claude dir: %v", err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	enc := testEncryptor(t, stateDir)
	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	store := newMockStorage()
	cfg := &config.Config{}
	syncer := NewSyncerWith(cfg, store, enc, state, claudeDir, true)

	return syncer, store, claudeDir
}

// testEncryptor creates a real encryptor with a temporary key file.
func testEncryptor(t *testing.T, dir string) *crypto.Encryptor {
	t.Helper()
	keyPath := filepath.Join(dir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "test-passphrase"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}
	return enc
}

// createTestFile creates a file under the given directory with the specified content.
func createTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", name, err)
	}
}

func TestNewSyncerWith(t *testing.T) {
	syncer, store, claudeDir := testSyncer(t)

	if syncer == nil {
		t.Fatal("Expected non-nil syncer")
	}
	if syncer.storage != store {
		t.Error("Storage was not set correctly")
	}
	if syncer.claudeDir != claudeDir {
		t.Errorf("claudeDir mismatch: got %q, want %q", syncer.claudeDir, claudeDir)
	}
	if !syncer.quiet {
		t.Error("Expected quiet to be true")
	}
	if syncer.encryptor == nil {
		t.Error("Expected non-nil encryptor")
	}
	if syncer.state == nil {
		t.Error("Expected non-nil state")
	}
	if syncer.cfg == nil {
		t.Error("Expected non-nil config")
	}
}
