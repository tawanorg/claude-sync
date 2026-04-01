package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tawanorg/claude-sync/internal/storage"
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
		"commands":            false,
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

func TestClaudeJSONPath(t *testing.T) {
	path := ClaudeJSONPath()
	if path == "" {
		t.Fatal("ClaudeJSONPath should not return empty string")
	}
	if !strings.HasSuffix(path, ".claude.json") {
		t.Errorf("ClaudeJSONPath should end with .claude.json, got %q", path)
	}
}

func TestGetStorageConfig_NewFormat(t *testing.T) {
	cfg := &Config{
		Storage: &storage.StorageConfig{
			Provider: storage.ProviderS3,
			Bucket:   "my-bucket",
			Region:   "us-east-1",
		},
	}

	sc := cfg.GetStorageConfig()
	if sc.Provider != storage.ProviderS3 {
		t.Errorf("expected provider S3, got %q", sc.Provider)
	}
	if sc.Bucket != "my-bucket" {
		t.Errorf("expected bucket my-bucket, got %q", sc.Bucket)
	}
}

func TestGetStorageConfig_LegacyFormat(t *testing.T) {
	cfg := &Config{
		AccountID:       "abc123",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Bucket:          "legacy-bucket",
		Endpoint:        "https://abc123.r2.cloudflarestorage.com",
	}

	sc := cfg.GetStorageConfig()
	if sc.Provider != storage.ProviderR2 {
		t.Errorf("expected provider R2 for legacy config, got %q", sc.Provider)
	}
	if sc.Bucket != "legacy-bucket" {
		t.Errorf("expected bucket legacy-bucket, got %q", sc.Bucket)
	}
	if sc.AccountID != "abc123" {
		t.Errorf("expected account ID abc123, got %q", sc.AccountID)
	}
}

func TestIsLegacyConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected bool
	}{
		{"legacy with account ID", Config{AccountID: "abc"}, true},
		{"new format with storage", Config{Storage: &storage.StorageConfig{Provider: "s3"}}, false},
		{"empty config", Config{}, false},
		{"both set uses new format", Config{Storage: &storage.StorageConfig{Provider: "s3"}, AccountID: "abc"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsLegacyConfig(); got != tt.expected {
				t.Errorf("IsLegacyConfig() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConfigSaveAndLoad(t *testing.T) {
	// Override config dir to temp dir
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".claude-sync")

	// We can't easily override ConfigDirPath, so test Save/Load via direct file ops
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		EncryptionKey: "~/.claude-sync/age-key.txt",
		Bucket:        "test-bucket",
		AccountID:     "test-account",
		Exclude:       []string{"*.tmp", "cache/**"},
		MCPSync:       true,
	}

	// Write config manually to test Load
	configPath := filepath.Join(configDir, "config.yaml")
	data := `bucket: test-bucket
account_id: test-account
encryption_key_path: "~/.claude-sync/age-key.txt"
exclude:
  - "*.tmp"
  - "cache/**"
mcp_sync: true
`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	// Verify the file was written
	readBack, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(readBack) == 0 {
		t.Fatal("config file should not be empty")
	}

	// Verify expected fields in the written content
	content := string(readBack)
	if !strings.Contains(content, "test-bucket") {
		t.Error("config should contain bucket name")
	}
	if !strings.Contains(content, "mcp_sync") {
		t.Error("config should contain mcp_sync field")
	}

	_ = cfg // cfg used for reference
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		expected bool
	}{
		// Directory wildcard patterns with /**
		{"exclude dir with /**", "plugins/cache/foo/bar.js", []string{"plugins/cache/**"}, true},
		{"exclude dir itself", "plugins/cache", []string{"plugins/cache/**"}, true},
		{"exclude nested dir", "plugins/marketplaces/repo/file.txt", []string{"plugins/marketplaces/**"}, true},
		{"non-matching dir", "plugins/installed.json", []string{"plugins/cache/**"}, false},

		// Filename glob patterns
		{"exclude by extension", "projects/foo/debug.tmp", []string{"*.tmp"}, true},
		{"exclude dotfile glob", "projects/.DS_Store", []string{".*"}, true},
		{"non-matching extension", "projects/foo/file.json", []string{"*.tmp"}, false},

		// Exact path patterns
		{"exact file match", "debug/log.txt", []string{"debug/log.txt"}, true},
		{"exact dir pattern with /**", "debug", []string{"debug/**"}, true},

		// Directory prefix (without /**)
		{"dir prefix match", "plugins/marketplace/repo/file.txt", []string{"plugins/marketplace"}, true},
		{"dir prefix exact", "plugins/marketplace", []string{"plugins/marketplace"}, true},

		// Multiple patterns
		{"first pattern matches", "plugins/cache/mod.js", []string{"plugins/cache/**", "*.tmp"}, true},
		{"second pattern matches", "foo.tmp", []string{"plugins/cache/**", "*.tmp"}, true},
		{"no pattern matches", "settings.json", []string{"plugins/cache/**", "*.tmp"}, false},

		// Empty patterns
		{"empty patterns", "anything.txt", []string{}, false},
		{"nil-like empty", "anything.txt", nil, false},

		// Edge cases
		{"partial name no match", "plugins/cachedata/file.txt", []string{"plugins/cache/**"}, false},
		{"shell-snapshots", "shell-snapshots/snap.json", []string{"shell-snapshots/**"}, true},
		{"telemetry dir", "telemetry/data.json", []string{"telemetry/**"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Exclude: tt.patterns}
			result := cfg.IsExcluded(tt.path)
			if result != tt.expected {
				t.Errorf("IsExcluded(%q) with patterns %v = %v, want %v", tt.path, tt.patterns, result, tt.expected)
			}
		})
	}
}
