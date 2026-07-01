package webdav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/tawanorg/claude-sync/internal/storage"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *storage.StorageConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &storage.StorageConfig{
				WebDAVURL:      "https://cloud.example.com/remote.php/dav/files/user/",
				WebDAVUsername: "user",
				WebDAVPassword: "pass",
				PathPrefix:     "claude-sync",
			},
			wantErr: false,
		},
		{
			name: "missing URL",
			cfg: &storage.StorageConfig{
				WebDAVUsername: "user",
				WebDAVPassword: "pass",
			},
			wantErr: true,
		},
		{
			name: "empty URL",
			cfg: &storage.StorageConfig{
				WebDAVURL:      "",
				WebDAVUsername: "user",
				WebDAVPassword: "pass",
			},
			wantErr: true,
		},
		{
			name: "URL with trailing slash stripped",
			cfg: &storage.StorageConfig{
				WebDAVURL:      "https://cloud.example.com/dav/",
				WebDAVUsername: "user",
				WebDAVPassword: "pass",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("New() returned nil client without error")
			}
		})
	}
}

func TestClientURLBuilding(t *testing.T) {
	tests := []struct {
		name           string
		baseURL        string
		pathPrefix     string
		key            string
		wantFullURL    string
		wantCollection string
	}{
		{
			name:           "with path prefix",
			baseURL:        "https://cloud.example.com/dav",
			pathPrefix:     "claude-sync",
			key:            "sessions/abc.age",
			wantFullURL:    "https://cloud.example.com/dav/claude-sync/sessions/abc.age",
			wantCollection: "https://cloud.example.com/dav/claude-sync/",
		},
		{
			name:           "without path prefix",
			baseURL:        "https://cloud.example.com/dav",
			pathPrefix:     "",
			key:            "settings.age",
			wantFullURL:    "https://cloud.example.com/dav/settings.age",
			wantCollection: "https://cloud.example.com/dav/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				baseURL:    tt.baseURL,
				pathPrefix: tt.pathPrefix,
			}

			if got := c.fullURL(tt.key); got != tt.wantFullURL {
				t.Errorf("fullURL() = %q, want %q", got, tt.wantFullURL)
			}

			if got := c.collectionURL(); got != tt.wantCollection {
				t.Errorf("collectionURL() = %q, want %q", got, tt.wantCollection)
			}
		})
	}
}

func TestUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check authentication
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testuser" || pass != "testpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case "MKCOL":
			// Parent directory creation
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "backup",
		username:   "testuser",
		password:   "testpass",
		httpClient: server.Client(),
	}

	err := client.Upload(context.Background(), "sessions/test.age", []byte("encrypted data"))
	if err != nil {
		t.Errorf("Upload() error = %v", err)
	}
}

func TestUploadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	err := client.Upload(context.Background(), "test.age", []byte("data"))
	if err == nil {
		t.Error("Upload() expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("Upload() error should contain HTTP status, got: %v", err)
	}
}

func TestDownload(t *testing.T) {
	expectedData := []byte("decrypted content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedData)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	data, err := client.Download(context.Background(), "test.age")
	if err != nil {
		t.Errorf("Download() error = %v", err)
	}
	if string(data) != string(expectedData) {
		t.Errorf("Download() = %q, want %q", string(data), string(expectedData))
	}
}

func TestDownloadNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	_, err := client.Download(context.Background(), "nonexistent.age")
	if err == nil {
		t.Error("Download() expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Download() error should indicate not found, got: %v", err)
	}
}

func TestDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	err := client.Delete(context.Background(), "test.age")
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}
}

func TestDeleteBatch(t *testing.T) {
	deleteCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleteCount++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	keys := []string{"file1.age", "file2.age", "file3.age"}
	err := client.DeleteBatch(context.Background(), keys)
	if err != nil {
		t.Errorf("DeleteBatch() error = %v", err)
	}
	if deleteCount != 3 {
		t.Errorf("DeleteBatch() made %d DELETE requests, want 3", deleteCount)
	}
}

func TestDeleteBatchEmpty(t *testing.T) {
	client := &Client{
		baseURL:    "https://example.com",
		pathPrefix: "",
		username:   "user",
		password:   "pass",
	}

	err := client.DeleteBatch(context.Background(), []string{})
	if err != nil {
		t.Errorf("DeleteBatch() with empty keys should not error, got: %v", err)
	}
}

func TestList(t *testing.T) {
	propfindResponse := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/backup/</d:href>
    <d:propstat>
      <d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/backup/sessions/abc.age</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
        <d:getcontentlength>1024</d:getcontentlength>
        <d:getlastmodified>Mon, 01 Jan 2024 12:00:00 GMT</d:getlastmodified>
        <d:getetag>"etag123"</d:getetag>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Depth") != "infinity" {
			t.Errorf("expected Depth: infinity, got: %s", r.Header.Get("Depth"))
		}
		w.WriteHeader(207)
		_, _ = w.Write([]byte(propfindResponse))
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "backup",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	objects, err := client.List(context.Background(), "")
	if err != nil {
		t.Errorf("List() error = %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("List() returned %d objects, want 1 (collections filtered)", len(objects))
	}
	if len(objects) > 0 && objects[0].Size != 1024 {
		t.Errorf("List() object size = %d, want 1024", objects[0].Size)
	}
}

func TestListNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	objects, err := client.List(context.Background(), "nonexistent/")
	if err != nil {
		t.Errorf("List() should not error on 404, got: %v", err)
	}
	if objects != nil {
		t.Errorf("List() should return nil on 404, got: %v", objects)
	}
}

func TestHead(t *testing.T) {
	propfindResponse := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/backup/test.age</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
        <d:getcontentlength>2048</d:getcontentlength>
        <d:getlastmodified>Tue, 02 Jan 2024 14:00:00 GMT</d:getlastmodified>
        <d:getetag>"etag456"</d:getetag>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Depth") != "0" {
			t.Errorf("Head should use Depth: 0, got: %s", r.Header.Get("Depth"))
		}
		w.WriteHeader(207)
		_, _ = w.Write([]byte(propfindResponse))
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "backup",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	info, err := client.Head(context.Background(), "test.age")
	if err != nil {
		t.Errorf("Head() error = %v", err)
	}
	if info.Size != 2048 {
		t.Errorf("Head() size = %d, want 2048", info.Size)
	}
	if info.ETag != "etag456" {
		t.Errorf("Head() ETag = %q, want \"etag456\"", info.ETag)
	}
}

func TestHeadNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	_, err := client.Head(context.Background(), "nonexistent.age")
	if err == nil {
		t.Error("Head() expected error for 404 response")
	}
}

func TestBucketExists(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantExists bool
		wantErr    bool
	}{
		{"exists (207)", 207, true, false},
		{"exists (200)", http.StatusOK, true, false},
		{"unauthorized", http.StatusUnauthorized, false, true},
		{"forbidden", http.StatusForbidden, false, true},
		{"not found without prefix", http.StatusNotFound, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := &Client{
				baseURL:    server.URL,
				pathPrefix: "", // no prefix so 404 returns false, not auto-create
				username:   "user",
				password:   "pass",
				httpClient: server.Client(),
			}

			exists, err := client.BucketExists(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("BucketExists() error = %v, wantErr %v", err, tt.wantErr)
			}
			if exists != tt.wantExists {
				t.Errorf("BucketExists() = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestBucketExistsAutoCreate(t *testing.T) {
	propfindCalled := false
	mkcolCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PROPFIND":
			propfindCalled = true
			w.WriteHeader(http.StatusNotFound)
		case "MKCOL":
			mkcolCalled = true
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "claude-sync", // with prefix, 404 triggers auto-create
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	exists, err := client.BucketExists(context.Background())
	if err != nil {
		t.Errorf("BucketExists() error = %v", err)
	}
	if !exists {
		t.Error("BucketExists() should return true after auto-create")
	}
	if !propfindCalled {
		t.Error("BucketExists() should call PROPFIND first")
	}
	if !mkcolCalled {
		t.Error("BucketExists() should call MKCOL to auto-create directory")
	}
}

func TestEnsureParentDirs(t *testing.T) {
	mkcolPaths := []string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			mkcolPaths = append(mkcolPaths, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "backup",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	err := client.ensureParentDirs(context.Background(), "a/b/c/file.age")
	if err != nil {
		t.Errorf("ensureParentDirs() error = %v", err)
	}

	// Should create a/, a/b/, a/b/c/ under the pathPrefix
	if len(mkcolPaths) != 3 {
		t.Errorf("ensureParentDirs() made %d MKCOL calls, want 3", len(mkcolPaths))
	}
}

func TestEnsureParentDirsNoParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("ensureParentDirs() should not make requests for root-level files")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "",
		username:   "user",
		password:   "pass",
		httpClient: server.Client(),
	}

	// File at root level should not trigger any MKCOL
	err := client.ensureParentDirs(context.Background(), "file.age")
	if err != nil {
		t.Errorf("ensureParentDirs() error = %v", err)
	}
}

// multistatusFor builds a 207 PROPFIND body for a Depth: 1 listing of dir:
// the self-reference plus the given child files and child collections.
func multistatusFor(dir string, files, dirs []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
	// self
	b.WriteString(`<d:response><d:href>` + dir + `</d:href><d:propstat><d:prop>` +
		`<d:resourcetype><d:collection/></d:resourcetype></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
	for _, f := range files {
		b.WriteString(`<d:response><d:href>` + dir + f + `</d:href><d:propstat><d:prop>` +
			`<d:resourcetype/><d:getcontentlength>10</d:getcontentlength>` +
			`<d:getlastmodified>Mon, 14 Apr 2025 10:00:00 GMT</d:getlastmodified>` +
			`<d:getetag>"e-` + f + `"</d:getetag></d:prop>` +
			`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
	}
	for _, d := range dirs {
		b.WriteString(`<d:response><d:href>` + dir + d + `/</d:href><d:propstat><d:prop>` +
			`<d:resourcetype><d:collection/></d:resourcetype></d:prop>` +
			`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
	}
	b.WriteString(`</d:multistatus>`)
	return b.String()
}

// TestListDepthInfinityFallback verifies that when a server rejects a
// Depth: infinity PROPFIND (as Synology WebDAV Server and Apache mod_dav do),
// List falls back to a recursive Depth: 1 walk and still enumerates every file.
func TestListDepthInfinityFallback(t *testing.T) {
	const prefix = "claude-sync"

	// Tree under /claude-sync/:
	//   settings.enc
	//   sessions/a.enc
	//   sessions/nested/b.enc
	tree := map[string]string{
		"/claude-sync/":                 multistatusFor("/claude-sync/", []string{"settings.enc"}, []string{"sessions"}),
		"/claude-sync/sessions/":        multistatusFor("/claude-sync/sessions/", []string{"a.enc"}, []string{"nested"}),
		"/claude-sync/sessions/nested/": multistatusFor("/claude-sync/sessions/nested/", []string{"b.enc"}, nil),
	}

	var infinityAttempts, depth1Requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Depth") == "infinity" {
			infinityAttempts++
			// Mimic Synology / Apache DavDepthInfinity off.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		depth1Requests++
		body, ok := tree[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(207)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c, err := New(&storage.StorageConfig{
		WebDAVURL:      server.URL,
		PathPrefix:     prefix,
		WebDAVUsername: "u",
		WebDAVPassword: "p",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	objs, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if infinityAttempts != 1 {
		t.Errorf("expected exactly 1 Depth: infinity attempt, got %d", infinityAttempts)
	}
	if depth1Requests != 3 {
		t.Errorf("expected 3 Depth: 1 requests (one per collection), got %d", depth1Requests)
	}

	got := make([]string, 0, len(objs))
	for _, o := range objs {
		got = append(got, o.Key)
	}
	sort.Strings(got)

	want := []string{"sessions/a.enc", "sessions/nested/b.enc", "settings.enc"}
	if len(got) != len(want) {
		t.Fatalf("expected keys %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected keys %v, got %v", want, got)
		}
	}
}

// TestListDepthInfinitySuccess verifies the fast path: a server that honors
// Depth: infinity is listed in a single request with no fallback walk.
func TestListDepthInfinitySuccess(t *testing.T) {
	const prefix = "claude-sync"

	full := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">` +
		`<d:response><d:href>/claude-sync/</d:href><d:propstat><d:prop>` +
		`<d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>/claude-sync/settings.enc</d:href><d:propstat><d:prop>` +
		`<d:resourcetype/><d:getcontentlength>10</d:getcontentlength>` +
		`<d:getlastmodified>Mon, 14 Apr 2025 10:00:00 GMT</d:getlastmodified><d:getetag>"x"</d:getetag></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`<d:response><d:href>/claude-sync/sessions/a.enc</d:href><d:propstat><d:prop>` +
		`<d:resourcetype/><d:getcontentlength>10</d:getcontentlength>` +
		`<d:getlastmodified>Mon, 14 Apr 2025 10:00:00 GMT</d:getlastmodified><d:getetag>"y"</d:getetag></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
		`</d:multistatus>`

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Depth") != "infinity" {
			t.Errorf("expected Depth: infinity, got %q", r.Header.Get("Depth"))
		}
		w.WriteHeader(207)
		_, _ = w.Write([]byte(full))
	}))
	defer server.Close()

	c, err := New(&storage.StorageConfig{
		WebDAVURL:      server.URL,
		PathPrefix:     prefix,
		WebDAVUsername: "u",
		WebDAVPassword: "p",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	objs, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if requests != 1 {
		t.Errorf("expected a single PROPFIND, got %d", requests)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d: %+v", len(objs), objs)
	}
}
