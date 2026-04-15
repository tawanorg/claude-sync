package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"
)

// mockStorage implements storage.Storage in-memory for testing.
type mockStorage struct {
	mu      sync.Mutex
	objects map[string]mockObject
}

type mockObject struct {
	data         []byte
	lastModified time.Time
}

func newMockStorage() *mockStorage {
	return &mockStorage{objects: make(map[string]mockObject)}
}

func (m *mockStorage) Upload(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objects[key] = mockObject{data: cp, lastModified: time.Now()}
	return nil
}

func (m *mockStorage) Download(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	cp := make([]byte, len(obj.data))
	copy(cp, obj.data)
	return cp, nil
}

func (m *mockStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *mockStorage) DeleteBatch(_ context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.objects, k)
	}
	return nil
}

func (m *mockStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []storage.ObjectInfo
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) {
			result = append(result, storage.ObjectInfo{
				Key:          key,
				Size:         int64(len(obj.data)),
				LastModified: obj.lastModified,
			})
		}
	}
	return result, nil
}

func (m *mockStorage) Head(_ context.Context, key string) (*storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return &storage.ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		LastModified: obj.lastModified,
	}, nil
}

func (m *mockStorage) BucketExists(_ context.Context) (bool, error) {
	return true, nil
}

// helper to create a test syncer with mock storage and temp dirs
type testEnv struct {
	syncer    *Syncer
	store     *mockStorage
	claudeDir string
	stateDir  string
}

func setupTestEnv(t *testing.T) *testEnv {
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

	// Generate encryption key
	keyPath := filepath.Join(stateDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "test-passphrase"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	store := newMockStorage()
	syncer := &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     true,
		cfg:       &config.Config{},
	}

	return &testEnv{
		syncer:    syncer,
		store:     store,
		claudeDir: claudeDir,
		stateDir:  stateDir,
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", name, err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("Failed to read %s: %v", name, err)
	}
	return string(data)
}

func TestPushUploadsNewFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# My Settings")
	writeFile(t, env.claudeDir, "settings.json", `{"theme":"dark"}`)

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if len(result.Uploaded) != 2 {
		t.Errorf("Expected 2 uploads, got %d: %v", len(result.Uploaded), result.Uploaded)
	}
	if len(result.Errors) > 0 {
		t.Errorf("Unexpected errors: %v", result.Errors)
	}

	// Verify files exist in mock storage with .age suffix
	objs, _ := env.store.List(ctx, "")
	if len(objs) != 2 {
		t.Errorf("Expected 2 objects in storage, got %d", len(objs))
	}
	for _, obj := range objs {
		if !strings.HasSuffix(obj.Key, ".age") {
			t.Errorf("Expected .age suffix on key %s", obj.Key)
		}
	}
}

func TestPushUploadsModifiedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# V1")

	// Initial push
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Initial push failed: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Fatalf("Expected 1 upload, got %d", len(result.Uploaded))
	}

	// Modify the file
	writeFile(t, env.claudeDir, "CLAUDE.md", "# V2 - modified")

	// Second push
	result, err = env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Second push failed: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Errorf("Expected 1 modified upload, got %d", len(result.Uploaded))
	}
}

func TestPushDeletesRemovedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# Settings")

	// Push
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Delete local file
	os.Remove(filepath.Join(env.claudeDir, "CLAUDE.md"))

	// Push again
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Delete push failed: %v", err)
	}
	if len(result.Deleted) != 1 {
		t.Errorf("Expected 1 delete, got %d", len(result.Deleted))
	}

	// Verify removed from storage
	objs, _ := env.store.List(ctx, "")
	if len(objs) != 0 {
		t.Errorf("Expected 0 objects in storage after delete, got %d", len(objs))
	}
}

func TestPullDownloadsNewRemoteFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Simulate remote files by encrypting and uploading directly to mock storage
	content := []byte("# Remote Settings")
	encrypted, err := env.syncer.encryptor.Encrypt(content)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if err := env.store.Upload(ctx, "CLAUDE.md.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %d: %v", len(result.Downloaded), result.Downloaded)
	}

	// Verify local file
	got := readFile(t, env.claudeDir, "CLAUDE.md")
	if got != "# Remote Settings" {
		t.Errorf("Expected '# Remote Settings', got %q", got)
	}
}

func TestPullSkipsUnchangedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# Synced")

	// Push to establish state
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Pull — nothing should be downloaded since remote hasn't changed beyond our push
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("Expected 0 downloads (unchanged), got %d: %v", len(result.Downloaded), result.Downloaded)
	}
}

func TestPullDetectsConflicts(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "history.jsonl", `{"event":"local-v1"}`)

	// Push to establish baseline
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Modify local file (simulating local changes)
	writeFile(t, env.claudeDir, "history.jsonl", `{"event":"local-v2"}`)

	// Modify remote file (simulating another device pushing)
	remoteContent := []byte(`{"event":"remote-v2"}`)
	encrypted, err := env.syncer.encryptor.Encrypt(remoteContent)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	// Small delay to ensure remote timestamp is after the state's Uploaded time
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "history.jsonl.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull — should detect conflict
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("Expected 1 conflict, got %d: %v", len(result.Conflicts), result.Conflicts)
	}

	// Local file should be preserved
	got := readFile(t, env.claudeDir, "history.jsonl")
	if got != `{"event":"local-v2"}` {
		t.Errorf("Local file should be preserved, got %q", got)
	}

	// A .conflict file should exist
	entries, err := os.ReadDir(env.claudeDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	conflictFound := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "history.jsonl.conflict.") {
			conflictFound = true
			// Verify conflict file contains remote content
			data, _ := os.ReadFile(filepath.Join(env.claudeDir, e.Name()))
			if string(data) != `{"event":"remote-v2"}` {
				t.Errorf("Conflict file should contain remote content, got %q", string(data))
			}
		}
	}
	if !conflictFound {
		t.Error("Expected a .conflict file to be created")
	}
}

func TestNoConflictWhenOnlyRemoteChanged(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# V1")

	// Push to establish baseline
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Remote changes (another device pushes a new version)
	remoteContent := []byte("# V2 from other device")
	encrypted, err := env.syncer.encryptor.Encrypt(remoteContent)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "CLAUDE.md.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Local file NOT modified (hash matches state)
	// Pull — should download without conflict
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("Expected 0 conflicts, got %d", len(result.Conflicts))
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %d", len(result.Downloaded))
	}

	got := readFile(t, env.claudeDir, "CLAUDE.md")
	if got != "# V2 from other device" {
		t.Errorf("Expected remote content, got %q", got)
	}
}

func TestPushThenPullRoundTrip(t *testing.T) {
	// Device A pushes, Device B (fresh) pulls — content should match
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "shared-state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	// Shared encryption key
	keyPath := filepath.Join(stateDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "round-trip-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	sharedStore := newMockStorage()

	// Device A setup
	deviceADir := filepath.Join(tmpDir, "deviceA", ".claude")
	deviceAStateDir := filepath.Join(tmpDir, "deviceA", ".claude-sync")
	if err := os.MkdirAll(deviceADir, 0755); err != nil {
		t.Fatalf("Failed to create deviceA claude dir: %v", err)
	}
	if err := os.MkdirAll(deviceAStateDir, 0700); err != nil {
		t.Fatalf("Failed to create deviceA state dir: %v", err)
	}

	stateA, _ := LoadStateFromDir(deviceAStateDir)
	syncerA := &Syncer{
		storage:   sharedStore,
		encryptor: enc,
		state:     stateA,
		claudeDir: deviceADir,
		quiet:     true,
		cfg:       &config.Config{},
	}

	// Device A creates files and pushes
	writeFile(t, deviceADir, "CLAUDE.md", "# Shared config")
	writeFile(t, deviceADir, "settings.json", `{"theme":"dark","fontSize":14}`)
	writeFile(t, deviceADir, "agents/helper.json", `{"name":"helper","model":"opus"}`)

	ctx := context.Background()
	resultA, err := syncerA.Push(ctx)
	if err != nil {
		t.Fatalf("Device A push failed: %v", err)
	}
	if len(resultA.Uploaded) != 3 {
		t.Fatalf("Device A expected 3 uploads, got %d", len(resultA.Uploaded))
	}

	// Device B setup (fresh, no local files)
	deviceBDir := filepath.Join(tmpDir, "deviceB", ".claude")
	deviceBStateDir := filepath.Join(tmpDir, "deviceB", ".claude-sync")
	if err := os.MkdirAll(deviceBDir, 0755); err != nil {
		t.Fatalf("Failed to create deviceB claude dir: %v", err)
	}
	if err := os.MkdirAll(deviceBStateDir, 0700); err != nil {
		t.Fatalf("Failed to create deviceB state dir: %v", err)
	}

	stateB, _ := LoadStateFromDir(deviceBStateDir)
	syncerB := &Syncer{
		storage:   sharedStore,
		encryptor: enc,
		state:     stateB,
		claudeDir: deviceBDir,
		quiet:     true,
		cfg:       &config.Config{},
	}

	// Device B pulls
	resultB, err := syncerB.Pull(ctx)
	if err != nil {
		t.Fatalf("Device B pull failed: %v", err)
	}
	if len(resultB.Downloaded) != 3 {
		t.Errorf("Device B expected 3 downloads, got %d: %v", len(resultB.Downloaded), resultB.Downloaded)
	}

	// Verify content matches
	if got := readFile(t, deviceBDir, "CLAUDE.md"); got != "# Shared config" {
		t.Errorf("CLAUDE.md mismatch: %q", got)
	}
	if got := readFile(t, deviceBDir, "settings.json"); got != `{"theme":"dark","fontSize":14}` {
		t.Errorf("settings.json mismatch: %q", got)
	}
	if got := readFile(t, deviceBDir, "agents/helper.json"); got != `{"name":"helper","model":"opus"}` {
		t.Errorf("agents/helper.json mismatch: %q", got)
	}
}

func TestConflictCreatesConflictFile(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Push initial version of history.jsonl
	writeFile(t, env.claudeDir, "history.jsonl", "line1\n")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Local appends
	writeFile(t, env.claudeDir, "history.jsonl", "line1\nline2-local\n")

	// Remote also changed
	remoteData := []byte("line1\nline2-remote\n")
	encrypted, _ := env.syncer.encryptor.Encrypt(remoteData)
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "history.jsonl.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}

	// Should have conflict
	if len(result.Conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(result.Conflicts))
	}
	if result.Conflicts[0] != "history.jsonl" {
		t.Errorf("Expected conflict on history.jsonl, got %s", result.Conflicts[0])
	}

	// Local preserved
	local := readFile(t, env.claudeDir, "history.jsonl")
	if local != "line1\nline2-local\n" {
		t.Errorf("Local should be preserved, got %q", local)
	}

	// Conflict file has remote content
	entries, _ := os.ReadDir(env.claudeDir)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "history.jsonl.conflict.") {
			found = true
			data, _ := os.ReadFile(filepath.Join(env.claudeDir, e.Name()))
			if string(data) != "line1\nline2-remote\n" {
				t.Errorf("Conflict file content mismatch: %q", string(data))
			}
		}
	}
	if !found {
		t.Error("No .conflict file created")
	}
}

func TestPushNoChangesIsNoop(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Push with no files — should be a no-op
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("Expected 0 uploads, got %d", len(result.Uploaded))
	}
	if len(result.Deleted) != 0 {
		t.Errorf("Expected 0 deletes, got %d", len(result.Deleted))
	}
}

func TestPullEmptyRemoteIsNoop(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Pull with nothing in remote
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("Expected 0 downloads, got %d", len(result.Downloaded))
	}
}

// TestPullSetsRestrictivePermissions verifies that files created by a pull are
// 0600 and directories created by a pull are 0700. ~/.claude can contain API
// keys, prompts, and personal context, so it must not be world-readable.
//
// Uses a nested remote path (agents/helper.json) so the pull actually has to
// create the parent directory — os.MkdirAll does not modify the mode of
// pre-existing directories, and env.claudeDir was created at 0755 by setup.
func TestPullSetsRestrictivePermissions(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	content := []byte(`{"name":"helper","model":"opus"}`)
	encrypted, err := env.syncer.encryptor.Encrypt(content)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if err := env.store.Upload(ctx, "agents/helper.json.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	if _, err := env.syncer.Pull(ctx); err != nil {
		t.Fatalf("Pull failed: %v", err)
	}

	// File created by pull must be user-only readable/writable.
	filePath := filepath.Join(env.claudeDir, "agents/helper.json")
	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat file failed: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("Expected file mode 0600, got %o", got)
	}

	// Directory created by pull must be user-only.
	dirPath := filepath.Join(env.claudeDir, "agents")
	di, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("Stat dir failed: %v", err)
	}
	if got := di.Mode().Perm(); got != 0700 {
		t.Errorf("Expected dir mode 0700, got %o", got)
	}
}
