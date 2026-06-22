package claudesettings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// --- Mock Repository for Testing ---

// MockRepository implements SettingsRepository for testing.
type MockRepository struct {
	data      map[string][]byte
	readErr   error
	writeErr  error
	mkdirErr  error
	writeCalls []writeCall
}

type writeCall struct {
	path string
	data []byte
	perm os.FileMode
}

func NewMockRepository() *MockRepository {
	return &MockRepository{data: make(map[string][]byte)}
}

func (m *MockRepository) Read(path string) ([]byte, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	data, ok := m.data[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *MockRepository) Write(path string, data []byte, perm os.FileMode) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.writeCalls = append(m.writeCalls, writeCall{path, data, perm})
	m.data[path] = data
	return nil
}

func (m *MockRepository) Exists(path string) bool {
	_, ok := m.data[path]
	return ok
}

func (m *MockRepository) MkdirAll(path string, perm os.FileMode) error {
	return m.mkdirErr
}

// --- Mock CommandMatcher for Testing ---

type MockMatcher struct {
	matchFunc func(string) bool
}

func (m *MockMatcher) Matches(cmd string) bool {
	if m.matchFunc != nil {
		return m.matchFunc(cmd)
	}
	return false
}

// --- Repository Pattern Tests ---

func TestLoadWithMockRepository(t *testing.T) {
	repo := NewMockRepository()
	repo.data["/test/settings.json"] = []byte(`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "test"}]}]}}`)

	settings, err := LoadWithRepo("/test/settings.json", repo)
	if err != nil {
		t.Fatalf("LoadWithRepo failed: %v", err)
	}

	if len(settings.Hooks["SessionStart"]) != 1 {
		t.Errorf("Expected 1 SessionStart hook group, got %d", len(settings.Hooks["SessionStart"]))
	}
}

func TestLoadWithRepoError(t *testing.T) {
	repo := NewMockRepository()
	repo.readErr = errors.New("permission denied")

	_, err := LoadWithRepo("/test/settings.json", repo)
	if err == nil {
		t.Error("Expected error when repository returns error")
	}
}

func TestSaveWithMockRepository(t *testing.T) {
	repo := NewMockRepository()
	settings := NewSettingsWithRepo(repo)
	settings.EnableAutoSync()

	err := settings.Save("/test/settings.json")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if len(repo.writeCalls) != 1 {
		t.Errorf("Expected 1 write call, got %d", len(repo.writeCalls))
	}

	if repo.writeCalls[0].perm != 0600 {
		t.Errorf("Expected permission 0600, got %o", repo.writeCalls[0].perm)
	}
}

func TestSaveWithMkdirError(t *testing.T) {
	repo := NewMockRepository()
	repo.mkdirErr = errors.New("cannot create directory")
	settings := NewSettingsWithRepo(repo)

	err := settings.Save("/test/settings.json")
	if err == nil {
		t.Error("Expected error when mkdir fails")
	}
}

func TestSaveWithWriteError(t *testing.T) {
	repo := NewMockRepository()
	repo.writeErr = errors.New("disk full")
	settings := NewSettingsWithRepo(repo)

	err := settings.Save("/test/settings.json")
	if err == nil {
		t.Error("Expected error when write fails")
	}
}

// --- Strategy Pattern Tests ---

func TestClaudeSyncMatcher(t *testing.T) {
	matcher := &ClaudeSyncMatcher{}

	tests := []struct {
		cmd      string
		expected bool
	}{
		{"claude-sync", true},
		{"claude-sync pull", true},
		{"claude-sync push -q", true},
		{"/usr/local/bin/claude-sync", true},
		{"/usr/local/bin/claude-sync pull", true},
		{"echo hello", false},
		{"my-claude-sync", false},
		{"claude-sync-wrapper", false},
		{"  claude-sync pull  ", true}, // with whitespace
	}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			result := matcher.Matches(tc.cmd)
			if result != tc.expected {
				t.Errorf("Matches(%q) = %v, want %v", tc.cmd, result, tc.expected)
			}
		})
	}
}

func TestHasMatchingHookWithCustomMatcher(t *testing.T) {
	groups := []HookGroup{{
		Hooks: []HookEntry{
			{Type: "command", Command: "echo hello"},
			{Type: "command", Command: "custom-tool run"},
		},
	}}

	// Matcher that matches "custom-tool"
	matcher := &MockMatcher{
		matchFunc: func(cmd string) bool {
			return cmd == "custom-tool run"
		},
	}

	if !HasMatchingHook(groups, matcher) {
		t.Error("Expected HasMatchingHook to return true for custom matcher")
	}

	// Matcher that matches nothing
	matcher.matchFunc = func(cmd string) bool { return false }
	if HasMatchingHook(groups, matcher) {
		t.Error("Expected HasMatchingHook to return false when no match")
	}
}

func TestRemoveMatchingHooksWithCustomMatcher(t *testing.T) {
	groups := []HookGroup{{
		Hooks: []HookEntry{
			{Type: "command", Command: "keep-this"},
			{Type: "command", Command: "remove-this"},
			{Type: "command", Command: "also-keep"},
		},
	}}

	// Remove commands containing "remove"
	matcher := &MockMatcher{
		matchFunc: func(cmd string) bool {
			return cmd == "remove-this"
		},
	}

	result := RemoveMatchingHooks(groups, matcher)
	if len(result) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(result))
	}
	if len(result[0].Hooks) != 2 {
		t.Fatalf("Expected 2 hooks, got %d", len(result[0].Hooks))
	}
	for _, h := range result[0].Hooks {
		if h.Command == "remove-this" {
			t.Error("remove-this should have been removed")
		}
	}
}

// --- Builder Pattern Tests ---

func TestHookEntryBuilder(t *testing.T) {
	entry := NewHookEntry().
		WithType("command").
		WithCommand("echo hello").
		Build()

	if entry.Type != "command" {
		t.Errorf("Expected type 'command', got %q", entry.Type)
	}
	if entry.Command != "echo hello" {
		t.Errorf("Expected command 'echo hello', got %q", entry.Command)
	}
}

func TestHookEntryBuilderDefaults(t *testing.T) {
	entry := NewHookEntry().WithCommand("test").Build()

	// Default type should be "command"
	if entry.Type != HookTypeCommand {
		t.Errorf("Expected default type %q, got %q", HookTypeCommand, entry.Type)
	}
}

// --- Configuration Tests ---

func TestDefaultAutoSyncConfig(t *testing.T) {
	cfg := DefaultAutoSyncConfig()

	if cfg.PullCommand != HookCommandPull {
		t.Errorf("PullCommand = %q, want %q", cfg.PullCommand, HookCommandPull)
	}
	if cfg.PushCommand != HookCommandPush {
		t.Errorf("PushCommand = %q, want %q", cfg.PushCommand, HookCommandPush)
	}
	if cfg.PullEvent != EventSessionStart {
		t.Errorf("PullEvent = %q, want %q", cfg.PullEvent, EventSessionStart)
	}
	if cfg.PushEvent != EventStop {
		t.Errorf("PushEvent = %q, want %q", cfg.PushEvent, EventStop)
	}
}

func TestEnableAutoSyncWithCustomConfig(t *testing.T) {
	settings := NewSettings()

	cfg := AutoSyncConfig{
		PullCommand: "custom-sync pull",
		PushCommand: "custom-sync push",
		PullEvent:   "CustomStart",
		PushEvent:   "CustomStop",
	}

	changed := settings.EnableAutoSyncWithConfig(cfg)
	if !changed {
		t.Error("Expected changed=true")
	}

	if len(settings.Hooks["CustomStart"]) == 0 {
		t.Error("CustomStart hooks should be added")
	}
	if len(settings.Hooks["CustomStop"]) == 0 {
		t.Error("CustomStop hooks should be added")
	}
}

func TestSettingsPath(t *testing.T) {
	// Default path
	path := SettingsPath("")
	if path == "" {
		t.Error("SettingsPath should return non-empty path")
	}
	if filepath.Base(path) != "settings.json" {
		t.Errorf("Expected settings.json, got %s", filepath.Base(path))
	}

	// Override path
	override := "/custom/path/settings.json"
	path = SettingsPath(override)
	if path != override {
		t.Errorf("Expected %s, got %s", override, path)
	}
}

func TestLoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load should not error on non-existent file: %v", err)
	}
	if settings == nil {
		t.Fatal("Settings should not be nil")
	}
	if settings.Hooks == nil {
		t.Error("Hooks map should be initialized")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if settings.Hooks == nil {
		t.Error("Hooks map should be initialized even for empty file")
	}
}

func TestLoadWithExistingHooks(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	content := `{
		"hooks": {
			"SessionStart": [{
				"matcher": "",
				"hooks": [{"type": "command", "command": "echo hello"}]
			}]
		},
		"otherField": "preserved"
	}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	hooks := settings.Hooks["SessionStart"]
	if len(hooks) != 1 {
		t.Fatalf("Expected 1 hook group, got %d", len(hooks))
	}
	if len(hooks[0].Hooks) != 1 {
		t.Fatalf("Expected 1 hook entry, got %d", len(hooks[0].Hooks))
	}
	if hooks[0].Hooks[0].Command != "echo hello" {
		t.Errorf("Expected 'echo hello', got '%s'", hooks[0].Hooks[0].Command)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	if err := os.WriteFile(path, []byte("not valid json"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestSavePreservesUnknownFields(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	original := `{
  "env": {"FOO": "bar"},
  "hooks": {},
  "customField": 123,
  "nested": {"a": 1, "b": 2}
}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Modify hooks
	settings.Hooks["SessionStart"] = []HookGroup{{
		Matcher: "",
		Hooks:   []HookEntry{{Type: "command", Command: "test"}},
	}}

	if err := settings.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read back and verify unknown fields preserved
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if result["customField"] != float64(123) {
		t.Errorf("customField not preserved: %v", result["customField"])
	}
	if result["env"] == nil {
		t.Error("env field not preserved")
	}
	if result["nested"] == nil {
		t.Error("nested field not preserved")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "settings.json")

	settings := NewSettings()
	if err := settings.Save(path); err != nil {
		t.Fatalf("Save should create parent directory: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("File should exist after save")
	}
}

func TestSaveFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	settings := NewSettings()
	if err := settings.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	// Check permissions are 0600 (rw-------)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("Expected permissions 0600, got %o", perm)
	}
}

func TestHasClaudeSyncHook(t *testing.T) {
	tests := []struct {
		name     string
		groups   []HookGroup
		expected bool
	}{
		{
			name:     "empty groups",
			groups:   nil,
			expected: false,
		},
		{
			name: "no claude-sync hooks",
			groups: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "echo hello"}},
			}},
			expected: false,
		},
		{
			name: "has claude-sync pull",
			groups: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "claude-sync pull -q"}},
			}},
			expected: true,
		},
		{
			name: "has claude-sync push",
			groups: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "claude-sync push -q"}},
			}},
			expected: true,
		},
		{
			name: "similar but not exact match",
			groups: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "my-claude-sync-tool pull"}},
			}},
			expected: false,
		},
		{
			name: "claude-sync in middle of command",
			groups: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "wrapper claude-sync pull"}},
			}},
			expected: false,
		},
		{
			name: "multiple groups with claude-sync in second",
			groups: []HookGroup{
				{Hooks: []HookEntry{{Type: "command", Command: "echo first"}}},
				{Hooks: []HookEntry{{Type: "command", Command: "claude-sync push"}}},
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := HasClaudeSyncHook(tc.groups)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestAddHook(t *testing.T) {
	// Add to nil slice
	groups := AddHook(nil, "test command")
	if len(groups) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(groups))
	}
	if len(groups[0].Hooks) != 1 {
		t.Fatalf("Expected 1 hook, got %d", len(groups[0].Hooks))
	}
	if groups[0].Hooks[0].Command != "test command" {
		t.Errorf("Expected 'test command', got '%s'", groups[0].Hooks[0].Command)
	}
	if groups[0].Hooks[0].Type != "command" {
		t.Errorf("Expected type 'command', got '%s'", groups[0].Hooks[0].Type)
	}

	// Add to existing slice
	groups = AddHook(groups, "another command")
	if len(groups) != 2 {
		t.Errorf("Expected 2 groups after second add, got %d", len(groups))
	}
}

func TestRemoveClaudeSyncHooks(t *testing.T) {
	tests := []struct {
		name         string
		input        []HookGroup
		expectedLen  int
		expectedCmds []string
	}{
		{
			name:        "nil input",
			input:       nil,
			expectedLen: 0,
		},
		{
			name: "remove single claude-sync hook",
			input: []HookGroup{{
				Hooks: []HookEntry{{Type: "command", Command: "claude-sync pull -q"}},
			}},
			expectedLen: 0,
		},
		{
			name: "preserve non-claude-sync hooks",
			input: []HookGroup{
				{Hooks: []HookEntry{{Type: "command", Command: "echo hello"}}},
				{Hooks: []HookEntry{{Type: "command", Command: "claude-sync push -q"}}},
			},
			expectedLen:  1,
			expectedCmds: []string{"echo hello"},
		},
		{
			name: "mixed hooks in same group",
			input: []HookGroup{{
				Hooks: []HookEntry{
					{Type: "command", Command: "echo before"},
					{Type: "command", Command: "claude-sync pull -q"},
					{Type: "command", Command: "echo after"},
				},
			}},
			expectedLen:  1,
			expectedCmds: []string{"echo before", "echo after"},
		},
		{
			name: "preserve hooks that look similar",
			input: []HookGroup{{
				Hooks: []HookEntry{
					{Type: "command", Command: "my-claude-sync pull"},
					{Type: "command", Command: "claude-sync pull"},
				},
			}},
			expectedLen:  1,
			expectedCmds: []string{"my-claude-sync pull"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := RemoveClaudeSyncHooks(tc.input)
			if len(result) != tc.expectedLen {
				t.Errorf("Expected %d groups, got %d", tc.expectedLen, len(result))
				return
			}

			// Collect all commands
			var cmds []string
			for _, g := range result {
				for _, h := range g.Hooks {
					cmds = append(cmds, h.Command)
				}
			}

			if len(cmds) != len(tc.expectedCmds) {
				t.Errorf("Expected %d commands, got %d: %v", len(tc.expectedCmds), len(cmds), cmds)
				return
			}

			for i, expected := range tc.expectedCmds {
				if cmds[i] != expected {
					t.Errorf("Command %d: expected '%s', got '%s'", i, expected, cmds[i])
				}
			}
		})
	}
}

func TestEnableAutoSync(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	// Start with empty settings
	settings := NewSettings()
	changed := settings.EnableAutoSync()

	if !changed {
		t.Error("EnableAutoSync should return true for fresh settings")
	}

	// Verify hooks were added
	if !HasClaudeSyncHook(settings.Hooks["SessionStart"]) {
		t.Error("SessionStart hook should be added")
	}
	if !HasClaudeSyncHook(settings.Hooks["Stop"]) {
		t.Error("Stop hook should be added")
	}

	// Save and reload
	if err := settings.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Enable again should be no-op
	changed = reloaded.EnableAutoSync()
	if changed {
		t.Error("EnableAutoSync should return false when hooks already present")
	}
}

func TestDisableAutoSync(t *testing.T) {
	settings := NewSettings()

	// Disable on empty settings
	changed := settings.DisableAutoSync()
	if changed {
		t.Error("DisableAutoSync should return false when no hooks present")
	}

	// Enable then disable
	settings.EnableAutoSync()
	changed = settings.DisableAutoSync()
	if !changed {
		t.Error("DisableAutoSync should return true after enabling")
	}

	if HasClaudeSyncHook(settings.Hooks["SessionStart"]) {
		t.Error("SessionStart hook should be removed")
	}
	if HasClaudeSyncHook(settings.Hooks["Stop"]) {
		t.Error("Stop hook should be removed")
	}
}

func TestAutoSyncStatus(t *testing.T) {
	settings := NewSettings()

	status := settings.AutoSyncStatus()
	if status.Enabled {
		t.Error("Status should be disabled initially")
	}

	settings.EnableAutoSync()
	status = settings.AutoSyncStatus()
	if !status.Enabled {
		t.Error("Status should be enabled after EnableAutoSync")
	}
	if !status.HasSessionStart {
		t.Error("HasSessionStart should be true")
	}
	if !status.HasStop {
		t.Error("HasStop should be true")
	}
}

func TestIntegrationRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	// Create settings with existing content
	original := `{
  "env": {"DEBUG": "1"},
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{"type": "command", "command": "echo 'bash called'"}]
    }]
  },
  "enabledPlugins": {"plugin1": true}
}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	// Load, enable auto-sync, save
	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	settings.EnableAutoSync()
	if err := settings.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load again and verify
	settings2, err := Load(path)
	if err != nil {
		t.Fatalf("Second load failed: %v", err)
	}

	// Verify PreToolUse preserved
	pre := settings2.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "Bash" {
		t.Error("PreToolUse hooks not preserved")
	}

	// Verify auto-sync hooks added
	if !HasClaudeSyncHook(settings2.Hooks["SessionStart"]) {
		t.Error("SessionStart hook missing")
	}
	if !HasClaudeSyncHook(settings2.Hooks["Stop"]) {
		t.Error("Stop hook missing")
	}

	// Verify other fields preserved by reading raw JSON
	data, _ := os.ReadFile(path)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["enabledPlugins"] == nil {
		t.Error("enabledPlugins not preserved")
	}
	if raw["env"] == nil {
		t.Error("env not preserved")
	}
}
