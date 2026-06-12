package claudesettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookEntry represents a single hook command entry.
type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookGroup represents a matcher + list of hooks.
type HookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []HookEntry `json:"hooks"`
}

// Settings holds the parsed hooks section of ~/.claude/settings.json.
type Settings struct {
	Hooks map[string][]HookGroup
}

// SettingsPath returns the path to ~/.claude/settings.json.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Load reads ~/.claude/settings.json, returning the parsed hooks and the full
// raw map so that unknown fields are preserved on Save.
func Load() (*Settings, map[string]json.RawMessage, error) {
	path, err := SettingsPath()
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{Hooks: make(map[string][]HookGroup)}, make(map[string]json.RawMessage), nil
		}
		return nil, nil, fmt.Errorf("reading settings: %w", err)
	}

	// Decode all fields as raw JSON to preserve unknown fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing settings: %w", err)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}

	s := &Settings{Hooks: make(map[string][]HookGroup)}
	if hooksRaw, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &s.Hooks); err != nil {
			return nil, nil, fmt.Errorf("parsing hooks: %w", err)
		}
	}

	return s, raw, nil
}

// Save writes the settings back to ~/.claude/settings.json, preserving all
// fields that were in the original file.
func Save(s *Settings, raw map[string]json.RawMessage) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}

	// Ensure the directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating .claude directory: %w", err)
	}

	hooksData, err := json.Marshal(s.Hooks)
	if err != nil {
		return fmt.Errorf("marshalling hooks: %w", err)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}
	raw["hooks"] = json.RawMessage(hooksData)

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0600)
}

// HasClaudeSyncHook returns true if any hook entry in groups has a command
// containing "claude-sync".
func HasClaudeSyncHook(groups []HookGroup) bool {
	for _, g := range groups {
		for _, h := range g.Hooks {
			if strings.Contains(h.Command, "claude-sync") {
				return true
			}
		}
	}
	return false
}

// AddHook appends a new hook group with a single command entry.
func AddHook(groups []HookGroup, command string) []HookGroup {
	return append(groups, HookGroup{
		Matcher: "",
		Hooks:   []HookEntry{{Type: "command", Command: command}},
	})
}

// RemoveClaudeSyncHooks removes any hook entries whose command contains
// "claude-sync". Groups that become empty are dropped entirely.
func RemoveClaudeSyncHooks(groups []HookGroup) []HookGroup {
	var result []HookGroup
	for _, g := range groups {
		var kept []HookEntry
		for _, h := range g.Hooks {
			if !strings.Contains(h.Command, "claude-sync") {
				kept = append(kept, h)
			}
		}
		if len(kept) > 0 {
			g.Hooks = kept
			result = append(result, g)
		}
		// Drop the group entirely if all hooks were claude-sync.
	}
	return result
}
