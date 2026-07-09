package webdav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tawanorg/claude-sync/internal/storage"
)

// mockWebDAVServer simulates a WebDAV server for integration testing.
// It stores files in memory and supports all required WebDAV methods.
type mockWebDAVServer struct {
	mu    sync.Mutex
	files map[string]mockFile
}

type mockFile struct {
	data    []byte
	modTime time.Time
	isDir   bool
}

func newMockWebDAVServer() *mockWebDAVServer {
	return &mockWebDAVServer{
		files: make(map[string]mockFile),
	}
}

func (s *mockWebDAVServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/")

	switch r.Method {
	case "PUT":
		s.handlePut(w, r, path)
	case "GET":
		s.handleGet(w, r, path)
	case "DELETE":
		s.handleDelete(w, r, path)
	case "PROPFIND":
		s.handlePropfind(w, r, path)
	case "MKCOL":
		s.handleMkcol(w, r, path)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *mockWebDAVServer) handlePut(w http.ResponseWriter, r *http.Request, path string) {
	data := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(data)
	s.files[path] = mockFile{data: data, modTime: time.Now()}
	w.WriteHeader(http.StatusCreated)
}

func (s *mockWebDAVServer) handleGet(w http.ResponseWriter, r *http.Request, path string) {
	f, ok := s.files[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.data)
}

func (s *mockWebDAVServer) handleDelete(w http.ResponseWriter, r *http.Request, path string) {
	delete(s.files, path)
	w.WriteHeader(http.StatusNoContent)
}

func (s *mockWebDAVServer) handleMkcol(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimSuffix(path, "/")
	if _, exists := s.files[path]; exists {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.files[path] = mockFile{isDir: true, modTime: time.Now()}
	w.WriteHeader(http.StatusCreated)
}

func (s *mockWebDAVServer) handlePropfind(w http.ResponseWriter, r *http.Request, path string) {
	depth := r.Header.Get("Depth")
	path = strings.TrimSuffix(path, "/")

	// Build response
	var responses []string

	// Check if path itself exists or is root
	if path == "" || path == "backup" {
		responses = append(responses, s.buildPropfindEntry("/"+path+"/", 0, true, time.Now()))
	}

	switch depth {
	case "infinity", "1":
		prefix := path
		if prefix != "" {
			prefix += "/"
		}
		for key, f := range s.files {
			if strings.HasPrefix(key, prefix) && !f.isDir {
				responses = append(responses, s.buildPropfindEntry("/"+key, int64(len(f.data)), false, f.modTime))
			}
		}
	case "0":
		f, ok := s.files[path]
		if ok {
			responses = append(responses, s.buildPropfindEntry("/"+path, int64(len(f.data)), f.isDir, f.modTime))
		}
	}

	if len(responses) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(207)
	resp := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`
	for _, r := range responses {
		resp += r
	}
	resp += `</d:multistatus>`
	_, _ = w.Write([]byte(resp))
}

func (s *mockWebDAVServer) buildPropfindEntry(href string, size int64, isDir bool, modTime time.Time) string {
	resourceType := "<d:resourcetype/>"
	if isDir {
		resourceType = "<d:resourcetype><d:collection/></d:resourcetype>"
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` +
		resourceType +
		`<d:getcontentlength>` + fmt.Sprintf("%d", size) + `</d:getcontentlength>` +
		`<d:getlastmodified>` + modTime.UTC().Format(time.RFC1123) + `</d:getlastmodified>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

// TestWebDAVFullCycle tests a complete upload -> list -> download -> delete cycle.
// This verifies the WebDAV provider correctly implements the Storage interface.
func TestWebDAVFullCycle(t *testing.T) {
	mockServer := newMockWebDAVServer()
	server := httptest.NewServer(mockServer)
	defer server.Close()

	cfg := &storage.StorageConfig{
		Provider:       storage.ProviderWebDAV,
		WebDAVURL:      server.URL,
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
		PathPrefix:     "backup",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx := context.Background()

	// Test Upload
	testData := []byte("encrypted session data")
	testKey := "sessions/abc123.age"

	if err := client.Upload(ctx, testKey, testData); err != nil {
		t.Fatalf("Upload() failed: %v", err)
	}

	// Test Download
	downloaded, err := client.Download(ctx, testKey)
	if err != nil {
		t.Fatalf("Download() failed: %v", err)
	}
	if string(downloaded) != string(testData) {
		t.Errorf("Download() returned %q, want %q", string(downloaded), string(testData))
	}

	// Test Head
	info, err := client.Head(ctx, testKey)
	if err != nil {
		t.Fatalf("Head() failed: %v", err)
	}
	if info.Key != testKey {
		t.Errorf("Head() key = %q, want %q", info.Key, testKey)
	}

	// Test List
	objects, err := client.List(ctx, "sessions/")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	found := false
	for _, obj := range objects {
		if strings.Contains(obj.Key, "abc123.age") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List() did not return uploaded file, got: %v", objects)
	}

	// Test Delete
	if err := client.Delete(ctx, testKey); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	// Verify deleted
	_, err = client.Download(ctx, testKey)
	if err == nil {
		t.Error("Download() after Delete() should fail")
	}

	// Test BucketExists
	exists, err := client.BucketExists(ctx)
	if err != nil {
		t.Fatalf("BucketExists() failed: %v", err)
	}
	if !exists {
		t.Error("BucketExists() = false, want true")
	}
}

// TestWebDAVDeleteBatch tests batch deletion.
func TestWebDAVDeleteBatchIntegration(t *testing.T) {
	mockServer := newMockWebDAVServer()
	server := httptest.NewServer(mockServer)
	defer server.Close()

	cfg := &storage.StorageConfig{
		Provider:       storage.ProviderWebDAV,
		WebDAVURL:      server.URL,
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
		PathPrefix:     "backup",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx := context.Background()

	// Upload multiple files
	keys := []string{"file1.age", "file2.age", "file3.age"}
	for _, key := range keys {
		if err := client.Upload(ctx, key, []byte("data")); err != nil {
			t.Fatalf("Upload(%s) failed: %v", key, err)
		}
	}

	// Delete batch
	if err := client.DeleteBatch(ctx, keys); err != nil {
		t.Fatalf("DeleteBatch() failed: %v", err)
	}

	// Verify all deleted
	for _, key := range keys {
		_, err := client.Download(ctx, key)
		if err == nil {
			t.Errorf("Download(%s) after DeleteBatch() should fail", key)
		}
	}
}
