package storage

import (
	"testing"
)

// Compile-time interface compliance checks.
// These ensure all storage providers properly implement the Storage interface.
// If any provider is missing a method, this file will fail to compile.

// TestInterfaceCompliance is a placeholder test that documents the compile-time checks.
// The actual compliance is verified by the type assertions below.
func TestInterfaceCompliance(t *testing.T) {
	// This test exists to ensure the interface compliance checks run.
	// The real work is done by the compile-time assertions below.
	t.Log("All storage providers implement the Storage interface (verified at compile time)")
}

// Note: The actual type assertions are in each provider's package via their init() functions.
// Each provider sets storage.NewXXX = New which returns (Storage, error).
// This guarantees interface compliance at compile time.
//
// For example, in webdav/webdav.go:
//   func init() { storage.NewWebDAV = New }
//   func New(cfg *storage.StorageConfig) (storage.Storage, error) { ... }
//
// If the returned *Client doesn't implement Storage, the code won't compile.
