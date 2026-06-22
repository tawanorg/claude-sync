// Package claudesettings handles reading and writing Claude Code's settings.json file.
// It preserves unknown fields during round-trip and provides safe hook management.
//
// Design patterns used:
//   - Repository Pattern: SettingsRepository interface abstracts file I/O for testability
//   - Strategy Pattern: CommandMatcher interface allows pluggable command detection
//   - Null Object Pattern: NewSettings() returns usable empty state, not nil
//   - Template Method: Load/Save define the skeleton, delegates to repository
package claudesettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Constants for hook configuration
const (
	// HookCommandPull is the command installed for SessionStart.
	HookCommandPull = "claude-sync pull -q"
	// HookCommandPush is the command installed for Stop.
	HookCommandPush = "claude-sync push -q"

	// HookTypeCommand is the standard hook type for shell commands.
	HookTypeCommand = "command"

	// EventSessionStart is the hook event for session start.
	EventSessionStart = "SessionStart"
	// EventStop is the hook event for session stop.
	EventStop = "Stop"
)

// SettingsRepository defines the interface for settings persistence.
// Implementing this interface allows for easy testing with mock repositories.
type SettingsRepository interface {
	Read(path string) ([]byte, error)
	Write(path string, data []byte, perm os.FileMode) error
	Exists(path string) bool
	MkdirAll(path string, perm os.FileMode) error
}

// CommandMatcher defines the strategy for matching hook commands.
// This allows different matching strategies to be plugged in.
type CommandMatcher interface {
	Matches(command string) bool
}

// HookEntry represents a single hook command.
type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookGroup represents a matcher + list of hooks for an event.
type HookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []HookEntry `json:"hooks"`
}

// Settings represents Claude Code's settings.json with safe round-trip support.
// Unknown fields are preserved in rawData during Load/Save cycles.
type Settings struct {
	Hooks   map[string][]HookGroup
	rawData map[string]json.RawMessage
	repo    SettingsRepository
}

// AutoSyncStatus describes the current state of auto-sync hooks.
type AutoSyncStatus struct {
	Enabled         bool
	HasSessionStart bool
	HasStop         bool
}

// AutoSyncConfig defines the hooks to install for auto-sync.
type AutoSyncConfig struct {
	PullCommand string
	PushCommand string
	PullEvent   string
	PushEvent   string
}

// DefaultAutoSyncConfig returns the standard auto-sync configuration.
func DefaultAutoSyncConfig() AutoSyncConfig {
	return AutoSyncConfig{
		PullCommand: HookCommandPull,
		PushCommand: HookCommandPush,
		PullEvent:   EventSessionStart,
		PushEvent:   EventStop,
	}
}

// --- Repository Implementations ---

// FileRepository implements SettingsRepository using the real filesystem.
type FileRepository struct{}

// Read reads a file from disk.
func (r *FileRepository) Read(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Write writes data to a file with the given permissions.
func (r *FileRepository) Write(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// Exists checks if a file exists.
func (r *FileRepository) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MkdirAll creates a directory and all parents.
func (r *FileRepository) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// defaultRepo is the default repository used when none is specified.
var defaultRepo SettingsRepository = &FileRepository{}

// --- Command Matcher Implementations ---

// ClaudeSyncMatcher implements CommandMatcher for claude-sync commands.
// It uses exact prefix matching to avoid false positives.
type ClaudeSyncMatcher struct{}

// Matches returns true if the command is a claude-sync command.
func (m *ClaudeSyncMatcher) Matches(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	// Direct match: "claude-sync" or "claude-sync <args>"
	if cmd == "claude-sync" || strings.HasPrefix(cmd, "claude-sync ") {
		return true
	}
	// Match with absolute path: "/usr/local/bin/claude-sync <args>"
	if strings.HasSuffix(cmd, "/claude-sync") || strings.Contains(cmd, "/claude-sync ") {
		return true
	}
	return false
}

// defaultMatcher is the matcher used for claude-sync command detection.
var defaultMatcher CommandMatcher = &ClaudeSyncMatcher{}

// --- Path Resolution ---

// SettingsPath returns the path to settings.json.
// If override is non-empty, it is returned directly.
// Otherwise returns ~/.claude/settings.json.
func SettingsPath(override string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// --- Settings Factory ---

// NewSettings creates an empty Settings with initialized maps.
// This implements the Null Object pattern - the returned Settings
// is always usable, never nil.
func NewSettings() *Settings {
	return &Settings{
		Hooks:   make(map[string][]HookGroup),
		rawData: make(map[string]json.RawMessage),
		repo:    defaultRepo,
	}
}

// NewSettingsWithRepo creates Settings with a custom repository.
// This is useful for testing with mock repositories.
func NewSettingsWithRepo(repo SettingsRepository) *Settings {
	s := NewSettings()
	s.repo = repo
	return s
}

// --- Load/Save Operations ---

// Load reads settings.json from the given path.
// If the file doesn't exist, returns empty settings (not an error).
// Unknown fields are preserved for later Save calls.
func Load(path string) (*Settings, error) {
	return LoadWithRepo(path, defaultRepo)
}

// LoadWithRepo reads settings using the specified repository.
// This enables dependency injection for testing.
func LoadWithRepo(path string, repo SettingsRepository) (*Settings, error) {
	data, err := repo.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewSettingsWithRepo(repo), nil
		}
		return nil, fmt.Errorf("reading settings: %w", err)
	}

	// Handle empty file
	if len(data) == 0 {
		return NewSettingsWithRepo(repo), nil
	}

	// Decode all fields as raw JSON to preserve unknown fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing settings: %w", err)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}

	s := &Settings{
		Hooks:   make(map[string][]HookGroup),
		rawData: raw,
		repo:    repo,
	}

	// Parse hooks if present
	if hooksRaw, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &s.Hooks); err != nil {
			// If hooks field exists but is malformed, initialize empty
			s.Hooks = make(map[string][]HookGroup)
		}
	}

	return s, nil
}

// Save writes settings back to the given path.
// Unknown fields from the original Load are preserved.
// Parent directories are created if needed. File is written with 0600 permissions.
func (s *Settings) Save(path string) error {
	repo := s.repo
	if repo == nil {
		repo = defaultRepo
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := repo.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	// Update hooks in raw data
	hooksData, err := json.Marshal(s.Hooks)
	if err != nil {
		return fmt.Errorf("marshalling hooks: %w", err)
	}
	if s.rawData == nil {
		s.rawData = make(map[string]json.RawMessage)
	}
	s.rawData["hooks"] = json.RawMessage(hooksData)

	// Marshal with indentation
	data, err := json.MarshalIndent(s.rawData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}

	// Append newline for POSIX compliance
	data = append(data, '\n')

	return repo.Write(path, data, 0600)
}

// --- Hook Detection ---

// HasClaudeSyncHook returns true if any hook in the groups is a claude-sync command.
func HasClaudeSyncHook(groups []HookGroup) bool {
	return HasMatchingHook(groups, defaultMatcher)
}

// HasMatchingHook returns true if any hook matches the given matcher.
// This implements the Strategy pattern for hook detection.
func HasMatchingHook(groups []HookGroup, matcher CommandMatcher) bool {
	for _, g := range groups {
		for _, h := range g.Hooks {
			if matcher.Matches(h.Command) {
				return true
			}
		}
	}
	return false
}

// --- Hook Manipulation ---

// HookEntryBuilder provides a fluent API for creating hook entries.
type HookEntryBuilder struct {
	hookType string
	command  string
}

// NewHookEntry creates a new hook entry builder.
func NewHookEntry() *HookEntryBuilder {
	return &HookEntryBuilder{hookType: HookTypeCommand}
}

// WithType sets the hook type.
func (b *HookEntryBuilder) WithType(t string) *HookEntryBuilder {
	b.hookType = t
	return b
}

// WithCommand sets the hook command.
func (b *HookEntryBuilder) WithCommand(cmd string) *HookEntryBuilder {
	b.command = cmd
	return b
}

// Build creates the HookEntry.
func (b *HookEntryBuilder) Build() HookEntry {
	return HookEntry{Type: b.hookType, Command: b.command}
}

// AddHook appends a new hook group with a single command entry.
func AddHook(groups []HookGroup, command string) []HookGroup {
	entry := NewHookEntry().WithCommand(command).Build()
	return append(groups, HookGroup{
		Matcher: "",
		Hooks:   []HookEntry{entry},
	})
}

// RemoveClaudeSyncHooks removes all claude-sync hook entries.
// Groups that become empty are dropped entirely.
// Non-claude-sync hooks are preserved.
func RemoveClaudeSyncHooks(groups []HookGroup) []HookGroup {
	return RemoveMatchingHooks(groups, defaultMatcher)
}

// RemoveMatchingHooks removes hooks that match the given matcher.
// This implements the Strategy pattern for hook removal.
func RemoveMatchingHooks(groups []HookGroup, matcher CommandMatcher) []HookGroup {
	var result []HookGroup
	for _, g := range groups {
		var kept []HookEntry
		for _, h := range g.Hooks {
			if !matcher.Matches(h.Command) {
				kept = append(kept, h)
			}
		}
		if len(kept) > 0 {
			result = append(result, HookGroup{
				Matcher: g.Matcher,
				Hooks:   kept,
			})
		}
	}
	return result
}

// --- Auto-Sync Operations ---

// EnableAutoSync adds claude-sync hooks for SessionStart and Stop events.
// Returns true if any changes were made, false if hooks were already present.
func (s *Settings) EnableAutoSync() bool {
	return s.EnableAutoSyncWithConfig(DefaultAutoSyncConfig())
}

// EnableAutoSyncWithConfig adds hooks using the specified configuration.
// This allows customizing the commands and events used.
func (s *Settings) EnableAutoSyncWithConfig(cfg AutoSyncConfig) bool {
	changed := false

	if !HasClaudeSyncHook(s.Hooks[cfg.PullEvent]) {
		s.Hooks[cfg.PullEvent] = AddHook(s.Hooks[cfg.PullEvent], cfg.PullCommand)
		changed = true
	}

	if !HasClaudeSyncHook(s.Hooks[cfg.PushEvent]) {
		s.Hooks[cfg.PushEvent] = AddHook(s.Hooks[cfg.PushEvent], cfg.PushCommand)
		changed = true
	}

	return changed
}

// DisableAutoSync removes all claude-sync hooks from SessionStart and Stop events.
// Returns true if any changes were made, false if no hooks were present.
func (s *Settings) DisableAutoSync() bool {
	return s.DisableAutoSyncWithConfig(DefaultAutoSyncConfig())
}

// DisableAutoSyncWithConfig removes hooks from the specified events.
func (s *Settings) DisableAutoSyncWithConfig(cfg AutoSyncConfig) bool {
	hadPull := HasClaudeSyncHook(s.Hooks[cfg.PullEvent])
	hadPush := HasClaudeSyncHook(s.Hooks[cfg.PushEvent])

	if !hadPull && !hadPush {
		return false
	}

	s.Hooks[cfg.PullEvent] = RemoveClaudeSyncHooks(s.Hooks[cfg.PullEvent])
	s.Hooks[cfg.PushEvent] = RemoveClaudeSyncHooks(s.Hooks[cfg.PushEvent])

	// Clean up empty slices to keep JSON clean
	if len(s.Hooks[cfg.PullEvent]) == 0 {
		delete(s.Hooks, cfg.PullEvent)
	}
	if len(s.Hooks[cfg.PushEvent]) == 0 {
		delete(s.Hooks, cfg.PushEvent)
	}

	return true
}

// AutoSyncStatus returns the current state of auto-sync hooks.
func (s *Settings) AutoSyncStatus() AutoSyncStatus {
	hasStart := HasClaudeSyncHook(s.Hooks[EventSessionStart])
	hasStop := HasClaudeSyncHook(s.Hooks[EventStop])
	return AutoSyncStatus{
		Enabled:         hasStart || hasStop,
		HasSessionStart: hasStart,
		HasStop:         hasStop,
	}
}
