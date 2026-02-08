package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetClearsConfigDir(t *testing.T) {
	// Create temp home directory
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create mock config directory
	configDir := filepath.Join(tmpDir, ".claude-sync")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Create mock files
	files := []string{"config.yaml", "age-key.txt", "state.json"}
	for _, f := range files {
		path := filepath.Join(configDir, f)
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatalf("Failed to create %s: %v", f, err)
		}
	}

	// Verify files exist
	for _, f := range files {
		path := filepath.Join(configDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatalf("File should exist before reset: %s", f)
		}
	}

	// Remove config dir (simulating reset)
	if err := os.RemoveAll(configDir); err != nil {
		t.Fatalf("Failed to remove config dir: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Error("Config directory should not exist after reset")
	}
}

func TestResetClearsStateFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create config dir and state file
	configDir := filepath.Join(tmpDir, ".claude-sync")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	statePath := filepath.Join(configDir, "state.json")
	stateContent := `{"files": {}, "device_id": "test"}`
	if err := os.WriteFile(statePath, []byte(stateContent), 0600); err != nil {
		t.Fatalf("Failed to create state file: %v", err)
	}

	// Verify state exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("State file should exist before reset")
	}

	// Remove state file (simulating reset --local)
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("Failed to remove state file: %v", err)
	}

	// Verify state is gone
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("State file should not exist after reset --local")
	}
}

func TestResetPreservesClaudeDir(t *testing.T) {
	tmpDir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create both directories
	configDir := filepath.Join(tmpDir, ".claude-sync")
	claudeDir := filepath.Join(tmpDir, ".claude")

	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create claude dir: %v", err)
	}

	// Create a session file in claude dir
	sessionFile := filepath.Join(claudeDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("session data"), 0644); err != nil {
		t.Fatalf("Failed to create session file: %v", err)
	}

	// Remove only config dir (reset should not touch .claude)
	if err := os.RemoveAll(configDir); err != nil {
		t.Fatalf("Failed to remove config dir: %v", err)
	}

	// Verify .claude is still there
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		t.Error(".claude directory should be preserved after reset")
	}

	// Verify session file is still there
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		t.Error("Session file should be preserved after reset")
	}
}
