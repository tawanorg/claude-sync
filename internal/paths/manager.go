// Package paths provides business logic for managing sync paths and exclude filters.
// It separates the path management concerns from CLI and config layers.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultSyncPaths are the built-in paths synced by default.
var DefaultSyncPaths = []string{
	"CLAUDE.md",
	"settings.json",
	"settings.local.json",
	"agents",
	"commands",
	"skills",
	"plugins",
	"projects",
	"plans",
	"tasks",
	"history.jsonl",
	"rules",
	"workflows",
}

// Manager handles sync path and exclude filter operations.
// It provides the business logic separate from config persistence.
type Manager struct {
	syncPaths  []string
	excludes   []string
	claudeDir  string
	defaultSet map[string]struct{}
}

// NewManager creates a Manager with the given paths and excludes.
// If claudeDir is empty, it defaults to ~/.claude.
func NewManager(syncPaths, excludes []string, claudeDir string) *Manager {
	if claudeDir == "" {
		home, _ := os.UserHomeDir()
		claudeDir = filepath.Join(home, ".claude")
	}

	// Build default set for quick lookup
	defaultSet := make(map[string]struct{}, len(DefaultSyncPaths))
	for _, p := range DefaultSyncPaths {
		defaultSet[p] = struct{}{}
	}

	// Use defaults if no custom paths
	if len(syncPaths) == 0 {
		syncPaths = append([]string{}, DefaultSyncPaths...)
	}

	return &Manager{
		syncPaths:  syncPaths,
		excludes:   excludes,
		claudeDir:  claudeDir,
		defaultSet: defaultSet,
	}
}

// SyncPaths returns the current sync paths.
func (m *Manager) SyncPaths() []string {
	return m.syncPaths
}

// Excludes returns the current exclude patterns.
func (m *Manager) Excludes() []string {
	return m.excludes
}

// AddResult contains the result of an Add operation.
type AddResult struct {
	Added           bool
	AlreadyExists   bool
	PathMissing     bool
	ExcludesRemoved int
}

// Add adds a path to the sync list.
// If the path was previously excluded, conflicting excludes are removed.
// Returns details about what changed.
func (m *Manager) Add(path string) AddResult {
	result := AddResult{}

	// Check if already in list
	for _, p := range m.syncPaths {
		if p == path {
			result.AlreadyExists = true
			return result
		}
	}

	// Check if path exists
	fullPath := filepath.Join(m.claudeDir, path)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		result.PathMissing = true
	}

	// Add to sync paths
	m.syncPaths = append(m.syncPaths, path)
	result.Added = true

	// Remove conflicting excludes
	newExcludes := m.excludes[:0:0]
	for _, e := range m.excludes {
		base := strings.TrimSuffix(strings.TrimSuffix(e, "/**"), "/*")
		if base == path || e == path {
			result.ExcludesRemoved++
			continue
		}
		newExcludes = append(newExcludes, e)
	}
	m.excludes = newExcludes

	return result
}

// RemoveResult contains the result of a Remove operation.
type RemoveResult struct {
	Removed      bool
	NotFound     bool
	ExcludeAdded string
	IsDefault    bool
}

// Remove removes a path from the sync list.
// Default paths are also added to excludes so they stay off after reset.
// Custom paths are simply removed.
func (m *Manager) Remove(path string) RemoveResult {
	result := RemoveResult{}

	// Find the path
	found := -1
	for i, p := range m.syncPaths {
		if p == path {
			found = i
			break
		}
	}

	if found == -1 {
		result.NotFound = true
		return result
	}

	// Remove from sync paths
	m.syncPaths = append(m.syncPaths[:found], m.syncPaths[found+1:]...)
	result.Removed = true

	// Check if it's a default path
	_, isDefault := m.defaultSet[path]
	result.IsDefault = isDefault

	// For default paths, add exclude so it survives reset
	if isDefault {
		excludePattern := path
		// Check if it's a directory
		fullPath := filepath.Join(m.claudeDir, path)
		if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
			excludePattern = path + "/*"
		}

		// Check if already excluded
		alreadyExcluded := false
		for _, e := range m.excludes {
			if e == excludePattern {
				alreadyExcluded = true
				break
			}
		}
		if !alreadyExcluded {
			m.excludes = append(m.excludes, excludePattern)
			result.ExcludeAdded = excludePattern
		}
	}

	return result
}

// AddExcludeResult contains the result of an AddExclude operation.
type AddExcludeResult struct {
	Added         bool
	AlreadyExists bool
	IsSyncPath    bool
}

// AddExclude adds a glob pattern to the exclude list.
// Returns an error indicator if the pattern matches a top-level sync path
// (user should use Remove instead).
func (m *Manager) AddExclude(pattern string) AddExcludeResult {
	result := AddExcludeResult{}

	// Check if it matches a sync path (should use Remove instead)
	for _, p := range m.syncPaths {
		if p == pattern {
			result.IsSyncPath = true
			return result
		}
	}

	// Check if already excluded
	for _, e := range m.excludes {
		if e == pattern {
			result.AlreadyExists = true
			return result
		}
	}

	m.excludes = append(m.excludes, pattern)
	result.Added = true
	return result
}

// RemoveExcludeResult contains the result of a RemoveExclude operation.
type RemoveExcludeResult struct {
	Removed  bool
	NotFound bool
}

// RemoveExclude removes a pattern from the exclude list.
func (m *Manager) RemoveExclude(pattern string) RemoveExcludeResult {
	result := RemoveExcludeResult{}

	found := -1
	for i, e := range m.excludes {
		if e == pattern {
			found = i
			break
		}
	}

	if found == -1 {
		result.NotFound = true
		return result
	}

	m.excludes = append(m.excludes[:found], m.excludes[found+1:]...)
	result.Removed = true
	return result
}

// Reset restores default sync paths and clears all excludes.
func (m *Manager) Reset() {
	m.syncPaths = append([]string{}, DefaultSyncPaths...)
	m.excludes = nil
}

// IsDefault returns true if the path is a built-in default.
func (m *Manager) IsDefault(path string) bool {
	_, ok := m.defaultSet[path]
	return ok
}

// HasPath returns true if the path is in the current sync list.
func (m *Manager) HasPath(path string) bool {
	for _, p := range m.syncPaths {
		if p == path {
			return true
		}
	}
	return false
}

// HasExclude returns true if the pattern is in the exclude list.
func (m *Manager) HasExclude(pattern string) bool {
	for _, e := range m.excludes {
		if e == pattern {
			return true
		}
	}
	return false
}

// Status returns information about the current sync configuration.
type Status struct {
	SyncPaths       []string
	Excludes        []string
	CustomPaths     []string // Non-default paths
	RemovedDefaults []string // Defaults that were removed (in excludes)
	IsCustomized    bool
}

// Status returns the current sync configuration status.
func (m *Manager) Status() Status {
	s := Status{
		SyncPaths: m.syncPaths,
		Excludes:  m.excludes,
	}

	// Find custom paths (not in defaults)
	for _, p := range m.syncPaths {
		if _, isDefault := m.defaultSet[p]; !isDefault {
			s.CustomPaths = append(s.CustomPaths, p)
		}
	}

	// Find removed defaults (in excludes)
	for _, e := range m.excludes {
		base := strings.TrimSuffix(strings.TrimSuffix(e, "/**"), "/*")
		if _, isDefault := m.defaultSet[base]; isDefault {
			s.RemovedDefaults = append(s.RemovedDefaults, base)
		}
	}

	s.IsCustomized = len(s.CustomPaths) > 0 || len(s.RemovedDefaults) > 0 || len(m.excludes) > 0
	return s
}
