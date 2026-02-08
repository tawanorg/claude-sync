package storage

import (
	"context"
	"fmt"
	"time"
)

// Provider represents a storage provider type
type Provider string

const (
	ProviderR2  Provider = "r2"
	ProviderS3  Provider = "s3"
	ProviderGCS Provider = "gcs"
)

// ObjectInfo contains metadata about a stored object
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// Storage defines the interface for cloud storage operations
type Storage interface {
	// Upload stores data with the given key
	Upload(ctx context.Context, key string, data []byte) error

	// Download retrieves data for the given key
	Download(ctx context.Context, key string) ([]byte, error)

	// Delete removes the object with the given key
	Delete(ctx context.Context, key string) error

	// List returns all objects with the given prefix
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Head returns metadata for the given key without downloading content
	Head(ctx context.Context, key string) (*ObjectInfo, error)

	// BucketExists checks if the configured bucket exists
	BucketExists(ctx context.Context) (bool, error)
}

// New creates a new Storage instance based on the provided configuration
func New(cfg *StorageConfig) (Storage, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	switch cfg.Provider {
	case ProviderR2:
		return NewR2(cfg)
	case ProviderS3:
		return NewS3(cfg)
	case ProviderGCS:
		return NewGCS(cfg)
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s", cfg.Provider)
	}
}

// NewR2 creates a new R2 storage adapter (implemented in r2/r2.go)
var NewR2 func(cfg *StorageConfig) (Storage, error)

// NewS3 creates a new S3 storage adapter (implemented in s3/s3.go)
var NewS3 func(cfg *StorageConfig) (Storage, error)

// NewGCS creates a new GCS storage adapter (implemented in gcs/gcs.go)
var NewGCS func(cfg *StorageConfig) (Storage, error)
