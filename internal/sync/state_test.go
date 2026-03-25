package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewState(t *testing.T) {
	state := NewState()

	if state == nil {
		t.Fatal("NewState returned nil")
	}

	if state.Files == nil {
		t.Error("Files map should be initialized")
	}

	if state.DeviceID == "" {
		t.Error("DeviceID should be set to hostname")
	}
}

func TestStateUpdateFile(t *testing.T) {
	state := NewState()

	// Create a temporary file to get real FileInfo
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("Failed to stat temp file: %v", err)
	}

	hash := "abc123hash"
	state.UpdateFile("test.txt", info, hash)

	file := state.GetFile("test.txt")
	if file == nil {
		t.Fatal("GetFile returned nil after UpdateFile")
	}

	if file.Path != "test.txt" {
		t.Errorf("Expected path 'test.txt', got '%s'", file.Path)
	}

	if file.Hash != hash {
		t.Errorf("Expected hash '%s', got '%s'", hash, file.Hash)
	}

	if file.Size != info.Size() {
		t.Errorf("Expected size %d, got %d", info.Size(), file.Size)
	}
}

func TestStateMarkUploaded(t *testing.T) {
	state := NewState()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	info, _ := os.Stat(tmpFile)
	state.UpdateFile("test.txt", info, "hash123")

	before := time.Now()
	state.MarkUploaded("test.txt")
	after := time.Now()

	file := state.GetFile("test.txt")
	if file.Uploaded.Before(before) || file.Uploaded.After(after) {
		t.Error("Uploaded time should be between before and after")
	}
}

func TestStateRemoveFile(t *testing.T) {
	state := NewState()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	info, _ := os.Stat(tmpFile)
	state.UpdateFile("test.txt", info, "hash123")

	if state.GetFile("test.txt") == nil {
		t.Fatal("File should exist before removal")
	}

	state.RemoveFile("test.txt")

	if state.GetFile("test.txt") != nil {
		t.Error("File should be nil after removal")
	}
}

func TestHashFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	content := []byte("Hello, World!")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	hash1, err := HashFile(tmpFile)
	if err != nil {
		t.Fatalf("HashFile failed: %v", err)
	}

	if hash1 == "" {
		t.Error("Hash should not be empty")
	}

	// Same content should produce same hash
	hash2, err := HashFile(tmpFile)
	if err != nil {
		t.Fatalf("HashFile failed on second call: %v", err)
	}

	if hash1 != hash2 {
		t.Error("Same file should produce same hash")
	}

	// Different content should produce different hash
	if err := os.WriteFile(tmpFile, []byte("Different content"), 0644); err != nil {
		t.Fatalf("Failed to update temp file: %v", err)
	}

	hash3, err := HashFile(tmpFile)
	if err != nil {
		t.Fatalf("HashFile failed on third call: %v", err)
	}

	if hash1 == hash3 {
		t.Error("Different content should produce different hash")
	}
}

func TestGetLocalFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory structure similar to .claude
	dirs := []string{"agents", "skills", "plugins"}
	files := map[string]string{
		"CLAUDE.md":          "# Claude MD",
		"settings.json":      "{}",
		"agents/agent1.json": `{"name": "agent1"}`,
		"skills/skill1.md":   "# Skill 1",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", path, err)
		}
	}

	// Test GetLocalFiles with specific sync paths
	syncPaths := []string{"CLAUDE.md", "settings.json", "agents", "skills"}
	localFiles, err := GetLocalFiles(tmpDir, syncPaths)
	if err != nil {
		t.Fatalf("GetLocalFiles failed: %v", err)
	}

	// Check that all expected files are found
	expectedFiles := []string{"CLAUDE.md", "settings.json", "agents/agent1.json", "skills/skill1.md"}
	for _, expected := range expectedFiles {
		if _, ok := localFiles[expected]; !ok {
			t.Errorf("Expected file '%s' not found in localFiles", expected)
		}
	}

	// Check that plugins directory (empty) is not included
	for path := range localFiles {
		if strings.HasPrefix(path, "plugins") {
			t.Errorf("Empty plugins directory should not have files, but found: %s", path)
		}
	}
}

func TestDetectChanges(t *testing.T) {
	tmpDir := t.TempDir()
	state := NewState()

	// Create initial files
	files := map[string]string{
		"file1.txt": "content1",
		"file2.txt": "content2",
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", name, err)
		}
	}

	// First detection - all files should be "add"
	changes, err := state.DetectChanges(tmpDir, []string{"file1.txt", "file2.txt"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 2 {
		t.Errorf("Expected 2 changes, got %d", len(changes))
	}

	for _, change := range changes {
		if change.Action != "add" {
			t.Errorf("Expected action 'add' for new file, got '%s'", change.Action)
		}
	}

	// Simulate syncing by adding files to state
	for _, change := range changes {
		info, _ := os.Stat(filepath.Join(tmpDir, change.Path))
		state.UpdateFile(change.Path, info, change.LocalHash)
	}

	// Second detection - no changes expected
	changes, err = state.DetectChanges(tmpDir, []string{"file1.txt", "file2.txt"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("Expected 0 changes after sync, got %d", len(changes))
	}

	// Modify a file
	if err := os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("modified content"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	changes, err = state.DetectChanges(tmpDir, []string{"file1.txt", "file2.txt"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 1 {
		t.Errorf("Expected 1 change after modification, got %d", len(changes))
	}

	if len(changes) > 0 && changes[0].Action != "modify" {
		t.Errorf("Expected action 'modify', got '%s'", changes[0].Action)
	}

	// Update state with modified file
	for _, change := range changes {
		info, _ := os.Stat(filepath.Join(tmpDir, change.Path))
		state.UpdateFile(change.Path, info, change.LocalHash)
	}

	// Delete a file
	if err := os.Remove(filepath.Join(tmpDir, "file2.txt")); err != nil {
		t.Fatalf("Failed to delete file: %v", err)
	}

	changes, err = state.DetectChanges(tmpDir, []string{"file1.txt", "file2.txt"})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	if len(changes) != 1 {
		t.Errorf("Expected 1 change after deletion, got %d", len(changes))
	}

	if len(changes) > 0 && changes[0].Action != "delete" {
		t.Errorf("Expected action 'delete', got '%s'", changes[0].Action)
	}
}

func TestGetLocalFilesWithExclude(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure mimicking ~/.claude
	dirs := []string{
		"plugins/cache/thedotmack/claude-mem",
		"plugins/marketplaces/repo",
		"projects/myproject",
		"agents",
		"debug",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
	}

	fileContents := map[string]string{
		"CLAUDE.md":                                     "# Claude",
		"settings.json":                                 "{}",
		"plugins/installed_plugins.json":                "{}",
		"plugins/cache/thedotmack/claude-mem/index.js":  "module.exports = {}",
		"plugins/marketplaces/repo/package.json":        `{"name": "repo"}`,
		"projects/myproject/memory.md":                  "# Memory",
		"agents/seo.md":                                 "# SEO Agent",
		"debug/log.txt":                                 "debug output",
	}

	for path, content := range fileContents {
		if err := os.WriteFile(filepath.Join(tmpDir, path), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", path, err)
		}
	}

	syncPaths := []string{"CLAUDE.md", "settings.json", "plugins", "projects", "agents", "debug"}

	// Without exclude — all files found
	allFiles, err := GetLocalFiles(tmpDir, syncPaths)
	if err != nil {
		t.Fatalf("GetLocalFiles failed: %v", err)
	}
	if len(allFiles) != len(fileContents) {
		t.Errorf("Expected %d files without exclude, got %d", len(fileContents), len(allFiles))
	}

	// With exclude — plugin cache, marketplaces, and debug excluded
	excludeFn := func(relPath string) bool {
		patterns := []string{"plugins/cache", "plugins/marketplaces", "debug"}
		for _, p := range patterns {
			if relPath == p || strings.HasPrefix(relPath, p+"/") {
				return true
			}
		}
		return false
	}
	filteredFiles, err := GetLocalFiles(tmpDir, syncPaths, excludeFn)
	if err != nil {
		t.Fatalf("GetLocalFiles with exclude failed: %v", err)
	}

	// Should include: CLAUDE.md, settings.json, installed_plugins.json, memory.md, seo.md
	expectedIncluded := []string{
		"CLAUDE.md", "settings.json", "plugins/installed_plugins.json",
		"projects/myproject/memory.md", "agents/seo.md",
	}
	for _, f := range expectedIncluded {
		if _, ok := filteredFiles[f]; !ok {
			t.Errorf("Expected file %q to be included after filtering", f)
		}
	}

	// Should exclude: cache, marketplaces, debug
	expectedExcluded := []string{
		"plugins/cache/thedotmack/claude-mem/index.js",
		"plugins/marketplaces/repo/package.json",
		"debug/log.txt",
	}
	for _, f := range expectedExcluded {
		if _, ok := filteredFiles[f]; ok {
			t.Errorf("Expected file %q to be excluded after filtering, but it was included", f)
		}
	}

	if len(filteredFiles) != len(expectedIncluded) {
		t.Errorf("Expected %d files after exclude, got %d", len(expectedIncluded), len(filteredFiles))
	}
}

func TestDetectChangesWithExclude(t *testing.T) {
	tmpDir := t.TempDir()
	state := NewState()

	// Create files including some that should be excluded
	os.MkdirAll(filepath.Join(tmpDir, "plugins/cache"), 0755)
	files := map[string]string{
		"settings.json":         "{}",
		"plugins/cache/big.dat": "lots of data",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", name, err)
		}
	}

	// Detect changes with exclude
	excludeFn := func(relPath string) bool {
		return relPath == "plugins/cache" || strings.HasPrefix(relPath, "plugins/cache/")
	}
	changes, err := state.DetectChanges(tmpDir, []string{"settings.json", "plugins"}, excludeFn)
	if err != nil {
		t.Fatalf("DetectChanges with exclude failed: %v", err)
	}

	// Only settings.json should be detected
	if len(changes) != 1 {
		t.Errorf("Expected 1 change (settings.json only), got %d", len(changes))
		for _, c := range changes {
			t.Logf("  change: %s (%s)", c.Path, c.Action)
		}
	}
	if len(changes) == 1 && changes[0].Path != "settings.json" {
		t.Errorf("Expected change for settings.json, got %s", changes[0].Path)
	}
}

func TestGetLocalFilesSkipsSymlinksInDirectories(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Create a regular file in the subdirectory
	regularFile := filepath.Join(subDir, "regular.txt")
	if err := os.WriteFile(regularFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create regular file: %v", err)
	}

	// Create a symlink in the subdirectory
	symlink := filepath.Join(subDir, "symlink.txt")
	if err := os.Symlink(regularFile, symlink); err != nil {
		// Skip test if symlinks aren't supported
		t.Skip("Symlinks not supported on this system")
	}

	localFiles, err := GetLocalFiles(tmpDir, []string{"subdir"})
	if err != nil {
		t.Fatalf("GetLocalFiles failed: %v", err)
	}

	// Regular file should be included
	if _, ok := localFiles["subdir/regular.txt"]; !ok {
		t.Error("Regular file in subdir should be included")
	}

	// Symlink inside directory walk should be skipped
	if _, ok := localFiles["subdir/symlink.txt"]; ok {
		t.Error("Symlink in subdir should be skipped during directory walk")
	}
}
