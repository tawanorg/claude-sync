package sync

import (
	"bytes"
	"os"
	"strings"
)

// PathMapper handles bidirectional translation between local relative paths
// (which embed the machine's home directory) and normalized remote keys
// (which use ${HOME} as a portable placeholder).
//
// Claude Code stores per-project data under ~/.claude/projects/ using directory
// names derived from the project's absolute path. For example:
//
//	/Users/merv/nexura  →  projects/-Users-merv-nexura/
//	/Users/alice/nexura →  projects/-Users-alice-nexura/
//
// The encoding replaces "/" with "-" and "/." with "--" in the absolute path.
// This means different machines with different home directories create different
// project directory names for the same project, breaking cross-device sync.
//
// PathMapper normalizes the home directory portion of these directory names
// to "${HOME}" before upload, and resolves it back to the local home directory
// on download. This allows two machines to share project data seamlessly.
type PathMapper struct {
	homeDir     string // e.g., "/Users/merv"
	homeDirSlug string // e.g., "-Users-merv" (homeDir encoded as Claude Code dir name)
}

// NewPathMapper creates a PathMapper for the given home directory.
func NewPathMapper(homeDir string) *PathMapper {
	homeDir = strings.TrimRight(homeDir, "/")
	return &PathMapper{
		homeDir:     homeDir,
		homeDirSlug: encodePathSegment(homeDir),
	}
}

// NewPathMapperFromEnv creates a PathMapper using the current user's home directory.
func NewPathMapperFromEnv() *PathMapper {
	homeDir, _ := os.UserHomeDir()
	return NewPathMapper(homeDir)
}

const homePlaceholder = "${HOME}"

// encodePathSegment converts an absolute path to the Claude Code directory name
// encoding: "/" becomes "-" and "/." becomes "--".
func encodePathSegment(absPath string) string {
	// Claude Code's encoding: replace /. with -- first, then / with -
	result := strings.ReplaceAll(absPath, "/.", "--")
	result = strings.ReplaceAll(result, "/", "-")
	return result
}

// NormalizeRemoteKey converts a local relative path to a normalized remote key
// by replacing the home directory slug with ${HOME} in project directory names.
//
// Example:
//
//	"projects/-Users-merv-nexura/memory/MEMORY.md"
//	→ "projects/${HOME}-nexura/memory/MEMORY.md"
//
// Non-project paths pass through unchanged.
func (pm *PathMapper) NormalizeRemoteKey(localRelPath string) string {
	if pm.homeDir == "" || !pm.IsProjectPath(localRelPath) {
		return localRelPath
	}

	// Extract: "projects/" + dirName + "/" + rest
	// Or just: "projects/" + dirName (no subpath)
	after := strings.TrimPrefix(localRelPath, "projects/")
	slashIdx := strings.Index(after, "/")

	var dirName, rest string
	if slashIdx == -1 {
		dirName = after
		rest = ""
	} else {
		dirName = after[:slashIdx]
		rest = after[slashIdx:] // includes leading "/"
	}

	// Replace the home dir slug prefix in the directory name.
	// We must ensure it's a proper prefix match (followed by "-" or end of string)
	// to avoid false positives like -Users-merv matching -Users-mervynlally.
	normalized := pm.replaceSlugPrefix(dirName, pm.homeDirSlug, homePlaceholder)
	if normalized == dirName {
		// No match — this project dir doesn't belong to our home directory.
		// Pass through unchanged.
		return localRelPath
	}

	return "projects/" + normalized + rest
}

// ResolveLocalPath converts a normalized remote key back to a local relative path
// by replacing ${HOME} with the local home directory slug.
//
// Example (on a machine with homeDir="/Users/alice"):
//
//	"projects/${HOME}-nexura/memory/MEMORY.md"
//	→ "projects/-Users-alice-nexura/memory/MEMORY.md"
//
// Keys without ${HOME} (legacy format) pass through unchanged.
func (pm *PathMapper) ResolveLocalPath(normalizedKey string) string {
	if !strings.Contains(normalizedKey, homePlaceholder) {
		return normalizedKey
	}

	if !pm.IsProjectPath(normalizedKey) {
		return normalizedKey
	}

	after := strings.TrimPrefix(normalizedKey, "projects/")
	slashIdx := strings.Index(after, "/")

	var dirName, rest string
	if slashIdx == -1 {
		dirName = after
		rest = ""
	} else {
		dirName = after[:slashIdx]
		rest = after[slashIdx:]
	}

	// Replace ${HOME} with the local home dir slug
	resolved := strings.Replace(dirName, homePlaceholder, pm.homeDirSlug, 1)

	return "projects/" + resolved + rest
}

// NormalizeContent replaces absolute home directory paths with ${HOME} in file
// content. This is the same approach used by NormalizeMCPPaths in mcp.go.
func (pm *PathMapper) NormalizeContent(data []byte) []byte {
	if pm.homeDir == "" {
		return data
	}
	return bytes.ReplaceAll(data, []byte(pm.homeDir), []byte(homePlaceholder))
}

// ResolveContent replaces ${HOME} placeholders with the local home directory
// in file content. This is the same approach used by ResolveMCPPaths in mcp.go.
func (pm *PathMapper) ResolveContent(data []byte) []byte {
	if pm.homeDir == "" {
		return data
	}
	return bytes.ReplaceAll(data, []byte(homePlaceholder), []byte(pm.homeDir))
}

// IsProjectPath returns true if the relative path is under the projects/ directory.
func (pm *PathMapper) IsProjectPath(relPath string) bool {
	return strings.HasPrefix(relPath, "projects/")
}

// replaceSlugPrefix replaces oldPrefix with newPrefix in slug, but only if
// oldPrefix appears as a proper prefix — meaning it's followed by "-" (next
// path segment) or is the entire slug. This prevents "-Users-merv" from
// matching "-Users-mervynlally".
func (pm *PathMapper) replaceSlugPrefix(slug, oldPrefix, newPrefix string) string {
	if !strings.HasPrefix(slug, oldPrefix) {
		return slug
	}

	remainder := slug[len(oldPrefix):]

	// Valid boundary: end of string, or "-" (next path segment)
	if remainder == "" || remainder[0] == '-' {
		return newPrefix + remainder
	}

	// Not a proper boundary — the prefix matched inside a longer token
	return slug
}
