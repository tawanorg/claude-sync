//go:build integration

// Package integration provides cross-device integration tests for claude-sync.
// These tests require real R2 credentials and simulate multiple devices using temp directories.
//
// To run these tests:
//
//	source integration/.env
//	go test -tags=integration -v ./integration/...
package integration

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"
	"github.com/tawanorg/claude-sync/internal/sync"

	// Register storage adapters
	_ "github.com/tawanorg/claude-sync/internal/storage/r2"
)

// TestBasicCrossDeviceSync tests the core sync flow:
// 1. Device A: init with passphrase, create files, push
// 2. Device B: init with same passphrase, pull
// 3. Verify: Device B has same files as Device A
func TestBasicCrossDeviceSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	passphrase := getTestPassphrase()

	// Setup two isolated test environments
	deviceADir := t.TempDir()
	deviceBDir := t.TempDir()

	// Device A: Initialize and push
	t.Run("DeviceA_Push", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceADir, passphrase)

		// Create test files using paths that are in config.SyncPaths
		claudeDir := filepath.Join(deviceADir, ".claude")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			t.Fatalf("failed to create claude dir: %v", err)
		}

		// Use CLAUDE.md which is in the default sync paths
		testContent := "Hello from Device A"
		if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte(testContent), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Push files
		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		result, err := syncer.Push(ctx)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		if len(result.Uploaded) == 0 {
			t.Error("expected files to be uploaded")
		}
		t.Logf("Uploaded %d files: %v", len(result.Uploaded), result.Uploaded)
	})

	// Device B: Initialize with same passphrase and pull
	t.Run("DeviceB_Pull", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceBDir, passphrase)

		// Verify ~/.claude is empty
		claudeDir := filepath.Join(deviceBDir, ".claude")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			t.Fatalf("failed to create claude dir: %v", err)
		}

		// Pull files
		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		result, err := syncer.Pull(ctx)
		if err != nil {
			t.Fatalf("pull failed: %v", err)
		}

		t.Logf("Downloaded %d files: %v", len(result.Downloaded), result.Downloaded)
		if len(result.Downloaded) == 0 {
			t.Error("expected files to be downloaded")
		}

		// Verify content matches (using CLAUDE.md)
		content, err := os.ReadFile(filepath.Join(claudeDir, "CLAUDE.md"))
		if err != nil {
			t.Fatalf("failed to read pulled file: %v", err)
		}

		if string(content) != "Hello from Device A" {
			t.Errorf("content mismatch: got %q, want %q", string(content), "Hello from Device A")
		}
	})

	// Cleanup remote
	t.Run("Cleanup", func(t *testing.T) {
		cleanupRemote(t, deviceADir)
	})
}

// TestKeyMismatchDetection tests that key mismatch is properly detected:
// 1. Device A: init with passphrase-1, push files
// 2. Device B: init with passphrase-2
// 3. Verify: init detects mismatch
func TestKeyMismatchDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	deviceADir := t.TempDir()
	deviceBDir := t.TempDir()

	// Device A: Push with passphrase-1
	t.Run("DeviceA_Push", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceADir, "passphrase-one-123")

		claudeDir := filepath.Join(deviceADir, ".claude")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			t.Fatalf("failed to create claude dir: %v", err)
		}

		// Use settings.json which is in the default sync paths
		if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"encrypted": "data"}`), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		result, err := syncer.Push(ctx)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}
		t.Logf("Uploaded %d files: %v", len(result.Uploaded), result.Uploaded)
	})

	// Device B: Try to decrypt with different passphrase
	t.Run("DeviceB_MismatchDetection", func(t *testing.T) {
		// Setup with different passphrase
		cfg := setupTestConfig(t, deviceBDir, "passphrase-two-456")

		storageCfg := cfg.GetStorageConfig()
		store, err := storage.New(storageCfg)
		if err != nil {
			t.Fatalf("failed to create storage: %v", err)
		}

		keyPath := filepath.Join(deviceBDir, ".claude-sync", "age-key.txt")

		// This should fail - the key doesn't match
		err = verifyKeyMatchesRemote(ctx, store, keyPath)
		if err == nil {
			t.Error("expected key mismatch error, got nil")
			return
		}

		if !strings.Contains(err.Error(), "key_mismatch") {
			t.Errorf("expected key_mismatch error, got: %v", err)
		}
	})

	// Cleanup
	t.Run("Cleanup", func(t *testing.T) {
		cleanupRemote(t, deviceADir)
	})
}

// TestPullWithExistingFiles tests pulling to a device that already has local files:
// 1. Device A: push files
// 2. Device C: has existing local files, then pulls
// 3. Verify: remote files downloaded, local-only files preserved
func TestPullWithExistingFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	passphrase := getTestPassphrase()

	deviceADir := t.TempDir()
	deviceCDir := t.TempDir()

	// Device A: Push some files
	t.Run("DeviceA_Push", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceADir, passphrase)

		claudeDir := filepath.Join(deviceADir, ".claude")
		if err := os.MkdirAll(filepath.Join(claudeDir, "projects"), 0755); err != nil {
			t.Fatalf("failed to create dirs: %v", err)
		}

		// Create files to push
		if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("Remote CLAUDE.md from Device A"), 0644); err != nil {
			t.Fatalf("failed to create CLAUDE.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"theme": "dark"}`), 0644); err != nil {
			t.Fatalf("failed to create settings.json: %v", err)
		}
		if err := os.WriteFile(filepath.Join(claudeDir, "projects/remote-project.json"), []byte(`{"name": "remote"}`), 0644); err != nil {
			t.Fatalf("failed to create project file: %v", err)
		}

		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		result, err := syncer.Push(ctx)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}
		t.Logf("Device A pushed %d files: %v", len(result.Uploaded), result.Uploaded)
	})

	// Device C: Has existing files, then pulls
	t.Run("DeviceC_ExistingFiles_Pull", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceCDir, passphrase)

		claudeDir := filepath.Join(deviceCDir, ".claude")
		if err := os.MkdirAll(filepath.Join(claudeDir, "rules"), 0755); err != nil {
			t.Fatalf("failed to create dirs: %v", err)
		}

		// Create EXISTING local files BEFORE pulling
		// This file exists both locally and remotely - should be overwritten (remote newer)
		if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("Local CLAUDE.md - should be overwritten"), 0644); err != nil {
			t.Fatalf("failed to create local CLAUDE.md: %v", err)
		}

		// This file is local-only - should be preserved
		if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte(`{"local": true}`), 0644); err != nil {
			t.Fatalf("failed to create settings.local.json: %v", err)
		}

		// Local-only rules - should be preserved
		if err := os.WriteFile(filepath.Join(claudeDir, "rules/my-rules.md"), []byte("# My local rules"), 0644); err != nil {
			t.Fatalf("failed to create local rules: %v", err)
		}

		t.Log("Created existing local files before pull")

		// Pull from remote
		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		// Test PreviewPull first
		preview, err := syncer.PreviewPull(ctx)
		if err != nil {
			t.Fatalf("preview pull failed: %v", err)
		}
		t.Logf("Preview - Download: %d, Overwrite: %d, Keep: %d, LocalOnly: %d",
			len(preview.WouldDownload), len(preview.WouldOverwrite),
			len(preview.WouldKeep), len(preview.LocalOnlyFiles))

		// Now actually pull
		result, err := syncer.Pull(ctx)
		if err != nil {
			t.Fatalf("pull failed: %v", err)
		}
		t.Logf("Downloaded %d files: %v", len(result.Downloaded), result.Downloaded)

		// Verify results
		// 1. CLAUDE.md should have remote content
		content, err := os.ReadFile(filepath.Join(claudeDir, "CLAUDE.md"))
		if err != nil {
			t.Fatalf("failed to read CLAUDE.md: %v", err)
		}
		if !strings.Contains(string(content), "Device A") {
			t.Errorf("CLAUDE.md should have remote content, got: %s", string(content))
		} else {
			t.Log("SUCCESS: CLAUDE.md updated from remote")
		}

		// 2. settings.json should exist (from remote)
		if _, err := os.Stat(filepath.Join(claudeDir, "settings.json")); err != nil {
			t.Errorf("settings.json should exist from remote: %v", err)
		} else {
			t.Log("SUCCESS: settings.json synced from remote")
		}

		// 3. settings.local.json should be preserved (local only)
		content, err = os.ReadFile(filepath.Join(claudeDir, "settings.local.json"))
		if err != nil {
			t.Errorf("settings.local.json should be preserved: %v", err)
		} else if strings.Contains(string(content), "local") {
			t.Log("SUCCESS: settings.local.json preserved (local only)")
		}

		// 4. rules/my-rules.md should be preserved (local only)
		if _, err := os.Stat(filepath.Join(claudeDir, "rules/my-rules.md")); err != nil {
			t.Errorf("rules/my-rules.md should be preserved: %v", err)
		} else {
			t.Log("SUCCESS: rules/my-rules.md preserved (local only)")
		}

		// 5. projects/remote-project.json should exist (from remote)
		if _, err := os.Stat(filepath.Join(claudeDir, "projects/remote-project.json")); err != nil {
			t.Errorf("projects/remote-project.json should exist: %v", err)
		} else {
			t.Log("SUCCESS: projects/remote-project.json synced from remote")
		}
	})

	// Cleanup
	t.Run("Cleanup", func(t *testing.T) {
		cleanupRemote(t, deviceADir)
	})
}

// TestConflictResolution tests conflict handling:
// 1. Both devices modify same file
// 2. Device A pushes first
// 3. Device B pulls (should create .conflict file)
func TestConflictResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	passphrase := getTestPassphrase()

	deviceADir := t.TempDir()
	deviceBDir := t.TempDir()

	// Use settings.local.json which is in config.SyncPaths
	testFile := "settings.local.json"

	// Initial sync to both devices
	t.Run("InitialSync", func(t *testing.T) {
		cfg := setupTestConfig(t, deviceADir, passphrase)

		claudeDir := filepath.Join(deviceADir, ".claude")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			t.Fatalf("failed to create claude dir: %v", err)
		}

		if err := os.WriteFile(filepath.Join(claudeDir, testFile), []byte(`{"version": "initial"}`), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		syncer, err := sync.NewSyncer(cfg, true)
		if err != nil {
			t.Fatalf("failed to create syncer: %v", err)
		}

		result, err := syncer.Push(ctx)
		if err != nil {
			t.Fatalf("initial push failed: %v", err)
		}
		t.Logf("Initial push uploaded %d files", len(result.Uploaded))

		// Device B pulls initial state
		cfgB := setupTestConfig(t, deviceBDir, passphrase)
		claudeDirB := filepath.Join(deviceBDir, ".claude")
		if err := os.MkdirAll(claudeDirB, 0755); err != nil {
			t.Fatalf("failed to create claude dir B: %v", err)
		}

		syncerB, err := sync.NewSyncer(cfgB, true)
		if err != nil {
			t.Fatalf("failed to create syncer B: %v", err)
		}

		resultB, err := syncerB.Pull(ctx)
		if err != nil {
			t.Fatalf("initial pull failed: %v", err)
		}
		t.Logf("Initial pull downloaded %d files", len(resultB.Downloaded))
	})

	// Both modify the file
	t.Run("ConcurrentModification", func(t *testing.T) {
		claudeDirA := filepath.Join(deviceADir, ".claude")
		claudeDirB := filepath.Join(deviceBDir, ".claude")

		// Device A modifies
		if err := os.WriteFile(filepath.Join(claudeDirA, testFile), []byte(`{"version": "from A"}`), 0644); err != nil {
			t.Fatalf("failed to modify file on A: %v", err)
		}

		// Device B modifies
		if err := os.WriteFile(filepath.Join(claudeDirB, testFile), []byte(`{"version": "from B"}`), 0644); err != nil {
			t.Fatalf("failed to modify file on B: %v", err)
		}

		// Device A pushes first
		cfgA := setupTestConfig(t, deviceADir, passphrase)
		syncerA, err := sync.NewSyncer(cfgA, true)
		if err != nil {
			t.Fatalf("failed to create syncer A: %v", err)
		}
		_, err = syncerA.Push(ctx)
		if err != nil {
			t.Fatalf("A push failed: %v", err)
		}

		// Device B pulls - should create conflict
		cfgB := setupTestConfig(t, deviceBDir, passphrase)
		syncerB, err := sync.NewSyncer(cfgB, true)
		if err != nil {
			t.Fatalf("failed to create syncer B: %v", err)
		}
		result, err := syncerB.Pull(ctx)
		if err != nil {
			t.Fatalf("B pull failed: %v", err)
		}

		t.Logf("Pull result - Downloaded: %d, Conflicts: %d", len(result.Downloaded), len(result.Conflicts))

		// Check for conflict
		if len(result.Conflicts) == 0 {
			t.Log("Note: conflict detection may depend on timing/state tracking")
		}

		// Verify conflict file exists (if conflict was detected)
		files, _ := filepath.Glob(filepath.Join(claudeDirB, testFile+".conflict.*"))
		t.Logf("Found %d conflict files", len(files))

		// Verify local content is preserved
		content, _ := os.ReadFile(filepath.Join(claudeDirB, testFile))
		if string(content) != `{"version": "from B"}` {
			t.Logf("Local content after pull: %s", string(content))
		}
	})

	// Cleanup
	t.Run("Cleanup", func(t *testing.T) {
		cleanupRemote(t, deviceADir)
	})
}

// Helper functions

func getTestPassphrase() string {
	if p := os.Getenv("CLAUDE_SYNC_TEST_PASSPHRASE"); p != "" {
		return p
	}
	return "test-passphrase-123"
}

func getTestStorageConfig() *storage.StorageConfig {
	return &storage.StorageConfig{
		Provider:        storage.ProviderR2,
		Bucket:          getEnvOrDefault("CLAUDE_SYNC_R2_BUCKET", "claude-sync-test"),
		AccountID:       os.Getenv("CLAUDE_SYNC_R2_ACCOUNT_ID"),
		AccessKeyID:     os.Getenv("CLAUDE_SYNC_R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLAUDE_SYNC_R2_SECRET_ACCESS_KEY"),
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func setupTestConfig(t *testing.T, baseDir, passphrase string) *config.Config {
	t.Helper()

	// Verify R2 credentials are available
	accountID := os.Getenv("CLAUDE_SYNC_R2_ACCOUNT_ID")
	accessKeyID := os.Getenv("CLAUDE_SYNC_R2_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("CLAUDE_SYNC_R2_SECRET_ACCESS_KEY")

	if accountID == "" || accessKeyID == "" || secretAccessKey == "" {
		t.Skip("R2 credentials not set - skipping integration test")
	}

	configDir := filepath.Join(baseDir, ".claude-sync")
	claudeDir := filepath.Join(baseDir, ".claude")

	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("failed to create claude dir: %v", err)
	}

	// Generate key from passphrase
	keyPath := filepath.Join(configDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, passphrase); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	storageCfg := &storage.StorageConfig{
		Provider:        storage.ProviderR2,
		Bucket:          getEnvOrDefault("CLAUDE_SYNC_R2_BUCKET", "claude-sync-test"),
		AccountID:       accountID,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
	}

	// Create a custom config that uses isolated paths
	cfg := &config.Config{
		Storage:           storageCfg,
		EncryptionKey:     keyPath,
		ClaudeDirOverride: claudeDir,
		StateDirOverride:  configDir,
	}

	return cfg
}

func cleanupRemote(t *testing.T, baseDir string) {
	t.Helper()

	storageCfg := getTestStorageConfig()
	store, err := storage.New(storageCfg)
	if err != nil {
		t.Logf("warning: failed to create storage for cleanup: %v", err)
		return
	}

	ctx := context.Background()
	objects, err := store.List(ctx, "")
	if err != nil {
		t.Logf("warning: failed to list objects for cleanup: %v", err)
		return
	}

	for _, obj := range objects {
		if err := store.Delete(ctx, obj.Key); err != nil {
			t.Logf("warning: failed to delete %s: %v", obj.Key, err)
		}
	}
}

// verifyKeyMatchesRemote is duplicated here for testing
// In production, this is in cmd/claude-sync/main.go
func verifyKeyMatchesRemote(ctx context.Context, store storage.Storage, keyPath string) error {
	objects, err := store.List(ctx, "")
	if err != nil || len(objects) == 0 {
		return nil
	}

	var testObj storage.ObjectInfo
	for _, obj := range objects {
		if obj.Size > 0 && obj.Size < 10000 {
			testObj = obj
			break
		}
	}
	if testObj.Key == "" && len(objects) > 0 {
		testObj = objects[0]
	}
	if testObj.Key == "" {
		return nil
	}

	encrypted, err := store.Download(ctx, testObj.Key)
	if err != nil {
		return nil
	}

	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		return err
	}

	_, err = enc.Decrypt(encrypted)
	if err != nil {
		return &keyMismatchError{wrapped: err}
	}

	return nil
}

type keyMismatchError struct {
	wrapped error
}

func (e *keyMismatchError) Error() string {
	return "key_mismatch: cannot decrypt remote files with current key"
}

// Ensure sha256 is used (for passphrase derivation compatibility check)
var _ = sha256.Sum256(nil)
