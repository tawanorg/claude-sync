package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	// Test data
	binaryData := []byte("test binary content for checksum verification")
	sum := sha256.Sum256(binaryData)
	correctHash := hex.EncodeToString(sum[:])
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	tests := []struct {
		name        string
		checksumTxt string
		assetName   string
		data        []byte
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid checksum",
			checksumTxt: fmt.Sprintf("%s  claude-sync-darwin-arm64\n", correctHash),
			assetName:   "claude-sync-darwin-arm64",
			data:        binaryData,
			wantErr:     false,
		},
		{
			name:        "valid checksum with binary mode marker",
			checksumTxt: fmt.Sprintf("%s *claude-sync-linux-x64\n", correctHash),
			assetName:   "claude-sync-linux-x64",
			data:        binaryData,
			wantErr:     false,
		},
		{
			name:        "checksum mismatch",
			checksumTxt: fmt.Sprintf("%s  claude-sync-darwin-arm64\n", wrongHash),
			assetName:   "claude-sync-darwin-arm64",
			data:        binaryData,
			wantErr:     true,
			errContains: "mismatch",
		},
		{
			name:        "asset not in checksums file",
			checksumTxt: fmt.Sprintf("%s  claude-sync-darwin-arm64\n", correctHash),
			assetName:   "claude-sync-linux-arm64",
			data:        binaryData,
			wantErr:     true,
			errContains: "no entry",
		},
		{
			name:        "empty checksums file",
			checksumTxt: "",
			assetName:   "claude-sync-darwin-arm64",
			data:        binaryData,
			wantErr:     true,
			errContains: "no entry",
		},
		{
			name: "multiple entries finds correct one",
			checksumTxt: fmt.Sprintf(`%s  claude-sync-darwin-arm64
abc123  claude-sync-darwin-x64
def456  claude-sync-linux-arm64
`, correctHash),
			assetName: "claude-sync-darwin-arm64",
			data:      binaryData,
			wantErr:   false,
		},
		{
			name:        "case insensitive hash comparison",
			checksumTxt: fmt.Sprintf("%s  claude-sync-darwin-arm64\n", "ABCD"+correctHash[4:]),
			assetName:   "claude-sync-darwin-arm64",
			data:        binaryData,
			wantErr:     true, // hash won't match but should attempt comparison
			errContains: "mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that serves the checksums.txt
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.checksumTxt))
			}))
			defer server.Close()

			release := &GitHubRelease{
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:               "checksums.txt",
						BrowserDownloadURL: server.URL + "/checksums.txt",
					},
					{
						Name:               tt.assetName,
						BrowserDownloadURL: server.URL + "/" + tt.assetName,
					},
				},
			}

			err := verifyChecksum(release, tt.assetName, tt.data)
			if tt.wantErr {
				if err == nil {
					t.Error("verifyChecksum() expected error, got nil")
					return
				}
				if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("verifyChecksum() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("verifyChecksum() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestVerifyChecksumNoChecksumsFile(t *testing.T) {
	release := &GitHubRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               "claude-sync-darwin-arm64",
				BrowserDownloadURL: "https://example.com/binary",
			},
			// No checksums.txt asset
		},
	}

	// Should warn but not fail when checksums.txt is missing (older release)
	err := verifyChecksum(release, "claude-sync-darwin-arm64", []byte("data"))
	if err != nil {
		t.Errorf("verifyChecksum() with no checksums.txt should not error (old release), got: %v", err)
	}
}

func TestVerifyChecksumDownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	release := &GitHubRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               "checksums.txt",
				BrowserDownloadURL: server.URL + "/checksums.txt",
			},
		},
	}

	err := verifyChecksum(release, "claude-sync-darwin-arm64", []byte("data"))
	if err == nil {
		t.Error("verifyChecksum() expected error when checksums.txt download fails")
	}
}

// helper function to check if string contains substring
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
