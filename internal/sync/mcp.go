package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MCPServers maps server names to their raw JSON configuration.
// Using json.RawMessage preserves unknown fields within each server entry.
type MCPServers map[string]json.RawMessage

// MCPMergeResult describes the outcome of a three-way merge of MCP server configs.
type MCPMergeResult struct {
	Merged    MCPServers
	Added     []string // server keys added from remote
	Updated   []string // server keys updated from remote
	Kept      []string // server keys kept as-is
	Conflicts []MCPConflict
}

// MCPConflict represents a merge conflict for a single MCP server key.
type MCPConflict struct {
	Key    string
	Local  json.RawMessage
	Remote json.RawMessage
}

// MCPPushResult describes the outcome of pushing MCP configs.
type MCPPushResult struct {
	ServersPushed int
	Unchanged     bool
}

// MCPPullResult describes the outcome of pulling MCP configs.
type MCPPullResult struct {
	Added     []string
	Updated   []string
	Kept      []string
	Conflicts []MCPConflict
	NoRemote  bool
}

// ReadMCPServers reads the mcpServers key from a claude.json file.
// Returns nil (not an error) if the file doesn't exist or has no mcpServers key.
func ReadMCPServers(path string) (MCPServers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	mcpRaw, ok := raw["mcpServers"]
	if !ok || len(mcpRaw) == 0 {
		return nil, nil
	}

	var servers MCPServers
	if err := json.Unmarshal(mcpRaw, &servers); err != nil {
		return nil, fmt.Errorf("failed to parse mcpServers: %w", err)
	}

	return servers, nil
}

// WriteMCPServers writes the mcpServers key into a claude.json file,
// preserving all other keys. Creates a .bak backup before writing.
func WriteMCPServers(path string, servers MCPServers) error {
	// Read existing file (or start with empty object)
	var raw map[string]json.RawMessage

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		raw = make(map[string]json.RawMessage)
	} else {
		// Create backup
		if err := os.WriteFile(path+".bak", data, 0600); err != nil {
			return fmt.Errorf("failed to create backup: %w", err)
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
	}

	// Marshal the servers and set the key
	serversJSON, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("failed to serialize mcpServers: %w", err)
	}
	raw["mcpServers"] = serversJSON

	// Write back with indentation matching Claude Code's format
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize claude.json: %w", err)
	}

	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	return nil
}

// NormalizeMCPPaths replaces machine-specific absolute paths with portable
// variable references (e.g., /Users/alice -> ${HOME}) in the raw JSON bytes.
func NormalizeMCPPaths(data []byte, homeDir string) []byte {
	if homeDir == "" {
		return data
	}
	// Ensure no trailing slash for consistent replacement
	homeDir = strings.TrimRight(homeDir, "/")
	return bytes.ReplaceAll(data, []byte(homeDir), []byte("${HOME}"))
}

// ResolveMCPPaths replaces portable variable references with the local
// machine's paths (e.g., ${HOME} -> /Users/bob) in the raw JSON bytes.
func ResolveMCPPaths(data []byte, homeDir string) []byte {
	if homeDir == "" {
		return data
	}
	homeDir = strings.TrimRight(homeDir, "/")
	return bytes.ReplaceAll(data, []byte("${HOME}"), []byte(homeDir))
}

// NormalizeMCPServers normalizes all path references in the server configs.
func NormalizeMCPServers(servers MCPServers, homeDir string) (MCPServers, error) {
	if servers == nil {
		return nil, nil
	}
	data, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	normalized := NormalizeMCPPaths(data, homeDir)
	var result MCPServers
	if err := json.Unmarshal(normalized, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ResolveMCPServers resolves all variable references in the server configs.
func ResolveMCPServers(servers MCPServers, homeDir string) (MCPServers, error) {
	if servers == nil {
		return nil, nil
	}
	data, err := json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	resolved := ResolveMCPPaths(data, homeDir)
	var result MCPServers
	if err := json.Unmarshal(resolved, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// mcpServerEqual compares two server configs by their canonical JSON representation.
func mcpServerEqual(a, b json.RawMessage) bool {
	// Compact both to canonical form for comparison
	var aBuf, bBuf bytes.Buffer
	if err := json.Compact(&aBuf, a); err != nil {
		return false
	}
	if err := json.Compact(&bBuf, b); err != nil {
		return false
	}
	return bytes.Equal(aBuf.Bytes(), bBuf.Bytes())
}

// MergeMCPServers performs a three-way merge of MCP server configurations.
// baseline is the last-synced state (may be nil for first sync).
func MergeMCPServers(local, remote, baseline MCPServers) *MCPMergeResult {
	result := &MCPMergeResult{
		Merged: make(MCPServers),
	}

	// Collect all keys across all three sets
	allKeys := make(map[string]bool)
	for k := range local {
		allKeys[k] = true
	}
	for k := range remote {
		allKeys[k] = true
	}
	for k := range baseline {
		allKeys[k] = true
	}

	for key := range allKeys {
		l, inLocal := local[key]
		r, inRemote := remote[key]
		b, inBaseline := baseline[key]

		switch {
		// Only in remote (new from another device)
		case !inLocal && inRemote && !inBaseline:
			result.Merged[key] = r
			result.Added = append(result.Added, key)

		// Only in local (not yet synced)
		case inLocal && !inRemote && !inBaseline:
			result.Merged[key] = l
			result.Kept = append(result.Kept, key)

		// In all three
		case inLocal && inRemote && inBaseline:
			localMatchesBaseline := mcpServerEqual(l, b)
			remoteMatchesBaseline := mcpServerEqual(r, b)

			switch {
			case localMatchesBaseline && remoteMatchesBaseline:
				// No changes
				result.Merged[key] = l
				result.Kept = append(result.Kept, key)
			case localMatchesBaseline && !remoteMatchesBaseline:
				// Only remote changed -> update from remote
				result.Merged[key] = r
				result.Updated = append(result.Updated, key)
			case !localMatchesBaseline && remoteMatchesBaseline:
				// Only local changed -> keep local
				result.Merged[key] = l
				result.Kept = append(result.Kept, key)
			default:
				// Both changed
				if mcpServerEqual(l, r) {
					// Changed identically
					result.Merged[key] = l
					result.Kept = append(result.Kept, key)
				} else {
					// Conflict: keep local, report conflict
					result.Merged[key] = l
					result.Conflicts = append(result.Conflicts, MCPConflict{
						Key:    key,
						Local:  l,
						Remote: r,
					})
				}
			}

		// In local and remote, no baseline (first sync)
		case inLocal && inRemote && !inBaseline:
			if mcpServerEqual(l, r) {
				result.Merged[key] = l
				result.Kept = append(result.Kept, key)
			} else {
				// Conflict: keep local, report
				result.Merged[key] = l
				result.Conflicts = append(result.Conflicts, MCPConflict{
					Key:    key,
					Local:  l,
					Remote: r,
				})
			}

		// In baseline and remote, deleted locally
		case !inLocal && inRemote && inBaseline:
			if mcpServerEqual(r, b) {
				// Remote unchanged, honor local deletion -> omit
			} else {
				// Remote changed after local deletion -> conflict
				result.Conflicts = append(result.Conflicts, MCPConflict{
					Key:    key,
					Remote: r,
				})
			}

		// In baseline and local, deleted remotely
		case inLocal && !inRemote && inBaseline:
			if mcpServerEqual(l, b) {
				// Local unchanged, honor remote deletion -> omit
			} else {
				// Local changed after remote deletion -> conflict
				result.Merged[key] = l
				result.Conflicts = append(result.Conflicts, MCPConflict{
					Key:   key,
					Local: l,
				})
			}

		// Only in baseline (deleted on both sides) -> omit
		case !inLocal && !inRemote && inBaseline:
			// Both deleted, nothing to do

		// Only in remote, was in baseline (shouldn't happen but handle gracefully)
		default:
			if inRemote {
				result.Merged[key] = r
				result.Added = append(result.Added, key)
			} else if inLocal {
				result.Merged[key] = l
				result.Kept = append(result.Kept, key)
			}
		}
	}

	return result
}

// HashMCPServers computes a hash of the normalized MCP servers for change detection.
func HashMCPServers(servers MCPServers) (string, error) {
	if servers == nil {
		return "", nil
	}
	data, err := json.Marshal(servers)
	if err != nil {
		return "", err
	}
	// Compact to canonical form
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return "", err
	}
	return hashBytes(buf.Bytes()), nil
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
