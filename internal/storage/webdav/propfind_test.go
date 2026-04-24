package webdav

import (
	"testing"
	"time"
)

func TestParsePropfindResponse(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">
  <d:response>
    <d:href>/remote.php/dav/files/user/claude-sync/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype><d:collection/></d:resourcetype>
        <d:getlastmodified>Mon, 14 Apr 2025 10:00:00 GMT</d:getlastmodified>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/remote.php/dav/files/user/claude-sync/sessions/abc123.enc</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
        <d:getcontentlength>4096</d:getcontentlength>
        <d:getlastmodified>Mon, 14 Apr 2025 12:30:00 GMT</d:getlastmodified>
        <d:getetag>"abc123def456"</d:getetag>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/remote.php/dav/files/user/claude-sync/settings.enc</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
        <d:getcontentlength>512</d:getcontentlength>
        <d:getlastmodified>Tue, 15 Apr 2025 08:00:00 GMT</d:getlastmodified>
        <d:getetag>"def789ghi012"</d:getetag>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`)

	results, err := parsePropfindResponse(xmlData)
	if err != nil {
		t.Fatalf("parsePropfindResponse failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(results))
	}

	if !results[0].IsCollection {
		t.Error("first response should be a collection")
	}

	if results[1].IsCollection {
		t.Error("second response should not be a collection")
	}
	if results[1].ContentLength != 4096 {
		t.Errorf("expected content length 4096, got %d", results[1].ContentLength)
	}
	if results[1].ETag != "abc123def456" {
		t.Errorf("expected etag abc123def456, got %q", results[1].ETag)
	}
	expectedTime := time.Date(2025, 4, 14, 12, 30, 0, 0, time.UTC)
	if !results[1].LastModified.Equal(expectedTime) {
		t.Errorf("expected last modified %v, got %v", expectedTime, results[1].LastModified)
	}

	if results[2].ContentLength != 512 {
		t.Errorf("expected content length 512, got %d", results[2].ContentLength)
	}
}

func TestParsePropfindResponseURLDecoding(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/remote.php/dav/files/user/claude-sync/path%20with%20spaces/file.enc</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
        <d:getcontentlength>100</d:getcontentlength>
        <d:getlastmodified>Mon, 14 Apr 2025 10:00:00 GMT</d:getlastmodified>
        <d:getetag>"aaa"</d:getetag>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`)

	results, err := parsePropfindResponse(xmlData)
	if err != nil {
		t.Fatalf("parsePropfindResponse failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 response, got %d", len(results))
	}

	expected := "/remote.php/dav/files/user/claude-sync/path with spaces/file.enc"
	if results[0].Href != expected {
		t.Errorf("expected decoded href %q, got %q", expected, results[0].Href)
	}
}

func TestParsePropfindResponseNon200Status(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/remote.php/dav/files/user/claude-sync/file.enc</d:href>
    <d:propstat>
      <d:prop>
        <d:getcontentlength>999</d:getcontentlength>
      </d:prop>
      <d:status>HTTP/1.1 404 Not Found</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`)

	results, err := parsePropfindResponse(xmlData)
	if err != nil {
		t.Fatalf("parsePropfindResponse failed: %v", err)
	}

	if results[0].ContentLength != 0 {
		t.Errorf("non-200 propstat should not populate content length, got %d", results[0].ContentLength)
	}
}
