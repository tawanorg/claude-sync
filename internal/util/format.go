package util

import (
	"fmt"
	"runtime"
	"strings"
)

// TruncatePath shortens a file path to maxLen characters,
// prefixing with "..." if truncation is needed.
func TruncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}

// FormatSize returns a human-readable string for a byte count.
func FormatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// CompareVersions performs a simple semver comparison.
// Returns -1 if v1 < v2, 0 if equal, 1 if v1 > v2.
func CompareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	for i := 0; i < 3; i++ {
		var p1, p2 int
		if i < len(parts1) {
			_, _ = fmt.Sscanf(parts1[i], "%d", &p1)
		}
		if i < len(parts2) {
			_, _ = fmt.Sscanf(parts2[i], "%d", &p2)
		}

		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}

	return 0
}

// GetBinaryName returns the platform-specific binary name for claude-sync.
func GetBinaryName(version string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	name := fmt.Sprintf("claude-sync-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}
