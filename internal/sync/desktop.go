package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/desktop"
)

// desktopPayloadVersion versions the wire format so an older client can
// recognise a payload it does not understand instead of misreading it.
const desktopPayloadVersion = 1

// desktopEntry carries one index record plus when the pushing machine last
// observed it. The observation time travels beside the record rather than
// inside it: the record schema belongs to the desktop app and must not gain
// fields invented here.
type desktopEntry struct {
	ObservedAt time.Time       `json:"observed_at"`
	Record     *desktop.Record `json:"record"`
}

type desktopPayload struct {
	Version int            `json:"version"`
	Entries []desktopEntry `json:"entries"`
}

// DesktopPushResult summarises a desktop index push.
type DesktopPushResult struct {
	RecordsPushed int
	Unchanged     bool
	NoIndex       bool
}

// DesktopPullResult summarises a desktop index pull.
type DesktopPullResult struct {
	Written int
	Updated int
	Skipped int

	// MissingTranscript counts sessions skipped because this machine has no
	// transcript for them. Writing those records would produce sidebar rows
	// that open onto "Session not found on disk".
	MissingTranscript int

	NoIndex  bool
	NoRemote bool
}

// desktopIndexDir resolves the index directory, preferring an explicit override.
func (s *Syncer) desktopIndexDir() (string, error) {
	if s.desktopDir != "" {
		return s.desktopDir, nil
	}
	root, err := desktop.SessionsRoot()
	if err != nil {
		return "", err
	}
	return desktop.IndexDir(root)
}

// PushDesktop uploads the desktop sidebar index.
//
// The sidebar renders from this index, not from ~/.claude/projects, so syncing
// conversations without it leaves them resumable from the CLI but invisible in
// the app. A machine with no desktop app contributes nothing and is not an
// error.
func (s *Syncer) PushDesktop(ctx context.Context) (*DesktopPushResult, error) {
	result := &DesktopPushResult{}

	dir, err := s.desktopIndexDir()
	if err != nil {
		if errors.Is(err, desktop.ErrNoIndex) {
			result.NoIndex = true
			return result, nil
		}
		return nil, err
	}

	snapshots, err := desktop.Snapshots(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read desktop index: %w", err)
	}
	if len(snapshots) == 0 {
		result.Unchanged = true
		return result, nil
	}

	payload := desktopPayload{Version: desktopPayloadVersion}
	for _, snap := range snapshots {
		payload.Entries = append(payload.Entries, desktopEntry{
			ObservedAt: snap.ObservedAt,
			Record:     snap.Record,
		})
	}
	// Stable ordering keeps the hash meaningful, so an unchanged index does not
	// re-upload merely because the filesystem returned a different order.
	sort.Slice(payload.Entries, func(i, j int) bool {
		return payload.Entries[i].Record.CLISessionID() < payload.Entries[j].Record.CLISessionID()
	})

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize desktop index: %w", err)
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if existing := s.state.GetFile(config.DesktopRemoteKey); existing != nil && existing.Hash == hash {
		result.Unchanged = true
		return result, nil
	}

	compressed, err := gzipCompress(data)
	if err != nil {
		return nil, fmt.Errorf("failed to compress desktop index: %w", err)
	}
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt desktop index: %w", err)
	}
	if err := s.storage.Upload(ctx, config.DesktopRemoteKey+".age", encrypted); err != nil {
		return nil, fmt.Errorf("failed to upload desktop index: %w", err)
	}

	s.state.mu.Lock()
	s.state.Files[config.DesktopRemoteKey] = &FileState{
		Path:     config.DesktopRemoteKey,
		Hash:     hash,
		Size:     int64(len(data)),
		ModTime:  time.Now(),
		Uploaded: time.Now(),
	}
	s.state.mu.Unlock()

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	result.RecordsPushed = len(payload.Entries)
	return result, nil
}

// PullDesktop applies the remote desktop index to this machine's sidebar.
//
// Records for sessions the sidebar has never seen are created; sessions it
// already knows about keep their local record and only have their archive state
// reconciled, last-writer-wins.
func (s *Syncer) PullDesktop(ctx context.Context) (*DesktopPullResult, error) {
	result := &DesktopPullResult{}

	dir, err := s.desktopIndexDir()
	if err != nil {
		if errors.Is(err, desktop.ErrNoIndex) {
			result.NoIndex = true
			return result, nil
		}
		return nil, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		// No desktop app on this machine: nothing to hydrate.
		result.NoIndex = true
		return result, nil
	}

	encrypted, err := s.storage.Download(ctx, config.DesktopRemoteKey+".age")
	if err != nil {
		// Nothing has been pushed yet. That is an empty result, not a failure.
		result.NoRemote = true
		return result, nil
	}

	compressed, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt desktop index: %w", err)
	}
	data, err := gzipDecompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress desktop index: %w", err)
	}

	var payload desktopPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse desktop index: %w", err)
	}
	if payload.Version > desktopPayloadVersion {
		return nil, fmt.Errorf("desktop index was written by a newer claude-sync (format v%d); upgrade to pull it", payload.Version)
	}

	// A sidebar record points at a transcript by id. If this machine does not
	// have that transcript, the row it creates opens onto "Session not found on
	// disk" — the index is intact but the conversation behind it is elsewhere.
	// Skip those and report them, so the remedy (pull the conversations first)
	// is visible instead of appearing as a broken sidebar.
	local := localTranscriptIDs(s.claudeDir)

	incoming := make([]desktop.Incoming, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		if entry.Record == nil {
			continue
		}
		if id := entry.Record.CLISessionID(); id != "" && !local[id] {
			result.MissingTranscript++
			continue
		}
		incoming = append(incoming, desktop.Incoming{
			Record:     entry.Record,
			ObservedAt: entry.ObservedAt,
		})
	}

	applied, err := desktop.Apply(dir, incoming)
	if err != nil {
		return nil, fmt.Errorf("failed to apply desktop index: %w", err)
	}
	result.Written = applied.Written
	result.Updated = applied.Updated
	result.Skipped = applied.Skipped
	return result, nil
}

// SetSkipArchived controls whether push omits archived sessions' transcript
// bodies. The archive labels themselves are always pushed, so the other machine
// still learns that a session was archived; only the bulk transcript is held
// back. Omitting the label too would let an archive decision made after the
// first sync never reach the other device.
func (s *Syncer) SetSkipArchived(skip bool) {
	s.skipArchived = skip
}

// sessionIDFromPath returns the session a transcript path belongs to.
//
// Transcripts live at projects/<encoded-project>/<session-id>.jsonl exactly one
// level below a project directory. Anything else — history.jsonl, task files,
// nested artefacts — belongs to no single session and is never filtered.
func sessionIDFromPath(relPath string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) != 3 || parts[0] != "projects" {
		return "", false
	}
	name := parts[2]
	if !strings.HasSuffix(name, ".jsonl") {
		return "", false
	}
	return strings.TrimSuffix(name, ".jsonl"), true
}

// archivedSessionIDs reports which sessions the local desktop index marks
// archived. A machine without a desktop index knows of none.
func (s *Syncer) archivedSessionIDs() map[string]bool {
	dir, err := s.desktopIndexDir()
	if err != nil {
		return nil
	}
	labels, err := desktop.Scan(dir)
	if err != nil {
		return nil
	}
	archived := make(map[string]bool)
	for id, label := range labels {
		if label.Status == desktop.ArchiveArchived {
			archived[id] = true
		}
	}
	return archived
}

// pushExcluder composes the configured excludes with --skip-archived. It is
// used only by push: pull and status keep the plain exclude set so the flag
// cannot silently change what a download or a status report covers.
func (s *Syncer) pushExcluder() func(string) bool {
	if !s.skipArchived {
		return s.isExcluded
	}
	archived := s.archivedSessionIDs()
	if len(archived) == 0 {
		return s.isExcluded
	}
	return func(relPath string) bool {
		if s.isExcluded(relPath) {
			return true
		}
		id, ok := sessionIDFromPath(relPath)
		return ok && archived[id]
	}
}

// localTranscriptIDs returns the session ids this machine holds transcripts for.
// Transcripts live at projects/<encoded-project>/<session-id>.jsonl.
func localTranscriptIDs(claudeDir string) map[string]bool {
	ids := make(map[string]bool)
	matches, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return ids
	}
	for _, path := range matches {
		ids[strings.TrimSuffix(filepath.Base(path), ".jsonl")] = true
	}
	return ids
}
