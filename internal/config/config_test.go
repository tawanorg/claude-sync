package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDirPath(t *testing.T) {
	path := ConfigDirPath()
	if path == "" {
		t.Fatal("ConfigDirPath should not return empty string")
	}

	if !strings.HasSuffix(path, ConfigDir) {
		t.Errorf("ConfigDirPath should end with '%s', got '%s'", ConfigDir, path)
	}
}

func TestConfigFilePath(t *testing.T) {
	path := ConfigFilePath()
	if path == "" {
		t.Fatal("ConfigFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, ConfigFile) {
		t.Errorf("ConfigFilePath should end with '%s', got '%s'", ConfigFile, path)
	}
}

func TestStateFilePath(t *testing.T) {
	path := StateFilePath()
	if path == "" {
		t.Fatal("StateFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, StateFile) {
		t.Errorf("StateFilePath should end with '%s', got '%s'", StateFile, path)
	}
}

func TestAgeKeyFilePath(t *testing.T) {
	path := AgeKeyFilePath()
	if path == "" {
		t.Fatal("AgeKeyFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, AgeKeyFile) {
		t.Errorf("AgeKeyFilePath should end with '%s', got '%s'", AgeKeyFile, path)
	}
}

func TestClaudeDir(t *testing.T) {
	path := ClaudeDir()
	if path == "" {
		t.Fatal("ClaudeDir should not return empty string")
	}

	if !strings.HasSuffix(path, ".claude") {
		t.Errorf("ClaudeDir should end with '.claude', got '%s'", path)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create a temporary directory to use as home
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create config directory
	configDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Create test config
	cfg := &Config{
		AccountID:       "test-account-id",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
		Bucket:          "test-bucket",
		EncryptionKey:   "~/.claude-sync/age-key.txt",
	}

	// Save config
	configPath := filepath.Join(configDir, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatalf("Failed to create config parent dir: %v", err)
	}

	// Write config manually since Save uses hardcoded path
	data := `account_id: test-account-id
access_key_id: test-access-key
secret_access_key: test-secret-key
bucket: test-bucket
encryption_key_path: ~/.claude-sync/age-key.txt
`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Load config
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded config
	if loaded.AccountID != cfg.AccountID {
		t.Errorf("AccountID mismatch: expected '%s', got '%s'", cfg.AccountID, loaded.AccountID)
	}
	if loaded.AccessKeyID != cfg.AccessKeyID {
		t.Errorf("AccessKeyID mismatch: expected '%s', got '%s'", cfg.AccessKeyID, loaded.AccessKeyID)
	}
	if loaded.SecretAccessKey != cfg.SecretAccessKey {
		t.Errorf("SecretAccessKey mismatch: expected '%s', got '%s'", cfg.SecretAccessKey, loaded.SecretAccessKey)
	}
	if loaded.Bucket != cfg.Bucket {
		t.Errorf("Bucket mismatch: expected '%s', got '%s'", cfg.Bucket, loaded.Bucket)
	}

	// Check that ~ is expanded in encryption key path
	if strings.HasPrefix(loaded.EncryptionKey, "~") {
		t.Error("EncryptionKey should have ~ expanded")
	}

	// Check that endpoint is auto-populated
	expectedEndpoint := "https://test-account-id.r2.cloudflarestorage.com"
	if loaded.Endpoint != expectedEndpoint {
		t.Errorf("Endpoint mismatch: expected '%s', got '%s'", expectedEndpoint, loaded.Endpoint)
	}
}

func TestLoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail when config doesn't exist")
	}

	if !strings.Contains(err.Error(), "run 'claude-sync init' first") {
		t.Errorf("Error should mention running init, got: %v", err)
	}
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Should not exist initially
	if Exists() {
		t.Error("Exists should return false when config doesn't exist")
	}

	// Create config file
	configDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Should exist now
	if !Exists() {
		t.Error("Exists should return true when config exists")
	}
}

func TestSyncPaths(t *testing.T) {
	// Verify SyncPaths contains expected entries
	expectedPaths := map[string]bool{
		"CLAUDE.md":           false,
		"settings.json":       false,
		"settings.local.json": false,
		"agents":              false,
		"skills":              false,
		"plugins":             false,
		"projects":            false,
		"history.jsonl":       false,
		"rules":               false,
	}

	for _, path := range SyncPaths {
		if _, ok := expectedPaths[path]; ok {
			expectedPaths[path] = true
		}
	}

	for path, found := range expectedPaths {
		if !found {
			t.Errorf("Expected path '%s' not found in SyncPaths", path)
		}
	}
}
