package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/tawanorg/claude-sync/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	ConfigDir  = ".claude-sync"
	ConfigFile = "config.yaml"
	StateFile  = "state.json"
	AgeKeyFile = "age-key.txt"

	// MCPRemoteKey is the remote storage key for synced MCP server configs.
	// The _external/ prefix separates it from ~/.claude/-relative files.
	MCPRemoteKey = "_external/mcp-servers.json"

	// Sync scopes control which subset of ~/.claude is synced.
	// ScopeFull (default) syncs everything in SyncPaths; ScopeSessions limits
	// syncing to portable conversation data only.
	ScopeFull     = "full"
	ScopeSessions = "sessions"
)

type Config struct {
	// New storage configuration (preferred)
	Storage *storage.StorageConfig `yaml:"storage,omitempty"`

	// Legacy R2-only fields (for backward compatibility)
	AccountID       string `yaml:"account_id,omitempty"`
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	Bucket          string `yaml:"bucket,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`

	// Common fields
	EncryptionKey string `yaml:"encryption_key_path"`

	// Exclude patterns (glob-style) for paths to skip during sync
	Exclude []string `yaml:"exclude,omitempty"`

	// Scope selects which subset of ~/.claude to sync: "full" (default, empty)
	// or "sessions" (portable conversation data only). See ScopedSyncPaths.
	Scope string `yaml:"scope,omitempty"`

	// SyncPaths overrides the scope-based default paths when non-empty.
	// Use GetEffectiveSyncPaths() to get the actual paths to sync.
	SyncPaths []string `yaml:"sync_paths,omitempty"`

	// MCPSync enables syncing MCP server configs from ~/.claude.json.
	// Pointer type allows distinguishing between unset (nil), enabled (true),
	// and explicitly disabled (false). Nil is treated as disabled for backward
	// compatibility with existing configs.
	MCPSync *bool `yaml:"mcp_sync,omitempty"`

	// PathMap maps local directory prefixes to shared token names so project
	// sessions stay resumable across devices with different layouts.
	// The home directory is always mapped (token HOME); add entries here when
	// project roots differ beyond that, e.g.:
	//   path_map:
	//     ~/work: WORK        # this device keeps projects in ~/work
	// with the other device mapping its own location to the same token:
	//   path_map:
	//     ~/Projects: WORK
	PathMap map[string]string `yaml:"path_map,omitempty"`

	// ClaudeDirOverride allows overriding the default ~/.claude path (for testing)
	ClaudeDirOverride string `yaml:"-"`

	// StateDirOverride allows overriding the state file directory (for testing)
	StateDirOverride string `yaml:"-"`

	// ClaudeJSONOverride allows overriding the ~/.claude.json path (for testing)
	ClaudeJSONOverride string `yaml:"-"`
}

// SyncPaths defines which paths under ~/.claude to sync in the default "full" scope.
var SyncPaths = []string{
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
}

// SessionSyncPaths is the subset synced in the "sessions" scope: portable,
// high-value conversation data and its per-project work state. It deliberately
// excludes plugins/ (which bundles non-portable node_modules and .venv trees),
// along with machine-specific settings, skills, agents, and commands.
var SessionSyncPaths = []string{
	"projects",
	"history.jsonl",
	"tasks",
	"plans",
}

// ScopedSyncPaths returns the sync path set for the given scope. "sessions"
// limits syncing to SessionSyncPaths; "full", empty, or any unrecognized value
// returns the complete SyncPaths so existing configs keep their behavior.
func ScopedSyncPaths(scope string) []string {
	if scope == ScopeSessions {
		return SessionSyncPaths
	}
	return SyncPaths
}

func ConfigDirPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ConfigDir)
}

func ConfigFilePath() string {
	return filepath.Join(ConfigDirPath(), ConfigFile)
}

func StateFilePath() string {
	return filepath.Join(ConfigDirPath(), StateFile)
}

func AgeKeyFilePath() string {
	return filepath.Join(ConfigDirPath(), AgeKeyFile)
}

func ClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// ClaudeJSONPath returns the path to ~/.claude.json where global MCP servers are configured.
func ClaudeJSONPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

func Load() (*Config, error) {
	configPath := ConfigFilePath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config not found: run 'claude-sync init' first")
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Expand ~ in encryption key path
	if cfg.EncryptionKey != "" && cfg.EncryptionKey[0] == '~' {
		home, _ := os.UserHomeDir()
		cfg.EncryptionKey = filepath.Join(home, cfg.EncryptionKey[1:])
	}

	// Expand ~ in path_map keys
	if len(cfg.PathMap) > 0 {
		home, _ := os.UserHomeDir()
		expanded := make(map[string]string, len(cfg.PathMap))
		for p, name := range cfg.PathMap {
			if p != "" && p[0] == '~' {
				p = filepath.Join(home, p[1:])
			}
			expanded[p] = name
		}
		cfg.PathMap = expanded
	}

	// Set default endpoint for Cloudflare R2
	if cfg.Endpoint == "" && cfg.AccountID != "" {
		cfg.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}

	return &cfg, nil
}

func Save(cfg *Config) error {
	configDir := ConfigDirPath()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	configPath := ConfigFilePath()
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func Exists() bool {
	_, err := os.Stat(ConfigFilePath())
	return err == nil
}

// GetStorageConfig returns the storage configuration, migrating from legacy format if needed
func (c *Config) GetStorageConfig() *storage.StorageConfig {
	// If new format is already configured, use it
	if c.Storage != nil && c.Storage.Provider != "" {
		return c.Storage
	}

	// Migrate from legacy R2 format
	return &storage.StorageConfig{
		Provider:        storage.ProviderR2,
		Bucket:          c.Bucket,
		AccountID:       c.AccountID,
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		Endpoint:        c.Endpoint,
	}
}

// IsLegacyConfig returns true if using the legacy R2-only config format
func (c *Config) IsLegacyConfig() bool {
	return c.Storage == nil && c.AccountID != ""
}

// GetEffectiveSyncPaths returns the paths to sync: custom SyncPaths if set,
// otherwise the scope-based defaults.
func (c *Config) GetEffectiveSyncPaths() []string {
	if len(c.SyncPaths) > 0 {
		return c.SyncPaths
	}
	return ScopedSyncPaths(c.Scope)
}

// IsMCPSyncEnabled returns true if MCP sync is explicitly enabled.
// Returns false if MCPSync is nil (unset) or false.
func (c *Config) IsMCPSyncEnabled() bool {
	return c.MCPSync != nil && *c.MCPSync
}

// SetMCPSync sets the MCP sync state. Pass true to enable, false to explicitly disable.
func (c *Config) SetMCPSync(enabled bool) {
	c.MCPSync = &enabled
}

// IsExcluded returns true if the given relative path matches any exclude pattern.
// Patterns support:
//   - Full doublestar glob syntax including ** for recursive matching
//   - Examples: "**/.git/**", "*.tmp", "plugins/cache/**", "projects/*/node_modules/**"
//   - Directory prefix (e.g. "plugins/marketplace" matches everything under it)
//   - Filename glob (e.g. "*.tmp" matches "foo/bar/file.tmp")
func (c *Config) IsExcluded(relPath string) bool {
	// Normalize path separators for consistent matching
	relPath = filepath.ToSlash(relPath)

	for _, pattern := range c.Exclude {
		// Normalize pattern separators
		pattern = filepath.ToSlash(pattern)

		// Use doublestar for full glob matching including ** support
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}

		// Try glob match on filename only (for patterns like "*.tmp")
		// but only if the pattern doesn't contain path separators
		if !strings.Contains(pattern, "/") && (strings.Contains(pattern, "*") || strings.Contains(pattern, "?")) {
			if matched, _ := doublestar.Match(pattern, filepath.Base(relPath)); matched {
				return true
			}
		}

		// Also match if the path starts with the pattern as a directory prefix
		// This lets "plugins/marketplace" exclude everything under that dir
		if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") {
			if len(relPath) > len(pattern) && relPath[:len(pattern)] == pattern &&
				relPath[len(pattern)] == '/' {
				return true
			}
			// Exact match for non-glob patterns
			if relPath == pattern {
				return true
			}
		}
	}
	return false
}
