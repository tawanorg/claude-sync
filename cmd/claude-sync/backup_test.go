package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateBackupSetsRestrictivePermissions verifies that the backup directory
// and the files copied into it are user-only readable/writable. ~/.claude can
// contain API keys, prompts, and personal context, so backups must not be
// world-readable either.
func TestCreateBackupSetsRestrictivePermissions(t *testing.T) {
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", originalHome)

	// Populate ~/.claude with a file inside a syncable subdirectory so that
	// createBackup also creates a nested directory we can stat.
	claudeDir := filepath.Join(tmpHome, ".claude")
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.MkdirAll(agentsDir, 0700); err != nil {
		t.Fatalf("Failed to create agents dir: %v", err)
	}
	helperPath := filepath.Join(agentsDir, "helper.json")
	if err := os.WriteFile(helperPath, []byte(`{"name":"helper"}`), 0600); err != nil {
		t.Fatalf("Failed to create helper.json: %v", err)
	}

	backupDir, err := createBackup()
	if err != nil {
		t.Fatalf("createBackup failed: %v", err)
	}

	// Backup root must be 0700.
	bi, err := os.Stat(backupDir)
	if err != nil {
		t.Fatalf("Stat backupDir failed: %v", err)
	}
	if got := bi.Mode().Perm(); got != 0700 {
		t.Errorf("Expected backup root mode 0700, got %o", got)
	}

	// Nested directory created during backup must be 0700.
	backupAgents := filepath.Join(backupDir, "agents")
	di, err := os.Stat(backupAgents)
	if err != nil {
		t.Fatalf("Stat backup agents dir failed: %v", err)
	}
	if got := di.Mode().Perm(); got != 0700 {
		t.Errorf("Expected backup nested dir mode 0700, got %o", got)
	}

	// Backed-up file must be 0600.
	backupFile := filepath.Join(backupDir, "agents", "helper.json")
	fi, err := os.Stat(backupFile)
	if err != nil {
		t.Fatalf("Stat backup file failed: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("Expected backup file mode 0600, got %o", got)
	}
}
