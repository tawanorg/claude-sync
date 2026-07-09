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

	// Enforce HTTPS to protect Basic Auth credentials, except for localhost
	if !strings.HasPrefix(baseURL, "https://") {
		if !isLocalhost(baseURL) {
			return nil, fmt.Errorf("WebDAV URL must use HTTPS to protect credentials (use https:// instead of http://)")
		}
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

// isLocalhost checks if a URL points to localhost (safe for HTTP)
func isLocalhost(url string) bool {
	// Strip scheme
	host := strings.TrimPrefix(url, "http://")
	host = strings.TrimPrefix(host, "https://")
	// Get host part before any path
	if idx := strings.Index(host, "/"); idx > 0 {
		host = host[:idx]
	}
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
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

// propfindListBody is the PROPFIND request body used to enumerate objects.
const propfindListBody = `<?xml version="1.0" encoding="UTF-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:getcontentlength/>
    <d:getlastmodified/>
    <d:getetag/>
    <d:resourcetype/>
  </d:prop>
</d:propfind>`

// List returns all objects under the given prefix using PROPFIND.
//
// It first attempts a single Depth: infinity request, which Nextcloud and
// ownCloud support. Many other servers — notably Synology's WebDAV Server and
// Apache mod_dav with its default DavDepthInfinity off — reject infinite-depth
// PROPFIND with 403/405/501. In that case we fall back to walking the tree with
// Depth: 1 requests, which every WebDAV server supports.
func (c *Client) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	startURL := c.collectionURL()
	if prefix != "" {
		startURL = c.collectionURL() + prefix
		if !strings.HasSuffix(startURL, "/") {
			startURL += "/"
		}
	}

	responses, status, err := c.propfind(ctx, startURL, "infinity")
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	switch {
	case status == http.StatusNotFound:
		return nil, nil
	case status == 207:
		return c.collectObjects(responses), nil
	case infinityUnsupported(status):
		// Server refuses Depth: infinity — walk the tree one level at a time.
		return c.listRecursive(ctx, startURL)
	default:
		return nil, fmt.Errorf("failed to list objects: HTTP %d", status)
	}
}

// infinityUnsupported reports whether a status code indicates the server
// rejected a Depth: infinity PROPFIND (as opposed to a genuine error).
func infinityUnsupported(status int) bool {
	switch status {
	case http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusBadRequest:
		return true
	default:
		return false
	}
}

// listRecursive walks the collection tree using Depth: 1 PROPFIND requests,
// for servers that reject Depth: infinity. Directories are visited breadth-first.
func (c *Client) listRecursive(ctx context.Context, startURL string) ([]storage.ObjectInfo, error) {
	var objects []storage.ObjectInfo
	queue := []string{startURL}
	visited := map[string]bool{}

	for len(queue) > 0 {
		dirURL := queue[0]
		queue = queue[1:]
		if visited[dirURL] {
			continue
		}
		visited[dirURL] = true

		responses, status, err := c.propfind(ctx, dirURL, "1")
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}
		if status == http.StatusNotFound {
			continue
		}
		if status != 207 {
			return nil, fmt.Errorf("failed to list objects: HTTP %d", status)
		}

		for _, r := range responses {
			key := c.hrefToKey(r.Href)
			if key == "" {
				continue // the collection referencing itself
			}
			if r.IsCollection {
				childURL := c.collectionURL() + key
				if !strings.HasSuffix(childURL, "/") {
					childURL += "/"
				}
				queue = append(queue, childURL)
				continue
			}
			objects = append(objects, storage.ObjectInfo{
				Key:          key,
				Size:         r.ContentLength,
				LastModified: r.LastModified,
				ETag:         r.ETag,
			})
		}
	}

	return objects, nil
}

// propfind issues a PROPFIND at the given depth and returns the parsed entries
// together with the HTTP status code. A non-207 status yields (nil, status, nil)
// so callers can decide how to react without treating it as a transport error.
func (c *Client) propfind(ctx context.Context, url, depth string) ([]parsedResponse, int, error) {
	resp, err := c.doRequest(ctx, "PROPFIND", url, strings.NewReader(propfindListBody), map[string]string{
		"Content-Type": "application/xml",
		"Depth":        depth,
	})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 207 {
		return nil, resp.StatusCode, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	responses, err := parsePropfindResponse(body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}

	return responses, resp.StatusCode, nil
}

// collectObjects turns PROPFIND entries into ObjectInfos, dropping collections
// and the self-reference of the listed directory.
func (c *Client) collectObjects(responses []parsedResponse) []storage.ObjectInfo {
	var objects []storage.ObjectInfo
	for _, r := range responses {
		if r.IsCollection {
			continue
		}
		key := c.hrefToKey(r.Href)
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
	return objects
}

// hrefToKey converts a PROPFIND href into a storage key relative to the
// configured path prefix.
func (c *Client) hrefToKey(href string) string {
	key := href
	if idx := strings.Index(key, c.pathPrefix+"/"); c.pathPrefix != "" && idx >= 0 {
		key = key[idx+len(c.pathPrefix)+1:]
	} else {
		key = strings.TrimPrefix(key, c.collectionURL())
	}
	return strings.TrimLeft(key, "/")
}

// Head returns metadata for the given key without downloading content.
func (c *Client) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	resp, err := c.doRequest(ctx, "PROPFIND", c.fullURL(key), strings.NewReader(propfindListBody), map[string]string{
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
