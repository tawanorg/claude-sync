package sync

import (
	"testing"
	"time"

	"github.com/tawanorg/claude-sync/internal/storage"
)

func TestRemoteChangedUsesETagNotClock(t *testing.T) {
	uploaded := time.Now()
	st := &FileState{Uploaded: uploaded, RemoteETag: "abc123"}

	// Same object, but the server clock is ahead of this device's: not a change.
	same := storage.ObjectInfo{ETag: `"ABC123"`, LastModified: uploaded.Add(5 * time.Second)}
	if remoteChanged(same, st) {
		t.Error("same ETag with a later server time was reported as a remote change")
	}

	// Another device uploaded different content: a change, whatever the times say.
	other := storage.ObjectInfo{ETag: `"def456"`, LastModified: uploaded.Add(-time.Second)}
	if !remoteChanged(other, st) {
		t.Error("different ETag was not reported as a remote change")
	}
}

func TestRemoteChangedFallsBackToTimeWithoutETag(t *testing.T) {
	uploaded := time.Now()
	st := &FileState{Uploaded: uploaded}
	if !remoteChanged(storage.ObjectInfo{LastModified: uploaded.Add(time.Second)}, st) {
		t.Error("later remote time without ETags was not reported as a change")
	}
	if remoteChanged(storage.ObjectInfo{LastModified: uploaded.Add(-time.Second)}, st) {
		t.Error("earlier remote time without ETags was reported as a change")
	}
}
