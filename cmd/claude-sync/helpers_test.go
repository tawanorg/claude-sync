package main

import (
	"runtime"
	"testing"
)

func TestTruncatePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		maxLen   int
		expected string
	}{
		{"short path unchanged", "/a/b", 10, "/a/b"},
		{"exact length unchanged", "12345", 5, "12345"},
		{"long path truncated", "/very/long/path/to/file.txt", 15, ".../to/file.txt"},
		{"minimal truncation", "abcdef", 5, "...ef"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncatePath(tt.path, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncatePath(%q, %d) = %q, want %q", tt.path, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name     string
		size     int64
		expected string
	}{
		{"bytes", 500, "500 B"},
		{"zero bytes", 0, "0 B"},
		{"kilobytes", 1536, "1.5 KB"},
		{"megabytes", 2 * 1024 * 1024, "2.0 MB"},
		{"gigabytes", 3 * 1024 * 1024 * 1024, "3.0 GB"},
		{"exact 1 KB", 1024, "1.0 KB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSize(tt.size)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.size, result, tt.expected)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		v1       string
		v2       string
		expected int
	}{
		{"equal versions", "1.0.0", "1.0.0", 0},
		{"v1 less than v2 major", "1.0.0", "2.0.0", -1},
		{"v1 greater than v2 major", "2.0.0", "1.0.0", 1},
		{"v1 less than v2 minor", "1.1.0", "1.2.0", -1},
		{"v1 greater than v2 minor", "1.3.0", "1.2.0", 1},
		{"v1 less than v2 patch", "1.0.1", "1.0.2", -1},
		{"v1 greater than v2 patch", "1.0.3", "1.0.2", 1},
		{"complex comparison", "1.6.3", "1.5.9", 1},
		{"short version strings", "1.0", "1.0.0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareVersions(tt.v1, tt.v2)
			if result != tt.expected {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, result, tt.expected)
			}
		})
	}
}

func TestGetBinaryName(t *testing.T) {
	result := getBinaryName("1.0.0")
	expected := "claude-sync-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		expected += ".exe"
	}
	if result != expected {
		t.Errorf("getBinaryName(\"1.0.0\") = %q, want %q", result, expected)
	}
}
