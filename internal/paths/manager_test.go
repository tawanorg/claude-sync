package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewManager(t *testing.T) {
	// With empty paths, should use defaults
	m := NewManager(nil, nil, "/tmp/test")
	if len(m.SyncPaths()) != len(DefaultSyncPaths) {
		t.Errorf("Expected %d default paths, got %d", len(DefaultSyncPaths), len(m.SyncPaths()))
	}

	// With custom paths, should use those
	custom := []string{"path1", "path2"}
	m = NewManager(custom, nil, "/tmp/test")
	if len(m.SyncPaths()) != 2 {
		t.Errorf("Expected 2 paths, got %d", len(m.SyncPaths()))
	}
}

func TestAddPath(t *testing.T) {
	m := NewManager([]string{"existing"}, nil, "/tmp/nonexistent")

	// Add new path
	result := m.Add("newpath")
	if !result.Added {
		t.Error("Expected Added=true")
	}
	if result.PathMissing != true {
		t.Error("Expected PathMissing=true for non-existent path")
	}
	if !m.HasPath("newpath") {
		t.Error("newpath should be in sync paths")
	}

	// Add existing path
	result = m.Add("existing")
	if !result.AlreadyExists {
		t.Error("Expected AlreadyExists=true")
	}
	if result.Added {
		t.Error("Should not add duplicate")
	}
}

func TestAddPathRemovesConflictingExcludes(t *testing.T) {
	m := NewManager([]string{"a"}, []string{"b", "b/*", "c/**"}, "/tmp/test")

	// Add 'b' should remove 'b' and 'b/*' excludes
	result := m.Add("b")
	if result.ExcludesRemoved != 2 {
		t.Errorf("Expected 2 excludes removed, got %d", result.ExcludesRemoved)
	}
	if m.HasExclude("b") || m.HasExclude("b/*") {
		t.Error("Conflicting excludes should be removed")
	}
	if !m.HasExclude("c/**") {
		t.Error("Unrelated exclude should remain")
	}
}

func TestRemovePath(t *testing.T) {
	m := NewManager([]string{"CLAUDE.md", "custom"}, nil, "/tmp/test")

	// Remove non-existent
	result := m.Remove("nonexistent")
	if !result.NotFound {
		t.Error("Expected NotFound=true")
	}

	// Remove custom path (not a default)
	result = m.Remove("custom")
	if !result.Removed {
		t.Error("Expected Removed=true")
	}
	if result.IsDefault {
		t.Error("'custom' is not a default path")
	}
	if result.ExcludeAdded != "" {
		t.Error("Custom path removal should not add exclude")
	}

	// Remove default path
	m = NewManager([]string{"CLAUDE.md"}, nil, "/tmp/test")
	result = m.Remove("CLAUDE.md")
	if !result.Removed {
		t.Error("Expected Removed=true")
	}
	if !result.IsDefault {
		t.Error("CLAUDE.md is a default path")
	}
	if result.ExcludeAdded == "" {
		t.Error("Default path removal should add exclude")
	}
}

func TestRemoveDefaultPathAddsExclude(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")

	// Create a directory to test dir detection
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(nil, nil, claudeDir)

	// Remove 'agents' (a directory)
	result := m.Remove("agents")
	if result.ExcludeAdded != "agents/*" {
		t.Errorf("Expected exclude 'agents/*', got %q", result.ExcludeAdded)
	}

	// Verify exclude was added
	if !m.HasExclude("agents/*") {
		t.Error("Exclude should be in list")
	}
}

func TestAddExclude(t *testing.T) {
	m := NewManager([]string{"plugins"}, []string{"existing/*"}, "/tmp/test")

	// Add new exclude
	result := m.AddExclude("plugins/**/node_modules/**")
	if !result.Added {
		t.Error("Expected Added=true")
	}

	// Add duplicate
	result = m.AddExclude("existing/*")
	if !result.AlreadyExists {
		t.Error("Expected AlreadyExists=true")
	}

	// Try to exclude a sync path directly
	result = m.AddExclude("plugins")
	if !result.IsSyncPath {
		t.Error("Expected IsSyncPath=true - should use Remove instead")
	}
	if result.Added {
		t.Error("Should not add sync path as exclude")
	}
}

func TestRemoveExclude(t *testing.T) {
	m := NewManager(nil, []string{"a/*", "b/**"}, "/tmp/test")

	// Remove existing
	result := m.RemoveExclude("a/*")
	if !result.Removed {
		t.Error("Expected Removed=true")
	}
	if m.HasExclude("a/*") {
		t.Error("Exclude should be removed")
	}

	// Remove non-existent
	result = m.RemoveExclude("nonexistent")
	if !result.NotFound {
		t.Error("Expected NotFound=true")
	}
}

func TestReset(t *testing.T) {
	m := NewManager(
		[]string{"custom1", "custom2"},
		[]string{"exclude1", "exclude2"},
		"/tmp/test",
	)

	m.Reset()

	if len(m.SyncPaths()) != len(DefaultSyncPaths) {
		t.Errorf("Expected %d default paths after reset, got %d", len(DefaultSyncPaths), len(m.SyncPaths()))
	}
	if len(m.Excludes()) != 0 {
		t.Errorf("Expected 0 excludes after reset, got %d", len(m.Excludes()))
	}
}

func TestIsDefault(t *testing.T) {
	m := NewManager(nil, nil, "/tmp/test")

	if !m.IsDefault("CLAUDE.md") {
		t.Error("CLAUDE.md should be a default")
	}
	if !m.IsDefault("settings.json") {
		t.Error("settings.json should be a default")
	}
	if m.IsDefault("custom-path") {
		t.Error("custom-path should not be a default")
	}
}

func TestStatus(t *testing.T) {
	// Default state
	m := NewManager(nil, nil, "/tmp/test")
	status := m.Status()
	if status.IsCustomized {
		t.Error("Default manager should not be customized")
	}
	if len(status.CustomPaths) != 0 {
		t.Error("No custom paths expected")
	}

	// Add custom path
	m.Add("my-custom")
	status = m.Status()
	if !status.IsCustomized {
		t.Error("Should be customized after adding path")
	}
	if len(status.CustomPaths) != 1 || status.CustomPaths[0] != "my-custom" {
		t.Error("Custom path not tracked")
	}

	// Remove default and check removed defaults tracking
	m = NewManager(nil, []string{"CLAUDE.md/*"}, "/tmp/test")
	status = m.Status()
	if len(status.RemovedDefaults) != 1 {
		t.Errorf("Expected 1 removed default, got %d", len(status.RemovedDefaults))
	}
}

func TestHasPath(t *testing.T) {
	m := NewManager([]string{"a", "b"}, nil, "/tmp/test")

	if !m.HasPath("a") {
		t.Error("Should have path 'a'")
	}
	if m.HasPath("c") {
		t.Error("Should not have path 'c'")
	}
}

func TestHasExclude(t *testing.T) {
	m := NewManager(nil, []string{"*.tmp", "cache/**"}, "/tmp/test")

	if !m.HasExclude("*.tmp") {
		t.Error("Should have exclude '*.tmp'")
	}
	if m.HasExclude("*.log") {
		t.Error("Should not have exclude '*.log'")
	}
}

func TestIntegrationWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Start fresh
	m := NewManager(nil, nil, claudeDir)

	// 1. Remove a default path
	result := m.Remove("history.jsonl")
	if !result.Removed || !result.IsDefault {
		t.Error("Should remove default")
	}
	if !m.HasExclude("history.jsonl") {
		t.Error("Should add exclude for removed default")
	}

	// Capture current state
	currentPaths := append([]string{}, m.SyncPaths()...)
	currentExcludes := append([]string{}, m.Excludes()...)

	// 2. Simulate re-adding with the saved state (like after config reload)
	// This simulates: user removed history.jsonl, config was saved with
	// modified syncPaths (without history.jsonl) and excludes (with history.jsonl)
	m = NewManager(currentPaths, currentExcludes, claudeDir)

	// Verify history.jsonl is NOT in sync paths but IS in excludes
	if m.HasPath("history.jsonl") {
		t.Error("history.jsonl should not be in sync paths")
	}
	if !m.HasExclude("history.jsonl") {
		t.Error("history.jsonl should be in excludes")
	}

	// 3. Add path back should work and remove the exclude
	result2 := m.Add("history.jsonl")
	if !result2.Added {
		t.Error("Should add history.jsonl back")
	}
	if result2.ExcludesRemoved != 1 {
		t.Errorf("Should remove 1 exclude, got %d", result2.ExcludesRemoved)
	}
	if m.HasExclude("history.jsonl") {
		t.Error("history.jsonl exclude should be removed")
	}

	// 4. Add custom path
	m.Add("my-notes")
	status := m.Status()
	if len(status.CustomPaths) != 1 {
		t.Error("Should have 1 custom path")
	}

	// 5. Add sub-path filter
	m.AddExclude("plugins/**/node_modules/**")
	if !m.HasExclude("plugins/**/node_modules/**") {
		t.Error("Should have the exclude")
	}

	// 6. Reset clears everything
	m.Reset()
	if !m.HasPath("history.jsonl") {
		t.Error("Reset should restore defaults including history.jsonl")
	}
	if len(m.Excludes()) != 0 {
		t.Error("Reset should clear excludes")
	}
}
