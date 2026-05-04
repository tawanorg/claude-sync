package webdav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/tawanorg/claude-sync/internal/storage"
)

func init() {
	storage.NewWebDAV = New
}

// Client implements the storage.Storage interface for WebDAV (Nextcloud, ownCloud, etc.)
type Client struct {
	baseURL    string
	pathPrefix string
	httpClient *http.Client
	username   string
	password   string
}

// New creates a new WebDAV storage client
func New(cfg *storage.StorageConfig) (storage.Storage, error) {
	baseURL := strings.TrimRight(cfg.WebDAVURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("WebDAV URL is required")
	}

	prefix := strings.Trim(cfg.PathPrefix, "/")

	return &Client{
		baseURL:    baseURL,
		pathPrefix: prefix,
		username:   cfg.WebDAVUsername,
		password:   cfg.WebDAVPassword,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *Client) fullURL(key string) string {
	if c.pathPrefix != "" {
		return c.baseURL + "/" + c.pathPrefix + "/" + key
	}
	return c.baseURL + "/" + key
}

func (c *Client) collectionURL() string {
	if c.pathPrefix != "" {
		return c.baseURL + "/" + c.pathPrefix + "/"
	}
	return c.baseURL + "/"
}

func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.httpClient.Do(req)
}

// Upload stores data with the given key, creating parent directories as needed.
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	if err := c.ensureParentDirs(ctx, key); err != nil {
		return fmt.Errorf("failed to create parent directories for %s: %w", key, err)
	}

	resp, err := c.doRequest(ctx, "PUT", c.fullURL(key), bytes.NewReader(data), map[string]string{
		"Content-Type": "application/octet-stream",
	})
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload %s: HTTP %d: %s", key, resp.StatusCode, string(body))
	}

	return nil
}

// Download retrieves data for the given key.
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.doRequest(ctx, "GET", c.fullURL(key), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download %s: HTTP %d", key, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", key, err)
	}

	return data, nil
}

// Delete removes the object with the given key.
func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.doRequest(ctx, "DELETE", c.fullURL(key), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete %s: HTTP %d", key, resp.StatusCode)
	}

	return nil
}

// DeleteBatch removes multiple objects sequentially.
func (c *Client) DeleteBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if err := c.Delete(ctx, key); err != nil {
			return err
		}
	}

	return nil
}

// List returns all objects under the given prefix using PROPFIND.
func (c *Client) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	url := c.collectionURL()
	if prefix != "" {
		url = c.collectionURL() + prefix
		if !strings.HasSuffix(url, "/") {
			url += "/"
		}
	}

	propfindBody := `<?xml version="1.0" encoding="UTF-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:getcontentlength/>
    <d:getlastmodified/>
    <d:getetag/>
    <d:resourcetype/>
  </d:prop>
</d:propfind>`

	resp, err := c.doRequest(ctx, "PROPFIND", url, strings.NewReader(propfindBody), map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "infinity",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("failed to list objects: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	responses, err := parsePropfindResponse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}

	collectionBase := c.collectionURL()
	var objects []storage.ObjectInfo
	for _, r := range responses {
		if r.IsCollection {
			continue
		}

		key := r.Href
		if idx := strings.Index(key, c.pathPrefix+"/"); idx >= 0 {
			key = key[idx+len(c.pathPrefix)+1:]
		} else {
			key = strings.TrimPrefix(key, collectionBase)
		}
		key = strings.TrimLeft(key, "/")

		if key == "" {
			continue
		}

		objects = append(objects, storage.ObjectInfo{
			Key:          key,
			Size:         r.ContentLength,
			LastModified: r.LastModified,
			ETag:         r.ETag,
		})
	}

	return objects, nil
}

// Head returns metadata for the given key without downloading content.
func (c *Client) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	propfindBody := `<?xml version="1.0" encoding="UTF-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:getcontentlength/>
    <d:getlastmodified/>
    <d:getetag/>
    <d:resourcetype/>
  </d:prop>
</d:propfind>`

	resp, err := c.doRequest(ctx, "PROPFIND", c.fullURL(key), strings.NewReader(propfindBody), map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "0",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to head %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("object not found: %s", key)
	}

	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("failed to head %s: HTTP %d", key, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	responses, err := parsePropfindResponse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}

	if len(responses) == 0 {
		return nil, fmt.Errorf("object not found: %s", key)
	}

	r := responses[0]
	return &storage.ObjectInfo{
		Key:          key,
		Size:         r.ContentLength,
		LastModified: r.LastModified,
		ETag:         r.ETag,
	}, nil
}

// BucketExists checks if the configured path prefix exists and is accessible.
// For WebDAV, the "bucket" concept maps to the path prefix directory.
// Unlike S3/R2/GCS where buckets must be pre-created, WebDAV directories
// can be created on the fly, so this auto-creates the path prefix if missing.
func (c *Client) BucketExists(ctx context.Context) (bool, error) {
	resp, err := c.doRequest(ctx, "PROPFIND", c.collectionURL(), strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:resourcetype/>
  </d:prop>
</d:propfind>`), map[string]string{
		"Content-Type": "application/xml",
		"Depth":        "0",
	})
	if err != nil {
		return false, fmt.Errorf("failed to check WebDAV path: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 207 || resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Errorf("authentication failed (HTTP %d) - check your username and app password", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusNotFound && c.pathPrefix != "" {
		// Auto-create the path prefix directory
		mkResp, mkErr := c.doRequest(ctx, "MKCOL", c.collectionURL(), nil, nil)
		if mkErr != nil {
			return false, fmt.Errorf("failed to create WebDAV directory '%s': %w", c.pathPrefix, mkErr)
		}
		mkResp.Body.Close()
		if mkResp.StatusCode == http.StatusCreated || mkResp.StatusCode == http.StatusMethodNotAllowed {
			return true, nil
		}
		return false, fmt.Errorf("failed to create WebDAV directory '%s': HTTP %d", c.pathPrefix, mkResp.StatusCode)
	}

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	return false, fmt.Errorf("unexpected HTTP %d checking WebDAV path", resp.StatusCode)
}

// ensureParentDirs creates all parent collections for the given key via MKCOL.
func (c *Client) ensureParentDirs(ctx context.Context, key string) error {
	dir := path.Dir(key)
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}

	parts := strings.Split(dir, "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = current + "/" + part
		}

		mkcolURL := c.collectionURL() + current + "/"
		resp, err := c.doRequest(ctx, "MKCOL", mkcolURL, nil, nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
		// 201 = created, 405 = already exists — both fine
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusConflict {
			if resp.StatusCode == http.StatusNotFound {
				continue
			}
		}
	}

	return nil
}
