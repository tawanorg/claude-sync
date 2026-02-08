package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
)

// TestFullWorkflowWithLocalState tests the sync workflow with real crypto
// but without actual R2 connection
func TestFullWorkflowWithLocalState(t *testing.T) {
	// Set up temporary directories
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	configDir := filepath.Join(tmpDir, ".claude-sync")

	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create claude dir: %v", err)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Generate encryption key using passphrase
	keyPath := filepath.Join(configDir, "age-key.txt")
	passphrase := "test-integration-passphrase-secure"

	err := crypto.GenerateKeyFromPassphrase(keyPath, passphrase)
	if err != nil {
		t.Fatalf("Failed to generate key from passphrase: %v", err)
	}

	// Create encryptor
	encryptor, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	// Create test files in claude directory
	testFiles := map[string]string{
		"CLAUDE.md":     "# My Claude Settings\n\nThis is a test.",
		"settings.json": `{"theme": "dark", "autoSave": true}`,
	}

	for name, content := range testFiles {
		path := filepath.Join(claudeDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", name, err)
		}
	}

	// Create agents subdirectory with files
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("Failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "agent1.json"), []byte(`{"name": "Agent 1"}`), 0644); err != nil {
		t.Fatalf("Failed to create agent file: %v", err)
	}

	// Initialize state
	state := NewState()

	// Detect changes (should be all adds)
	changes, err := state.DetectChanges(claudeDir, []string{"CLAUDE.md", "settings.json", "agents"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 3 {
		t.Errorf("Expected 3 new files, got %d", len(changes))
	}

	// Simulate push: encrypt each file and update state
	encryptedFiles := make(map[string][]byte)
	for _, change := range changes {
		fullPath := filepath.Join(claudeDir, change.Path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", change.Path, err)
		}

		encrypted, err := encryptor.Encrypt(data)
		if err != nil {
			t.Fatalf("Failed to encrypt file %s: %v", change.Path, err)
		}

		encryptedFiles[change.Path] = encrypted

		// Update state
		info, _ := os.Stat(fullPath)
		state.UpdateFile(change.Path, info, change.LocalHash)
		state.MarkUploaded(change.Path)
	}

	t.Logf("Encrypted %d files", len(encryptedFiles))

	// Verify no more changes after state update
	changes, err = state.DetectChanges(claudeDir, []string{"CLAUDE.md", "settings.json", "agents"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("Expected 0 changes after state update, got %d", len(changes))
	}

	// Simulate pull: decrypt files and verify content
	for path, encrypted := range encryptedFiles {
		decrypted, err := encryptor.Decrypt(encrypted)
		if err != nil {
			t.Fatalf("Failed to decrypt file %s: %v", path, err)
		}

		// Read original file and compare
		fullPath := filepath.Join(claudeDir, path)
		original, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("Failed to read original file %s: %v", path, err)
		}

		if string(decrypted) != string(original) {
			t.Errorf("Decrypted content doesn't match original for %s", path)
		}
	}

	t.Log("Full workflow with encryption completed successfully")
}

// TestCrossDeviceSyncWithPassphrase verifies that the same passphrase
// produces the same key on different "devices" (simulated with separate directories)
func TestCrossDeviceSyncWithPassphrase(t *testing.T) {
	tmpDir := t.TempDir()
	passphrase := "shared-passphrase-for-sync-test"

	// Device 1 setup
	device1Dir := filepath.Join(tmpDir, "device1")
	device1KeyPath := filepath.Join(device1Dir, "age-key.txt")
	if err := os.MkdirAll(device1Dir, 0700); err != nil {
		t.Fatalf("Failed to create device1 dir: %v", err)
	}

	err := crypto.GenerateKeyFromPassphrase(device1KeyPath, passphrase)
	if err != nil {
		t.Fatalf("Device1: Failed to generate key: %v", err)
	}

	enc1, err := crypto.NewEncryptor(device1KeyPath)
	if err != nil {
		t.Fatalf("Device1: Failed to create encryptor: %v", err)
	}

	// Device 2 setup
	device2Dir := filepath.Join(tmpDir, "device2")
	device2KeyPath := filepath.Join(device2Dir, "age-key.txt")
	if err := os.MkdirAll(device2Dir, 0700); err != nil {
		t.Fatalf("Failed to create device2 dir: %v", err)
	}

	err = crypto.GenerateKeyFromPassphrase(device2KeyPath, passphrase)
	if err != nil {
		t.Fatalf("Device2: Failed to generate key: %v", err)
	}

	enc2, err := crypto.NewEncryptor(device2KeyPath)
	if err != nil {
		t.Fatalf("Device2: Failed to create encryptor: %v", err)
	}

	// Verify both devices have the same public key
	if enc1.PublicKey() != enc2.PublicKey() {
		t.Errorf("Different public keys for same passphrase:\nDevice1: %s\nDevice2: %s",
			enc1.PublicKey(), enc2.PublicKey())
	}

	// Test cross-device encryption/decryption
	testData := []byte("Secret data that should be accessible on both devices")

	// Encrypt on device 1
	encrypted, err := enc1.Encrypt(testData)
	if err != nil {
		t.Fatalf("Device1: Failed to encrypt: %v", err)
	}

	// Decrypt on device 2
	decrypted, err := enc2.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Device2: Failed to decrypt data encrypted on device1: %v", err)
	}

	if string(decrypted) != string(testData) {
		t.Errorf("Cross-device decryption failed:\nExpected: %s\nGot: %s", testData, decrypted)
	}

	t.Log("Cross-device sync with passphrase verified successfully")
}

// TestSyncStateDetectsAllChangeTypes tests add, modify, delete detection
func TestSyncStateDetectsAllChangeTypes(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create claude dir: %v", err)
	}

	state := NewState()
	syncPaths := []string{"file1.txt", "file2.txt", "file3.txt"}

	// Create initial files
	for _, name := range syncPaths {
		path := filepath.Join(claudeDir, name)
		if err := os.WriteFile(path, []byte("initial content for "+name), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", name, err)
		}
	}

	// Initial detection - all adds
	changes, _ := state.DetectChanges(claudeDir, syncPaths)
	addCount := 0
	for _, c := range changes {
		if c.Action == "add" {
			addCount++
		}
	}
	if addCount != 3 {
		t.Errorf("Expected 3 add changes, got %d", addCount)
	}

	// Update state to simulate sync
	for _, c := range changes {
		info, _ := os.Stat(filepath.Join(claudeDir, c.Path))
		state.UpdateFile(c.Path, info, c.LocalHash)
	}

	// Modify file1
	if err := os.WriteFile(filepath.Join(claudeDir, "file1.txt"), []byte("modified content"), 0644); err != nil {
		t.Fatalf("Failed to modify file1: %v", err)
	}

	// Delete file2
	if err := os.Remove(filepath.Join(claudeDir, "file2.txt")); err != nil {
		t.Fatalf("Failed to delete file2: %v", err)
	}

	// file3 unchanged

	// Detect changes
	changes, _ = state.DetectChanges(claudeDir, syncPaths)

	var hasModify, hasDelete bool
	for _, c := range changes {
		switch c.Action {
		case "modify":
			if c.Path == "file1.txt" {
				hasModify = true
			}
		case "delete":
			if c.Path == "file2.txt" {
				hasDelete = true
			}
		}
	}

	if !hasModify {
		t.Error("Expected modify change for file1.txt")
	}
	if !hasDelete {
		t.Error("Expected delete change for file2.txt")
	}
	if len(changes) != 2 {
		t.Errorf("Expected 2 changes (modify + delete), got %d", len(changes))
	}
}

// TestConfigPaths verifies config path functions
func TestConfigPaths(t *testing.T) {
	// These should return non-empty paths
	if config.ConfigDirPath() == "" {
		t.Error("ConfigDirPath should not be empty")
	}
	if config.ConfigFilePath() == "" {
		t.Error("ConfigFilePath should not be empty")
	}
	if config.StateFilePath() == "" {
		t.Error("StateFilePath should not be empty")
	}
	if config.AgeKeyFilePath() == "" {
		t.Error("AgeKeyFilePath should not be empty")
	}
	if config.ClaudeDir() == "" {
		t.Error("ClaudeDir should not be empty")
	}
}

// TestSyncPathsConfig verifies sync paths are properly configured
func TestSyncPathsConfig(t *testing.T) {
	// Verify expected paths are in SyncPaths
	expectedPaths := []string{"CLAUDE.md", "settings.json", "agents", "skills", "plugins"}

	for _, expected := range expectedPaths {
		found := false
		for _, path := range config.SyncPaths {
			if path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected '%s' in SyncPaths", expected)
		}
	}
}
