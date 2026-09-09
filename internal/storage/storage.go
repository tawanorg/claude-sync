package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound distinguishes a missing object from transient failures
// (timeouts, throttling, 5xx). Adapters wrap their provider-specific
// not-found errors with it; callers that must not confuse "absent" with
// "unreachable" — such as the history merge-on-push, where the difference is
// clobbering the bucket union — check it via IsNotFound.
var ErrNotFound = errors.New("object not found")

// IsNotFound reports whether err indicates the object does not exist.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// Provider represents a storage provider type
type Provider string

const (
	ProviderR2     Provider = "r2"
	ProviderS3     Provider = "s3"
	ProviderGCS    Provider = "gcs"
	ProviderWebDAV Provider = "webdav"
)

// MaxDownloadSize is the maximum allowed size for a single downloaded object.
// This prevents memory exhaustion from oversized or malicious remote files.
//
// Was 100MB. Raised after a real transcript from a single long-running
// session hit 887MB (and a sibling in the same project 930MB) — legitimate
// conversation history, not corruption. 2GB leaves headroom for that pattern
// to continue while still bounding worst-case memory use for a single
// in-memory download on any machine this runs on.
const MaxDownloadSize = 2 * 1024 * 1024 * 1024

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

	// DeleteBatch removes multiple objects in a single operation
	DeleteBatch(ctx context.Context, keys []string) error

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
	case ProviderWebDAV:
		return NewWebDAV(cfg)
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

// NewWebDAV creates a new WebDAV storage adapter (implemented in webdav/webdav.go)
var NewWebDAV func(cfg *StorageConfig) (Storage, error)
