package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// --- Task 3: Push tests ---

func TestSyncerPush_NewFiles(t *testing.T) {
	syncer, store, claudeDir := testSyncer(t)
	ctx := context.Background()

	createTestFile(t, claudeDir, "CLAUDE.md", "# My Settings")
	createTestFile(t, claudeDir, "settings.json", `{"theme":"dark"}`)

	result, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if len(result.Uploaded) != 2 {
		t.Errorf("Expected 2 uploads, got %d: %v", len(result.Uploaded), result.Uploaded)
	}

	// Verify files exist in mock storage
	objs, _ := store.List(ctx, "")
	if len(objs) != 2 {
		t.Errorf("Expected 2 objects in storage, got %d", len(objs))
	}
}

func TestSyncerPush_NoChanges(t *testing.T) {
	syncer, _, claudeDir := testSyncer(t)
	ctx := context.Background()

	createTestFile(t, claudeDir, "CLAUDE.md", "# My Settings")

	// First push
	_, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("First push failed: %v", err)
	}

	// Second push — no changes
	result, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Second push failed: %v", err)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("Expected 0 uploads on second push, got %d: %v", len(result.Uploaded), result.Uploaded)
	}
}

// --- Task 4: Pull tests ---

func TestSyncerPull_DownloadsNewFiles(t *testing.T) {
	// Create syncer1 and syncer2 sharing the same mock storage and encryptor
	syncer1, store, claudeDir1 := testSyncer(t)
	ctx := context.Background()

	// Create a file on syncer1 and push
	createTestFile(t, claudeDir1, "CLAUDE.md", "# Shared Settings")
	_, err := syncer1.Push(ctx)
	if err != nil {
		t.Fatalf("Push from syncer1 failed: %v", err)
	}

	// Create syncer2 sharing the same storage and encryptor
	tmpDir2 := t.TempDir()
	claudeDir2 := filepath.Join(tmpDir2, ".claude")
	stateDir2 := filepath.Join(tmpDir2, ".claude-sync")
	if err := os.MkdirAll(claudeDir2, 0755); err != nil {
		t.Fatalf("Failed to create claudeDir2: %v", err)
	}
	if err := os.MkdirAll(stateDir2, 0700); err != nil {
		t.Fatalf("Failed to create stateDir2: %v", err)
	}
	state2, err := LoadStateFromDir(stateDir2)
	if err != nil {
		t.Fatalf("Failed to load state2: %v", err)
	}
	syncer2 := NewSyncerWith(&config.Config{}, store, syncer1.encryptor, state2, claudeDir2, true)

	// Pull from syncer2
	result, err := syncer2.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull from syncer2 failed: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %d: %v", len(result.Downloaded), result.Downloaded)
	}

	// Verify the file was downloaded
	data, err := os.ReadFile(filepath.Join(claudeDir2, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}
	if string(data) != "# Shared Settings" {
		t.Errorf("Downloaded content mismatch: got %q", string(data))
	}
}

// --- Task 5: Status/State tests ---

func TestSyncerStatus_DetectsNewFiles(t *testing.T) {
	syncer, _, claudeDir := testSyncer(t)
	ctx := context.Background()

	createTestFile(t, claudeDir, "CLAUDE.md", "# New file")

	changes, err := syncer.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != "add" {
		t.Errorf("Expected action 'add', got %q", changes[0].Action)
	}
}

func TestSyncerStatus_NoChangesAfterPush(t *testing.T) {
	syncer, _, claudeDir := testSyncer(t)
	ctx := context.Background()

	createTestFile(t, claudeDir, "CLAUDE.md", "# Settings")

	_, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	changes, err := syncer.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("Expected 0 changes after push, got %d", len(changes))
	}
}

func TestSyncerGetState(t *testing.T) {
	syncer, _, _ := testSyncer(t)
	state := syncer.GetState()
	if state == nil {
		t.Fatal("Expected non-nil state from GetState")
	}
}

func TestSyncerHasState(t *testing.T) {
	syncer, _, claudeDir := testSyncer(t)
	ctx := context.Background()

	// New syncer should not have state
	if syncer.HasState() {
		t.Error("Expected HasState to be false for new syncer")
	}

	// After push, should have state
	createTestFile(t, claudeDir, "CLAUDE.md", "# Settings")
	_, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if !syncer.HasState() {
		t.Error("Expected HasState to be true after push")
	}
}

func TestSyncerSetProgressFunc(t *testing.T) {
	syncer, _, claudeDir := testSyncer(t)
	ctx := context.Background()

	var called atomic.Int32
	syncer.SetProgressFunc(func(event ProgressEvent) {
		called.Add(1)
	})

	createTestFile(t, claudeDir, "CLAUDE.md", "# Settings")
	_, err := syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if called.Load() == 0 {
		t.Error("Expected progress function to be called at least once")
	}
}

// --- Task 6: MCP tests ---

func TestSyncerPushMCP_NoServers(t *testing.T) {
	syncer, _, _ := testSyncer(t)
	ctx := context.Background()

	// Point claudeJSONPath to a non-existent file
	tmpDir := t.TempDir()
	syncer.cfg.ClaudeJSONOverride = filepath.Join(tmpDir, "claude.json")

	result, err := syncer.PushMCP(ctx)
	if err != nil {
		t.Fatalf("PushMCP failed: %v", err)
	}
	if !result.Unchanged {
		t.Error("Expected Unchanged to be true when no servers exist")
	}
}

func TestSyncerPushMCP_WithServers(t *testing.T) {
	syncer, _, _ := testSyncer(t)
	ctx := context.Background()

	// Override claudeJSONPath to a temp location
	tmpDir := t.TempDir()
	syncer.cfg.ClaudeJSONOverride = filepath.Join(tmpDir, "claude.json")

	// Write claude.json with mcpServers
	claudeJSON := syncer.claudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	content := `{"mcpServers":{"test-server":{"command":"node","args":["server.js"]}}}`
	if err := os.WriteFile(claudeJSON, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write claude.json: %v", err)
	}

	result, err := syncer.PushMCP(ctx)
	if err != nil {
		t.Fatalf("PushMCP failed: %v", err)
	}
	if result.ServersPushed != 1 {
		t.Errorf("Expected ServersPushed=1, got %d", result.ServersPushed)
	}
}

func TestSyncerPullMCP_NoRemote(t *testing.T) {
	syncer, _, _ := testSyncer(t)
	ctx := context.Background()

	// Point claudeJSONPath to a temp location
	tmpDir := t.TempDir()
	syncer.cfg.ClaudeJSONOverride = filepath.Join(tmpDir, "claude.json")

	result, err := syncer.PullMCP(ctx)
	if err != nil {
		t.Fatalf("PullMCP failed: %v", err)
	}
	if !result.NoRemote {
		t.Error("Expected NoRemote to be true when no remote data exists")
	}
}

func TestSyncerPullMCP_RoundTrip(t *testing.T) {
	// syncer1 pushes MCP, syncer2 pulls it
	syncer1, store, _ := testSyncer(t)
	ctx := context.Background()

	// Set up syncer1's claude.json
	tmpDir1 := t.TempDir()
	claudeJSON1 := filepath.Join(tmpDir1, "claude.json")
	syncer1.cfg.ClaudeJSONOverride = claudeJSON1
	content := `{"mcpServers":{"my-server":{"command":"python","args":["serve.py"]}}}`
	if err := os.WriteFile(claudeJSON1, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write claude.json: %v", err)
	}

	// Push from syncer1
	_, err := syncer1.PushMCP(ctx)
	if err != nil {
		t.Fatalf("PushMCP from syncer1 failed: %v", err)
	}

	// Create syncer2 sharing the same storage and encryptor
	tmpDir2 := t.TempDir()
	stateDir2 := filepath.Join(tmpDir2, ".claude-sync")
	claudeDir2 := filepath.Join(tmpDir2, ".claude")
	if err := os.MkdirAll(stateDir2, 0700); err != nil {
		t.Fatalf("Failed to create stateDir2: %v", err)
	}
	if err := os.MkdirAll(claudeDir2, 0755); err != nil {
		t.Fatalf("Failed to create claudeDir2: %v", err)
	}
	state2, _ := LoadStateFromDir(stateDir2)
	syncer2 := NewSyncerWith(&config.Config{}, store, syncer1.encryptor, state2, claudeDir2, true)
	claudeJSON2 := filepath.Join(tmpDir2, "claude.json")
	syncer2.cfg.ClaudeJSONOverride = claudeJSON2

	// Pull from syncer2
	result, err := syncer2.PullMCP(ctx)
	if err != nil {
		t.Fatalf("PullMCP from syncer2 failed: %v", err)
	}

	// Verify "my-server" was added
	found := false
	for _, name := range result.Added {
		if name == "my-server" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'my-server' in Added, got %v", result.Added)
	}

	// Verify claude.json was written with the server
	data, err := os.ReadFile(claudeJSON2)
	if err != nil {
		t.Fatalf("Failed to read syncer2 claude.json: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse syncer2 claude.json: %v", err)
	}
	if _, ok := parsed["mcpServers"]; !ok {
		t.Error("Expected mcpServers key in syncer2 claude.json")
	}
}

func TestSyncerMCPStatus_NoServers(t *testing.T) {
	syncer, _, _ := testSyncer(t)
	ctx := context.Background()

	// Point to non-existent claude.json
	tmpDir := t.TempDir()
	syncer.cfg.ClaudeJSONOverride = filepath.Join(tmpDir, "claude.json")

	result, err := syncer.MCPStatus(ctx)
	if err != nil {
		t.Fatalf("MCPStatus failed: %v", err)
	}
	if result.ServerCount != 0 {
		t.Errorf("Expected ServerCount=0, got %d", result.ServerCount)
	}
}

// --- Task 7: Gzip tests ---

func TestGzipRoundTrip(t *testing.T) {
	original := []byte("Hello, this is test data for gzip compression round-trip!")

	compressed, err := gzipCompress(original)
	if err != nil {
		t.Fatalf("gzipCompress failed: %v", err)
	}

	if !isGzipped(compressed) {
		t.Error("Expected compressed data to be detected as gzipped")
	}

	decompressed, err := gzipDecompress(compressed)
	if err != nil {
		t.Fatalf("gzipDecompress failed: %v", err)
	}

	if string(decompressed) != string(original) {
		t.Errorf("Round-trip mismatch: got %q, want %q", string(decompressed), string(original))
	}
}

func TestIsGzipped(t *testing.T) {
	if isGzipped(nil) {
		t.Error("nil should not be detected as gzipped")
	}
	if isGzipped([]byte{0x00}) {
		t.Error("single byte should not be detected as gzipped")
	}
	if isGzipped([]byte("plain text data")) {
		t.Error("plain text should not be detected as gzipped")
	}
	if !isGzipped([]byte{0x1f, 0x8b, 0x08}) {
		t.Error("gzip magic bytes should be detected as gzipped")
	}
}

func TestGzipDecompress_RejectsOversizedData(t *testing.T) {
	// Create data that's all zeros (compresses very well) — 101 MB exceeds 100 MB limit
	bigData := make([]byte, 101*1024*1024)
	compressed, err := gzipCompress(bigData)
	if err != nil {
		t.Fatalf("Failed to compress test data: %v", err)
	}

	_, err = gzipDecompress(compressed)
	if err == nil {
		t.Fatal("Expected error for oversized decompressed data, got nil")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("Expected 'maximum size' error, got: %v", err)
	}
}

func TestGzipDecompress_AcceptsNormalData(t *testing.T) {
	original := []byte("Normal sized data for testing")
	compressed, err := gzipCompress(original)
	if err != nil {
		t.Fatalf("Failed to compress: %v", err)
	}

	result, err := gzipDecompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if string(result) != string(original) {
		t.Errorf("Mismatch: got %q, want %q", result, original)
	}
}

func TestUpdateFileAndMarkUploaded_IsAtomic(t *testing.T) {
	state := NewState()
	tmpDir := t.TempDir()

	// Create a test file to get FileInfo
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat test file: %v", err)
	}

	state.UpdateFileAndMarkUploaded("test.txt", info, "abc123")

	f := state.GetFile("test.txt")
	if f == nil {
		t.Fatal("Expected file state to exist")
	}
	if f.Hash != "abc123" {
		t.Errorf("Expected hash 'abc123', got %q", f.Hash)
	}
	if f.Uploaded.IsZero() {
		t.Error("Expected Uploaded to be set")
	}
	if f.Size != info.Size() {
		t.Errorf("Expected size %d, got %d", info.Size(), f.Size)
	}
}

func TestPullDownloadsWithRestrictivePermissions(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Upload encrypted content to mock storage
	content := []byte("# Sensitive Settings")
	encrypted, err := env.syncer.encryptor.Encrypt(content)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if err := env.store.Upload(ctx, "settings.json.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull
	_, err = env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}

	// Check file permissions
	filePath := filepath.Join(env.claudeDir, "settings.json")
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("Expected file permission 0600, got %04o", perm)
	}
}
