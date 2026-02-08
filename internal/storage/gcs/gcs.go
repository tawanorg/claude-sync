package gcs

import (
	"context"
	"fmt"
	"io"
	"os"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	appstorage "github.com/tawanorg/claude-sync/internal/storage"
)

func init() {
	appstorage.NewGCS = New
}

// Client implements the storage.Storage interface for Google Cloud Storage
type Client struct {
	client *storage.Client
	bucket string
}

// New creates a new GCS storage client
func New(cfg *appstorage.StorageConfig) (appstorage.Storage, error) {
	ctx := context.Background()

	var opts []option.ClientOption

	// Configure authentication
	if cfg.CredentialsFile != "" {
		// Expand ~ in path
		credPath := cfg.CredentialsFile
		if len(credPath) > 0 && credPath[0] == '~' {
			home, _ := os.UserHomeDir()
			credPath = home + credPath[1:]
		}
		opts = append(opts, option.WithCredentialsFile(credPath))
	} else if cfg.CredentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(cfg.CredentialsJSON)))
	}
	// If no credentials specified, the client will use Application Default Credentials

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &Client{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// Upload stores data with the given key
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	wc := c.client.Bucket(c.bucket).Object(key).NewWriter(ctx)
	wc.ContentType = "application/octet-stream"

	if _, err := wc.Write(data); err != nil {
		wc.Close()
		return fmt.Errorf("failed to write %s: %w", key, err)
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}

	return nil
}

// Download retrieves data for the given key
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	rc, err := c.client.Bucket(c.bucket).Object(key).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", key, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", key, err)
	}

	return data, nil
}

// Delete removes the object with the given key
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.client.Bucket(c.bucket).Object(key).Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

// DeleteBatch removes multiple objects concurrently
func (c *Client) DeleteBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	const maxConcurrency = 10

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			if err := c.client.Bucket(c.bucket).Object(key).Delete(ctx); err != nil {
				return fmt.Errorf("failed to delete %s: %w", key, err)
			}
			return nil
		})
	}

	return g.Wait()
}

// List returns all objects with the given prefix
func (c *Client) List(ctx context.Context, prefix string) ([]appstorage.ObjectInfo, error) {
	var objects []appstorage.ObjectInfo

	query := &storage.Query{}
	if prefix != "" {
		query.Prefix = prefix
	}

	it := c.client.Bucket(c.bucket).Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		objects = append(objects, appstorage.ObjectInfo{
			Key:          attrs.Name,
			Size:         attrs.Size,
			LastModified: attrs.Updated,
			ETag:         attrs.Etag,
		})
	}

	return objects, nil
}

// Head returns metadata for the given key without downloading content
func (c *Client) Head(ctx context.Context, key string) (*appstorage.ObjectInfo, error) {
	attrs, err := c.client.Bucket(c.bucket).Object(key).Attrs(ctx)
	if err != nil {
		return nil, err
	}

	return &appstorage.ObjectInfo{
		Key:          attrs.Name,
		Size:         attrs.Size,
		LastModified: attrs.Updated,
		ETag:         attrs.Etag,
	}, nil
}

// BucketExists checks if the configured bucket exists
func (c *Client) BucketExists(ctx context.Context) (bool, error) {
	_, err := c.client.Bucket(c.bucket).Attrs(ctx)
	if err != nil {
		return false, nil
	}
	return true, nil
}
