package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"

	// Register storage adapters
	_ "github.com/tawanorg/claude-sync/internal/storage/gcs"
	_ "github.com/tawanorg/claude-sync/internal/storage/r2"
	_ "github.com/tawanorg/claude-sync/internal/storage/s3"
)

const defaultWorkers = 10

type Syncer struct {
	storage    storage.Storage
	encryptor  *crypto.Encryptor
	state      *SyncState
	claudeDir  string
	quiet      bool
	onProgress ProgressFunc
	cfg        *config.Config
}

type SyncResult struct {
	Uploaded   []string
	Downloaded []string
	Deleted    []string
	Conflicts  []string
	Errors     []error
}

type ProgressEvent struct {
	Action   string // "upload", "download", "delete", "encrypt", "decrypt", "scan"
	Path     string
	Size     int64
	Current  int
	Total    int
	Complete bool
	Error    error
}

type ProgressFunc func(event ProgressEvent)

func NewSyncer(cfg *config.Config, quiet bool) (*Syncer, error) {
	storageCfg := cfg.GetStorageConfig()
	store, err := storage.New(storageCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Use overridden state path if provided, otherwise use default
	var state *SyncState
	if cfg.StateDirOverride != "" {
		state, err = LoadStateFromDir(cfg.StateDirOverride)
	} else {
		state, err = LoadState()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	// Use overridden claude dir if provided, otherwise use default
	claudeDir := config.ClaudeDir()
	if cfg.ClaudeDirOverride != "" {
		claudeDir = cfg.ClaudeDirOverride
	}

	return &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     quiet,
		cfg:       cfg,
	}, nil
}

func (s *Syncer) SetProgressFunc(fn ProgressFunc) {
	s.onProgress = fn
}

func (s *Syncer) progress(event ProgressEvent) {
	if s.onProgress != nil {
		s.onProgress(event)
	}
}

func (s *Syncer) isExcluded(relPath string) bool {
	return s.cfg.IsExcluded(relPath)
}

func (s *Syncer) log(format string, args ...interface{}) {
	if !s.quiet {
		fmt.Printf(format+"\n", args...)
	}
}

func (s *Syncer) Push(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	s.progress(ProgressEvent{Action: "scan", Path: "Detecting changes..."})

	changes, err := s.state.DetectChanges(s.claudeDir, config.SyncPaths, s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to detect changes: %w", err)
	}

	if len(changes) == 0 {
		s.progress(ProgressEvent{Action: "scan", Complete: true})
		return result, nil
	}

	// Separate uploads from deletes
	var uploads, deletes []FileChange
	for _, change := range changes {
		switch change.Action {
		case "add", "modify":
			uploads = append(uploads, change)
		case "delete":
			deletes = append(deletes, change)
		}
	}

	total := len(changes)
	var mu sync.Mutex
	var completed atomic.Int32

	// Process uploads concurrently
	if len(uploads) > 0 {
		sem := make(chan struct{}, defaultWorkers)
		var wg sync.WaitGroup

		for _, change := range uploads {
			wg.Add(1)
			go func(change FileChange) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				n := int(completed.Add(1))
				s.progress(ProgressEvent{
					Action:  "upload",
					Path:    change.Path,
					Size:    change.LocalSize,
					Current: n,
					Total:   total,
				})

				if err := s.uploadFile(ctx, change.Path); err != nil {
					s.progress(ProgressEvent{
						Action: "upload",
						Path:   change.Path,
						Error:  err,
					})
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", change.Path, err))
					mu.Unlock()
					return
				}
				mu.Lock()
				result.Uploaded = append(result.Uploaded, change.Path)
				mu.Unlock()
			}(change)
		}
		wg.Wait()
	}

	// Process deletes (use batch delete if available, otherwise concurrent)
	if len(deletes) > 0 {
		deleteKeys := make([]string, len(deletes))
		for i, change := range deletes {
			deleteKeys[i] = s.remoteKey(change.Path)
		}
		if err := s.storage.DeleteBatch(ctx, deleteKeys); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("batch delete: %w", err))
		} else {
			for _, change := range deletes {
				s.state.RemoveFile(change.Path)
				result.Deleted = append(result.Deleted, change.Path)
			}
		}
	}

	s.progress(ProgressEvent{Action: "upload", Complete: true, Total: total})

	s.state.LastPush = time.Now()
	s.state.LastSync = time.Now()
	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}

	return result, nil
}

func (s *Syncer) Pull(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	s.progress(ProgressEvent{Action: "scan", Path: "Fetching remote file list..."})

	// List all remote objects
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	if len(remoteObjects) == 0 {
		s.progress(ProgressEvent{Action: "scan", Complete: true})
		return result, nil
	}

	// Build remote file map
	remoteFiles := make(map[string]storage.ObjectInfo)
	for _, obj := range remoteObjects {
		// Skip non-encrypted files
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		localPath := s.localPath(obj.Key)
		// Skip excluded paths
		if s.isExcluded(localPath) {
			continue
		}
		remoteFiles[localPath] = obj
	}

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, config.SyncPaths, s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Build list of files to download
	type downloadTask struct {
		localPath string
		remoteObj storage.ObjectInfo
	}
	var toDownload []downloadTask

	for localPath, remoteObj := range remoteFiles {
		localInfo, localExists := localFiles[localPath]
		stateFile := s.state.GetFile(localPath)

		shouldDownload := false

		if !localExists {
			shouldDownload = true
		} else if stateFile != nil {
			// Check if remote is newer than our last known state
			if remoteObj.LastModified.After(stateFile.Uploaded) {
				// Remote was updated after we last uploaded
				// Check if local was also modified
				localHash, _ := HashFile(filepath.Join(s.claudeDir, localPath))
				if localHash != stateFile.Hash {
					// Conflict: both changed
					result.Conflicts = append(result.Conflicts, localPath)
					s.progress(ProgressEvent{
						Action: "conflict",
						Path:   localPath,
					})
					if err := s.handleConflict(ctx, localPath, remoteObj); err != nil {
						result.Errors = append(result.Errors, err)
					}
					continue
				}
				shouldDownload = true
			}
		} else if localInfo.ModTime().Before(remoteObj.LastModified) {
			shouldDownload = true
		}

		if shouldDownload {
			toDownload = append(toDownload, downloadTask{localPath, remoteObj})
		}
	}

	// Download files concurrently
	total := len(toDownload)
	if total > 0 {
		sem := make(chan struct{}, defaultWorkers)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var completed atomic.Int32

		for _, task := range toDownload {
			wg.Add(1)
			go func(task downloadTask) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				n := int(completed.Add(1))
				s.progress(ProgressEvent{
					Action:  "download",
					Path:    task.localPath,
					Size:    task.remoteObj.Size,
					Current: n,
					Total:   total,
				})

				if err := s.downloadFile(ctx, task.localPath, task.remoteObj.Key); err != nil {
					s.progress(ProgressEvent{
						Action: "download",
						Path:   task.localPath,
						Error:  err,
					})
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.localPath, err))
					mu.Unlock()
					return
				}
				mu.Lock()
				result.Downloaded = append(result.Downloaded, task.localPath)
				mu.Unlock()
			}(task)
		}
		wg.Wait()
	}

	s.progress(ProgressEvent{Action: "download", Complete: true, Total: total})

	s.state.LastPull = time.Now()
	s.state.LastSync = time.Now()
	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}

	return result, nil
}

func (s *Syncer) Status(ctx context.Context) ([]FileChange, error) {
	return s.state.DetectChanges(s.claudeDir, config.SyncPaths, s.isExcluded)
}

func (s *Syncer) uploadFile(ctx context.Context, relativePath string) error {
	fullPath := filepath.Join(s.claudeDir, relativePath)

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Compress
	compressed, err := gzipCompress(data)
	if err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}

	// Encrypt
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return fmt.Errorf("failed to encrypt: %w", err)
	}

	// Upload
	remoteKey := s.remoteKey(relativePath)
	if err := s.storage.Upload(ctx, remoteKey, encrypted); err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	// Update state
	info, _ := os.Stat(fullPath)
	hash, _ := HashFile(fullPath)
	s.state.UpdateFile(relativePath, info, hash)
	s.state.MarkUploaded(relativePath)

	return nil
}

func (s *Syncer) downloadFile(ctx context.Context, relativePath, remoteKey string) error {
	// Download
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	// Decrypt
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("failed to decrypt: %w", err)
	}

	// Decompress if gzipped (backward-compatible with uncompressed data)
	if isGzipped(data) {
		data, err = gzipDecompress(data)
		if err != nil {
			return fmt.Errorf("failed to decompress: %w", err)
		}
	}

	// Ensure directory exists
	fullPath := filepath.Join(s.claudeDir, relativePath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Update state
	info, _ := os.Stat(fullPath)
	hash, _ := HashFile(fullPath)
	s.state.UpdateFile(relativePath, info, hash)
	s.state.MarkUploaded(relativePath)

	return nil
}

func (s *Syncer) handleConflict(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo) error {
	s.log("Conflict detected: %s (keeping local, saving remote as .conflict)", relativePath)

	// Download remote version with conflict suffix
	conflictPath := relativePath + ".conflict." + time.Now().Format("20060102-150405")
	if err := s.downloadFile(ctx, conflictPath, remoteObj.Key); err != nil {
		return fmt.Errorf("failed to save conflict file: %w", err)
	}

	return nil
}

func (s *Syncer) remoteKey(relativePath string) string {
	// Add .age extension for encrypted files
	return relativePath + ".age"
}

func (s *Syncer) localPath(remoteKey string) string {
	// Remove .age extension
	return strings.TrimSuffix(remoteKey, ".age")
}

func (s *Syncer) GetState() *SyncState {
	return s.state
}

// HasState returns true if the syncer has existing sync state (not first sync)
func (s *Syncer) HasState() bool {
	return !s.state.IsEmpty()
}

// FilePreview represents a file that would be affected by a pull operation
type FilePreview struct {
	Path       string
	LocalTime  time.Time
	RemoteTime time.Time
	LocalSize  int64
	RemoteSize int64
	LocalOnly  bool // File exists only locally
	RemoteOnly bool // File exists only remotely
}

// PullPreview represents what would happen during a pull operation
type PullPreview struct {
	WouldDownload  []FilePreview // New remote files that would be downloaded
	WouldOverwrite []FilePreview // Existing local files that would be replaced
	WouldKeep      []FilePreview // Local files that would be kept (local newer)
	WouldConflict  []FilePreview // Files that would create a conflict
	LocalOnlyFiles []FilePreview // Files that exist only locally
}

// PreviewPull returns a preview of what would happen during a pull operation
// without actually making any changes
func (s *Syncer) PreviewPull(ctx context.Context) (*PullPreview, error) {
	preview := &PullPreview{}

	// List all remote objects
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	// Build remote file map
	remoteFiles := make(map[string]storage.ObjectInfo)
	for _, obj := range remoteObjects {
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		localPath := s.localPath(obj.Key)
		if s.isExcluded(localPath) {
			continue
		}
		remoteFiles[localPath] = obj
	}

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, config.SyncPaths, s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Analyze each remote file
	for localPath, remoteObj := range remoteFiles {
		localInfo, localExists := localFiles[localPath]
		stateFile := s.state.GetFile(localPath)

		fp := FilePreview{
			Path:       localPath,
			RemoteTime: remoteObj.LastModified,
			RemoteSize: remoteObj.Size,
		}

		if localExists {
			fp.LocalTime = localInfo.ModTime()
			fp.LocalSize = localInfo.Size()
		}

		if !localExists {
			// New file from remote
			fp.RemoteOnly = true
			preview.WouldDownload = append(preview.WouldDownload, fp)
		} else if stateFile != nil {
			// Check if remote is newer than our last known state
			if remoteObj.LastModified.After(stateFile.Uploaded) {
				// Remote was updated after we last uploaded
				localHash, _ := HashFile(filepath.Join(s.claudeDir, localPath))
				if localHash != stateFile.Hash {
					// Conflict: both changed
					preview.WouldConflict = append(preview.WouldConflict, fp)
				} else {
					// Only remote changed
					preview.WouldOverwrite = append(preview.WouldOverwrite, fp)
				}
			} else {
				// Local is current
				preview.WouldKeep = append(preview.WouldKeep, fp)
			}
		} else {
			// No state - compare timestamps
			if localInfo.ModTime().Before(remoteObj.LastModified) {
				preview.WouldOverwrite = append(preview.WouldOverwrite, fp)
			} else {
				preview.WouldKeep = append(preview.WouldKeep, fp)
			}
		}
	}

	// Find local-only files
	for localPath, localInfo := range localFiles {
		if _, exists := remoteFiles[localPath]; !exists {
			preview.LocalOnlyFiles = append(preview.LocalOnlyFiles, FilePreview{
				Path:      localPath,
				LocalTime: localInfo.ModTime(),
				LocalSize: localInfo.Size(),
				LocalOnly: true,
			})
		}
	}

	return preview, nil
}

type DiffEntry struct {
	Path       string
	Status     string // "local_only", "remote_only", "modified", "synced"
	LocalSize  int64
	RemoteSize int64
	LocalTime  time.Time
	RemoteTime time.Time
}

func (s *Syncer) Diff(ctx context.Context) ([]DiffEntry, error) {
	var entries []DiffEntry

	// Get local files
	localFiles, err := GetLocalFiles(s.claudeDir, config.SyncPaths, s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Get remote files
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	remoteFiles := make(map[string]storage.ObjectInfo)
	for _, obj := range remoteObjects {
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		localPath := s.localPath(obj.Key)
		if s.isExcluded(localPath) {
			continue
		}
		remoteFiles[localPath] = obj
	}

	// Find local-only and modified files
	for relPath, info := range localFiles {
		remoteObj, exists := remoteFiles[relPath]
		if !exists {
			entries = append(entries, DiffEntry{
				Path:      relPath,
				Status:    "local_only",
				LocalSize: info.Size(),
				LocalTime: info.ModTime(),
			})
		} else {
			stateFile := s.state.GetFile(relPath)
			if stateFile != nil {
				localHash, _ := HashFile(filepath.Join(s.claudeDir, relPath))
				if localHash != stateFile.Hash || remoteObj.LastModified.After(stateFile.Uploaded) {
					entries = append(entries, DiffEntry{
						Path:       relPath,
						Status:     "modified",
						LocalSize:  info.Size(),
						RemoteSize: remoteObj.Size,
						LocalTime:  info.ModTime(),
						RemoteTime: remoteObj.LastModified,
					})
				} else {
					entries = append(entries, DiffEntry{
						Path:       relPath,
						Status:     "synced",
						LocalSize:  info.Size(),
						RemoteSize: remoteObj.Size,
						LocalTime:  info.ModTime(),
						RemoteTime: remoteObj.LastModified,
					})
				}
			} else {
				entries = append(entries, DiffEntry{
					Path:       relPath,
					Status:     "modified",
					LocalSize:  info.Size(),
					RemoteSize: remoteObj.Size,
					LocalTime:  info.ModTime(),
					RemoteTime: remoteObj.LastModified,
				})
			}
		}
	}

	// Find remote-only files
	for relPath, obj := range remoteFiles {
		if _, exists := localFiles[relPath]; !exists {
			entries = append(entries, DiffEntry{
				Path:       relPath,
				Status:     "remote_only",
				RemoteSize: obj.Size,
				RemoteTime: obj.LastModified,
			})
		}
	}

	return entries, nil
}

// isGzipped checks if data starts with the gzip magic number (0x1f 0x8b).
func isGzipped(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func gzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
