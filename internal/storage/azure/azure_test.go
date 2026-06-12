package azure

import (
	"testing"

	storage "github.com/tawanorg/claude-sync/internal/storage"
)

func TestNew_ValidURL(t *testing.T) {
	cfg := &storage.StorageConfig{
		Provider: storage.ProviderAzure,
		AzureURL: "https://pskyops.blob.core.windows.net/claude-sync?sv=2021-06-08&sig=fakesig",
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("New() returned nil provider")
	}
	ap := p.(*AzureProvider)
	if ap.containerName != "claude-sync" {
		t.Errorf("containerName = %q, want %q", ap.containerName, "claude-sync")
	}
}

func TestNew_MissingContainer(t *testing.T) {
	cfg := &storage.StorageConfig{
		Provider: storage.ProviderAzure,
		AzureURL: "https://pskyops.blob.core.windows.net/?sv=2021-06-08&sig=fakesig",
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("New() expected error for missing container name, got nil")
	}
}

func TestNew_InvalidURL(t *testing.T) {
	cfg := &storage.StorageConfig{
		Provider: storage.ProviderAzure,
		AzureURL: "://not-a-url",
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("New() expected error for invalid URL, got nil")
	}
}
