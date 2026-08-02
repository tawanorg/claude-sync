package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// conflictArtifactRe matches only this tool's own conflict artifacts
// (<name>.conflict.<yyyymmdd>-<hhmmss>, the format handleConflict writes),
// never user files that merely contain ".conflict." in their names. The
// anchoring matters: excluding a path that is already tracked in state makes
// the next push prune it from the bucket, so a loose pattern would turn into
// remote deletion of real data.
var conflictArtifactRe = regexp.MustCompile(`\.conflict\.\d{8}-\d{6}$`)

// maxDecompressedSize is the maximum allowed size for decompressed data (500MB).
// This prevents decompression bomb attacks from consuming excessive memory.
const maxDecompressedSize = 500 * 1024 * 1024

// ManifestKey is the remote storage key for file metadata (mtimes).
const ManifestKey = "_metadata/manifest.json"

// errSkipUpload is returned by uploadFile when the shrink guard declines to
// upload a truncated session transcript. Push treats it as neither an upload
// nor an error: it is deliberate inaction, and reporting it as a failure would
// make every push look broken while a machine waits for its next pull.
var errSkipUpload = errors.New("upload skipped: local copy is behind the bucket copy")

// FileManifest stores metadata about synced files, primarily mtimes.
type FileManifest struct {
	Files map[string]FileMetadata `json:"files"`
}

// FileMetadata stores metadata for a single file.
type FileMetadata struct {
	ModTime time.Time `json:"mod_time"`
}

type Syncer struct {
	storage    storage.Storage
	encryptor  *crypto.Encryptor
	state      *SyncState
	claudeDir  string
	quiet      bool
	onProgress ProgressFunc
	cfg        *config.Config
	paths      *PathMapper
	ccdDir     string // desktop-app session store override (tests inject a temp dir)
}

// uploadEncoded compresses, encrypts, and uploads raw bytes to a remote key.
func (s *Syncer) uploadEncoded(ctx context.Context, key string, data []byte) error {
	compressed, err := gzipCompress(data)
	if err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return fmt.Errorf("failed to encrypt: %w", err)
	}
	return s.storage.Upload(ctx, key, encrypted)
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

	homeDir, _ := os.UserHomeDir()
	mapper, err := NewPathMapper(homeDir, cfg.PathMap)
	if err != nil {
		return nil, err
	}

	return &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     quiet,
		cfg:       cfg,
		paths:     mapper,
	}, nil
}

// NewSyncerWith creates a Syncer with pre-built dependencies (for testing).
func NewSyncerWith(cfg *config.Config, store storage.Storage, enc *crypto.Encryptor, state *SyncState, claudeDir string, quiet bool) *Syncer {
	homeDir, _ := os.UserHomeDir()
	mapper, _ := NewPathMapper(homeDir, cfg.PathMap)
	return &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     quiet,
		cfg:       cfg,
		paths:     mapper,
	}
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
	// Per-machine debris never syncs, regardless of user excludes. A .lock is
	// a local process lock — on another machine it is indistinguishable from a
	// lock genuinely held there. A .conflict.<ts> file is this tool's own local
	// recovery artifact; uploading it replicates the artifact to every machine,
	// where each copy can itself be re-detected and spawn further ones.
	base := filepath.Base(relPath)
	if base == ".lock" || conflictArtifactRe.MatchString(base) {
		return true
	}
	return s.cfg.IsExcluded(relPath)
}

// syncPaths returns the set of ~/.claude paths to sync, honoring both the
// configured scope ("full" by default, or "sessions" for portable data only)
// and any sync_paths override, with scope acting as a ceiling.
func (s *Syncer) syncPaths() []string {
	return s.cfg.GetEffectiveSyncPaths()
}

// Scope returns the configured sync scope (empty means the default "full").
func (s *Syncer) Scope() string {
	return s.cfg.Scope
}

// SyncPaths returns the effective ~/.claude paths this syncer operates on, so
// callers such as the pre-pull backup cover exactly the set that pull can
// overwrite rather than recomputing it from scope alone.
func (s *Syncer) SyncPaths() []string {
	return s.syncPaths()
}

func (s *Syncer) log(format string, args ...interface{}) {
	if !s.quiet {
		fmt.Printf(format+"\n", args...)
	}
}

func (s *Syncer) Push(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	s.progress(ProgressEvent{Action: "scan", Path: "Detecting changes..."})

	changes, err := s.state.DetectChanges(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to detect changes: %w", err)
	}

	if len(changes) == 0 {
		s.progress(ProgressEvent{Action: "scan", Complete: true})
		// Desktop-only changes (rename/archive/new app session) touch no
		// ~/.claude file, so records must still publish on an otherwise
		// no-op push.
		s.pushCCDSessions(ctx, result)
		if err := s.state.Save(); err != nil {
			return result, fmt.Errorf("failed to save state: %w", err)
		}
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
					if errors.Is(err, errSkipUpload) {
						// Deliberate inaction, not a failure: the file is
						// neither uploaded nor errored.
						return
					}
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
		// The bucket's history.jsonl is a union that only ever grows — a
		// locally deleted history file must not delete every other machine's
		// entries with it. Drop the state entry instead; the next pull
		// restores the file locally.
		var remoteDeletes []FileChange
		for _, change := range deletes {
			if change.Path == HistoryFile {
				s.state.RemoveFile(change.Path)
				continue
			}
			remoteDeletes = append(remoteDeletes, change)
		}

		if len(remoteDeletes) > 0 {
			deleteKeys := make([]string, len(remoteDeletes))
			for i, change := range remoteDeletes {
				deleteKeys[i] = s.remoteKey(change.Path)
			}
			if err := s.storage.DeleteBatch(ctx, deleteKeys); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("batch delete: %w", err))
			} else {
				for _, change := range remoteDeletes {
					s.state.RemoveFile(change.Path)
					result.Deleted = append(result.Deleted, change.Path)
				}
			}
		}
	}

	s.progress(ProgressEvent{Action: "upload", Complete: true, Total: total})

	// Upload manifest with file mtimes for cross-device mtime preservation
	if len(result.Uploaded) > 0 || len(result.Deleted) > 0 {
		if err := s.uploadManifest(ctx); err != nil {
			// Log but don't fail - manifest is best-effort
			s.log("Warning: failed to upload manifest: %v", err)
		}
	}

	// Sync desktop-app session records BEFORE saving state — their state
	// entries must persist or every future hook re-transfers every record.
	s.pushCCDSessions(ctx, result)

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

	// Download manifest for mtime restoration (best-effort, may not exist)
	manifest, _ := s.downloadManifest(ctx)

	// Build remote file map
	remoteFiles, skipped := s.buildRemoteMap(remoteObjects)
	for _, key := range skipped {
		result.Errors = append(result.Errors,
			fmt.Errorf("%s: unknown path token; add the matching path_map entry on this device", key))
	}

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
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

		// A local history.jsonl is union-merged with the remote copy instead
		// of being overwritten (data loss) or kept with the remote parked in a
		// .conflict file (invisible data). Merge on first sync regardless of
		// mtimes, and afterwards whenever the remote changed since our last
		// upload. A missing local file falls through to the plain download.
		if localPath == HistoryFile && localExists {
			if stateFile == nil || remoteObj.LastModified.After(stateFile.Uploaded) {
				changed, err := s.pullMergeHistory(ctx, localPath, remoteObj)
				if err != nil {
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", localPath, err))
				} else if changed {
					result.Downloaded = append(result.Downloaded, localPath)
					s.progress(ProgressEvent{Action: "download", Path: localPath, Size: remoteObj.Size})
				}
			}
			continue
		}

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
					// Both sides changed since the last sync. For append-only
					// session transcripts that usually means one side is
					// simply ahead — a fast-forward, not a conflict. Compare
					// the bytes before declaring one (resolveJSONLConflict).
					if isSessionJSONL(localPath) {
						res, rerr := s.resolveJSONLConflict(ctx, localPath, remoteObj)
						if rerr != nil {
							result.Errors = append(result.Errors, fmt.Errorf("%s: %w", localPath, rerr))
							continue
						}
						switch res {
						case jsonlEqual, jsonlLocalAhead:
							continue
						case jsonlFastForwarded, jsonlMerged:
							result.Downloaded = append(result.Downloaded, localPath)
							s.progress(ProgressEvent{Action: "download", Path: localPath, Size: remoteObj.Size})
							continue
						}
						// jsonlConflict falls through to the standard path.
					}
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

				// Get original mtime from manifest if available
				var mtime *time.Time
				if manifest != nil {
					if meta, ok := manifest.Files[task.localPath]; ok {
						mtime = &meta.ModTime
					}
				}

				if err := s.downloadFile(ctx, task.localPath, task.remoteObj.Key, mtime); err != nil {
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

	// Sync desktop-app session records BEFORE saving state (their entries must
	// persist to avoid re-fetching every record on every pull).
	s.pullCCDSessions(ctx, result)

	s.state.LastPull = time.Now()
	s.state.LastSync = time.Now()
	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}

	return result, nil
}

func (s *Syncer) Status(ctx context.Context) ([]FileChange, error) {
	return s.state.DetectChanges(s.claudeDir, s.syncPaths(), s.isExcluded)
}

func (s *Syncer) uploadFile(ctx context.Context, relativePath string) error {
	fullPath := filepath.Join(s.claudeDir, relativePath)

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// history.jsonl is one file appended to by every machine, so a plain
	// upload is last-writer-wins and silently drops the other machines'
	// prompt-history entries. Union-merging the remote copy into the upload
	// keeps the bucket copy a superset of every machine's history.
	if relativePath == HistoryFile {
		merged, err := s.mergeHistoryForPush(ctx, relativePath, fullPath, data)
		if err != nil {
			return err
		}
		data = merged
	}

	// A session transcript that SHRANK since the last push is far more likely
	// a truncated copy (partial restore, torn sync) than a real rewrite — and
	// uploading it would clobber the fuller bucket copy for every machine.
	// When local is a (possibly equal) prefix of the current remote, skip the
	// upload; the next pull fast-forwards local instead. State is left
	// untouched so the file is re-examined on the next push. The remote
	// round-trip is paid only in the shrunk case, and any fetch error
	// (not-found included) falls through to a normal upload — this guard must
	// never turn a push into a hard failure.
	if isSessionJSONL(relativePath) {
		if st := s.state.GetFile(relativePath); st != nil && int64(len(data)) < st.Size {
			if remote, ferr := s.fetchDecoded(ctx, relativePath, s.remoteKey(relativePath)); ferr == nil {
				switch ClassifyPrefix(data, remote) {
				case PrefixEqual, PrefixRemoteAhead:
					s.log("Skipping upload of %s: local copy is behind the bucket copy", relativePath)
					return errSkipUpload
				}
			}
		}
	}

	// Local-form bytes actually being uploaded; for history the state hash is
	// computed from this instead of re-reading the file, so any line a live
	// session appends after our read still differs from state and gets pushed.
	localForm := data

	// Replace machine-specific paths with portable tokens in session content
	if IsPortableContentPath(relativePath) {
		data = s.paths.NormalizeContent(data)
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
	var hash string
	if relativePath == HistoryFile {
		hash = HashBytes(localForm)
	} else {
		hash, _ = HashFile(fullPath)
	}
	s.state.UpdateFile(relativePath, info, hash)
	s.state.MarkUploaded(relativePath)

	return nil
}

// fetchDecoded downloads a remote object and returns its plaintext in LOCAL
// form: decrypted, decompressed, and with portable tokens resolved to this
// device's paths. Factored out of downloadFile so callers can inspect remote
// content without writing it to disk, and so a byte comparison against the
// local file compares like with like.
func (s *Syncer) fetchDecoded(ctx context.Context, relativePath, remoteKey string) ([]byte, error) {
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}

	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	// Decompress if gzipped (backward-compatible with uncompressed data)
	if isGzipped(data) {
		data, err = gzipDecompress(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress: %w", err)
		}
	}

	// Replace portable tokens with this device's paths in session content
	if IsPortableContentPath(relativePath) {
		data = s.paths.ResolveContent(data)
	}
	return data, nil
}

// mergeHistoryForPush returns the union of the local history payload and the
// current remote copy. When the remote contributes entries they are APPENDED
// to the local file (never a rewrite — a rewrite from a stale read would
// destroy lines a live Claude Code session appends during the merge's network
// round-trip). A missing remote object (first push) is not an error; any
// other failure — a transient Head error included — aborts the upload rather
// than clobbering entries that could not be merged.
func (s *Syncer) mergeHistoryForPush(ctx context.Context, relativePath, fullPath string, local []byte) ([]byte, error) {
	remoteKey := s.remoteKey(relativePath)
	if _, err := s.storage.Head(ctx, remoteKey); err != nil {
		if storage.IsNotFound(err) {
			// No remote copy yet — nothing to merge.
			return local, nil
		}
		return nil, fmt.Errorf("checking remote history before merge: %w", err)
	}

	remote, err := s.fetchDecoded(ctx, relativePath, remoteKey)
	if err != nil {
		return nil, fmt.Errorf("fetching remote history for merge: %w", err)
	}

	merged, addedLines, err := MergeHistoryPayloads(local, remote)
	if err != nil {
		return nil, fmt.Errorf("merging history: %w", err)
	}
	if len(addedLines) == 0 {
		return local, nil
	}

	if err := appendHistoryLines(fullPath, addedLines); err != nil {
		return nil, fmt.Errorf("appending merged history: %w", err)
	}
	return merged, nil
}

// pullMergeHistory folds the remote history into the local file by APPENDING
// the missing lines, instead of overwriting the file or declaring a conflict.
// State is intentionally left untouched when lines are appended: the local
// hash then differs from the last-uploaded hash, so the next push detects the
// change and publishes the union back to the bucket.
func (s *Syncer) pullMergeHistory(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo) (bool, error) {
	remote, err := s.fetchDecoded(ctx, relativePath, remoteObj.Key)
	if err != nil {
		return false, err
	}

	fullPath := filepath.Join(s.claudeDir, relativePath)
	local, err := os.ReadFile(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	_, addedLines, err := MergeHistoryPayloads(local, remote)
	if err != nil {
		return false, err
	}
	if len(addedLines) == 0 {
		// Local is already a superset; if it differs from state the next push
		// will publish it.
		return false, nil
	}

	if err := appendHistoryLines(fullPath, addedLines); err != nil {
		return false, err
	}
	return true, nil
}

// downloadFile downloads and decrypts a file from remote storage.
// If originalMtime is non-nil, the file's modification time will be restored to that value.
func (s *Syncer) downloadFile(ctx context.Context, relativePath, remoteKey string, originalMtime *time.Time) error {
	data, err := s.fetchDecoded(ctx, relativePath, remoteKey)
	if err != nil {
		return err
	}

	// Guard against path traversal from crafted remote keys
	fullPath := filepath.Join(s.claudeDir, relativePath)
	if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(s.claudeDir)+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write outside %s: %s", s.claudeDir, relativePath)
	}

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Transcripts can contain secrets echoed by tools: keep them user-only
	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Restore original modification time if provided
	if originalMtime != nil {
		if err := os.Chtimes(fullPath, *originalMtime, *originalMtime); err != nil {
			// Log but don't fail - mtime restoration is best-effort
			s.log("Warning: failed to restore mtime for %s: %v", relativePath, err)
		}
	}

	// Update state
	info, _ := os.Stat(fullPath)
	hash, _ := HashFile(fullPath)
	s.state.UpdateFile(relativePath, info, hash)
	s.state.MarkUploaded(relativePath)

	return nil
}

// jsonlResolution is the outcome of resolveJSONLConflict. jsonlConflict is the
// zero value and is also what accompanies any error return.
type jsonlResolution int

const (
	jsonlConflict      jsonlResolution = iota // genuinely diverged: caller runs handleConflict
	jsonlEqual                                // same bytes: state refreshed, nothing written
	jsonlFastForwarded                        // local extended with the remote's missing tail
	jsonlLocalAhead                           // local kept; the next push publishes it
	jsonlMerged                               // diverged transcripts united by MergeSessionPayloads
)

// resolveJSONLConflict re-examines an apparent both-sides-changed conflict on
// an append-only session transcript before it is declared.
//
// In a 92-artifact corpus every such "conflict" was a strict byte-prefix
// relation: one side simply ahead (an active session appending between two
// syncs) or one side truncated (a partial restore). Writing a .conflict file
// for those either loses nothing or, in the truncated case, parks the ONLY
// complete copy in a file Claude Code never reads.
//
// The remote payload is downloaded here, but that costs nothing extra: the
// conflict path this replaces already downloads it to write the .conflict file.
func (s *Syncer) resolveJSONLConflict(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo) (jsonlResolution, error) {
	remote, err := s.fetchDecoded(ctx, relativePath, remoteObj.Key)
	if err != nil {
		return jsonlConflict, err
	}
	fullPath := filepath.Join(s.claudeDir, relativePath)
	local, err := os.ReadFile(fullPath)
	if err != nil {
		return jsonlConflict, err
	}

	switch ClassifyPrefix(local, remote) {
	case PrefixEqual:
		// Same content on both sides. Refresh state so this file stops
		// re-triggering conflict detection on every subsequent pull.
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			return jsonlConflict, statErr
		}
		s.state.UpdateFile(relativePath, info, HashBytes(local))
		s.state.MarkUploaded(relativePath)
		return jsonlEqual, nil

	case PrefixRemoteAhead:
		// Remote strictly extends local (typically a truncated local copy).
		// Append ONLY the missing tail with O_APPEND — never truncate-rewrite.
		// If a live session appends between our read and this write, an append
		// yields divergent ordering that the next pull classifies as a real
		// conflict (zero bytes lost), whereas a rewrite would silently drop
		// those lines.
		f, oerr := os.OpenFile(fullPath, os.O_APPEND|os.O_WRONLY, 0600)
		if oerr != nil {
			return jsonlConflict, oerr
		}
		if _, werr := f.Write(remote[len(local):]); werr != nil {
			_ = f.Close()
			return jsonlConflict, werr
		}
		if cerr := f.Close(); cerr != nil {
			return jsonlConflict, cerr
		}

		final, rerr := os.ReadFile(fullPath)
		if rerr != nil {
			return jsonlConflict, rerr
		}
		if bytes.Equal(final, remote) {
			// Clean fast-forward: record the new content, and that the bucket
			// already holds exactly these bytes.
			info, statErr := os.Stat(fullPath)
			if statErr != nil {
				return jsonlConflict, statErr
			}
			s.state.UpdateFile(relativePath, info, HashBytes(final))
			s.state.MarkUploaded(relativePath)
		}
		// Otherwise a live session appended during the fast-forward, so the
		// file is local+concurrent+tail while the bucket holds local+tail.
		// Leave state UNTOUCHED on purpose: UpdateFile resets the Uploaded
		// timestamp, so recording the new hash would make the next pull see
		// "local unchanged, remote newer" and rewrite the file from the
		// bucket, silently dropping the concurrent lines. With state stale the
		// next pull re-enters this resolver, classifies PrefixNone and writes
		// a real .conflict file, and the next push publishes the full local
		// file. Nothing is lost either way.
		return jsonlFastForwarded, nil

	case PrefixLocalAhead:
		// Local strictly extends remote: the bucket is simply behind this
		// machine. Keep local and leave state untouched so the next push
		// publishes it. A .conflict copy of a stale prefix is pure noise.
		return jsonlLocalAhead, nil

	case PrefixNone:
		// Genuinely diverged: the same session advanced on two machines.
		// Union-merge by APPENDING what the remote has that local lacks
		// (MergeSessionPayloads never rewrites local). Whose mutable state
		// wins is decided by file-level times — the records themselves carry
		// no timestamp to order by. LastModified is upload time and lags the
		// remote write by up to one push delay, so near-ties resolve in
		// remote's favour — a bounded, self-correcting bias (stale state
		// until the next local state write). Any merge failure degrades to
		// the legacy keep-local-plus-.conflict path rather than blocking the
		// pull.
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			return jsonlConflict, statErr
		}
		localNewer := info.ModTime().After(remoteObj.LastModified)
		added, merr := MergeSessionPayloads(local, remote, localNewer)
		if merr != nil {
			return jsonlConflict, nil
		}
		if len(added) == 0 {
			// Local already carries everything the remote has. Keep it; state
			// stays untouched so the next push publishes the union.
			return jsonlLocalAhead, nil
		}
		if aerr := appendHistoryLines(fullPath, added); aerr != nil {
			return jsonlConflict, nil
		}
		// State intentionally untouched (same convention as pullMergeHistory):
		// the local hash now differs from the last-uploaded hash, so the next
		// push publishes the union.
		return jsonlMerged, nil
	}
	return jsonlConflict, nil
}

func (s *Syncer) handleConflict(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo) error {
	s.log("Conflict detected: %s (keeping local, saving remote as .conflict)", relativePath)

	// Download remote version with conflict suffix
	conflictPath := relativePath + ".conflict." + time.Now().Format("20060102-150405")
	if err := s.downloadFile(ctx, conflictPath, remoteObj.Key, nil); err != nil {
		return fmt.Errorf("failed to save conflict file: %w", err)
	}

	return nil
}

// uploadManifest builds and uploads a manifest containing file mtimes from current state.
func (s *Syncer) uploadManifest(ctx context.Context) error {
	manifest := FileManifest{
		Files: make(map[string]FileMetadata),
	}

	// Build manifest from current state
	s.state.mu.Lock()
	for path, fs := range s.state.Files {
		manifest.Files[path] = FileMetadata{
			ModTime: fs.ModTime,
		}
	}
	s.state.mu.Unlock()

	// Serialize manifest
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	// Compress
	compressed, err := gzipCompress(data)
	if err != nil {
		return fmt.Errorf("failed to compress manifest: %w", err)
	}

	// Encrypt
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return fmt.Errorf("failed to encrypt manifest: %w", err)
	}

	// Upload
	remoteKey := ManifestKey + ".age"
	if err := s.storage.Upload(ctx, remoteKey, encrypted); err != nil {
		return fmt.Errorf("failed to upload manifest: %w", err)
	}

	return nil
}

// downloadManifest downloads and parses the file manifest from remote storage.
// Returns nil if no manifest exists (backward compatibility with older syncs).
func (s *Syncer) downloadManifest(ctx context.Context) (*FileManifest, error) {
	remoteKey := ManifestKey + ".age"

	// Download
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		// Manifest may not exist for older syncs - that's OK
		return nil, nil
	}

	// Decrypt
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt manifest: %w", err)
	}

	// Decompress if gzipped
	if isGzipped(data) {
		data, err = gzipDecompress(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress manifest: %w", err)
		}
	}

	// Parse
	var manifest FileManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

func (s *Syncer) remoteKey(relativePath string) string {
	// Normalize machine-specific path segments, add .age extension
	return s.paths.NormalizeRelPath(relativePath) + ".age"
}

// localPath maps a remote key back to a local relative path. ok is false when
// the key uses a path_map token this device doesn't define.
func (s *Syncer) localPath(remoteKey string) (string, bool) {
	return s.paths.ResolveRelPath(strings.TrimSuffix(remoteKey, ".age"))
}

// buildRemoteMap maps remote objects to local relative paths, skipping
// non-encrypted keys, MCP data, excluded paths, and keys with unknown path
// tokens (reported via skipped). When a legacy un-normalized key and its
// normalized replacement both exist, the normalized one wins.
func (s *Syncer) buildRemoteMap(remoteObjects []storage.ObjectInfo) (remoteFiles map[string]storage.ObjectInfo, skipped []string) {
	remoteFiles = make(map[string]storage.ObjectInfo)
	for _, obj := range remoteObjects {
		// Skip non-encrypted files
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		localPath, ok := s.localPath(obj.Key)
		if !ok {
			skipped = append(skipped, obj.Key)
			continue
		}
		// Skip external files (handled by MCP sync)
		if strings.HasPrefix(localPath, "_external/") {
			continue
		}
		// Skip metadata files (manifest, etc.)
		if strings.HasPrefix(localPath, "_metadata/") {
			continue
		}
		// Skip excluded paths
		if s.isExcluded(localPath) {
			continue
		}
		if existing, dup := remoteFiles[localPath]; dup {
			// Prefer the canonical (normalized) key over a legacy duplicate
			if existing.Key == s.remoteKey(localPath) {
				continue
			}
		}
		remoteFiles[localPath] = obj
	}
	return remoteFiles, skipped
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
	remoteFiles, _ := s.buildRemoteMap(remoteObjects)

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
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
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Get remote files
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	remoteFiles, _ := s.buildRemoteMap(remoteObjects)

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

// claudeJSONPath returns the path to ~/.claude.json, respecting test overrides.
func (s *Syncer) claudeJSONPath() string {
	if s.cfg.ClaudeJSONOverride != "" {
		return s.cfg.ClaudeJSONOverride
	}
	return config.ClaudeJSONPath()
}

// PushMCP reads local MCP server configs, normalizes paths, and uploads them.
func (s *Syncer) PushMCP(ctx context.Context) (*MCPPushResult, error) {
	result := &MCPPushResult{}

	claudeJSON := s.claudeJSONPath()
	servers, err := ReadMCPServers(claudeJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP servers: %w", err)
	}
	if len(servers) == 0 {
		result.Unchanged = true
		return result, nil
	}

	homeDir, _ := os.UserHomeDir()
	normalized, err := NormalizeMCPServers(servers, homeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize MCP paths: %w", err)
	}

	// Check if anything changed vs last push
	newHash, err := HashMCPServers(normalized)
	if err != nil {
		return nil, fmt.Errorf("failed to hash MCP servers: %w", err)
	}

	stateFile := s.state.GetFile(config.MCPRemoteKey)
	if stateFile != nil && stateFile.Hash == newHash {
		result.Unchanged = true
		return result, nil
	}

	// Serialize, compress, encrypt, upload
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize MCP servers: %w", err)
	}

	compressed, err := gzipCompress(data)
	if err != nil {
		return nil, fmt.Errorf("failed to compress: %w", err)
	}

	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt: %w", err)
	}

	remoteKey := config.MCPRemoteKey + ".age"
	if err := s.storage.Upload(ctx, remoteKey, encrypted); err != nil {
		return nil, fmt.Errorf("failed to upload MCP servers: %w", err)
	}

	// Update state
	s.state.mu.Lock()
	s.state.Files[config.MCPRemoteKey] = &FileState{
		Path:     config.MCPRemoteKey,
		Hash:     newHash,
		Size:     int64(len(data)),
		ModTime:  time.Now(),
		Uploaded: time.Now(),
	}
	s.state.mu.Unlock()

	if err := s.state.SetMCPBaseline(normalized); err != nil {
		return nil, fmt.Errorf("failed to save MCP baseline: %w", err)
	}

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	result.ServersPushed = len(normalized)
	return result, nil
}

// PullMCP downloads remote MCP server configs and merges them with local configs.
func (s *Syncer) PullMCP(ctx context.Context) (*MCPPullResult, error) {
	result := &MCPPullResult{}

	// Download remote MCP data
	remoteKey := config.MCPRemoteKey + ".age"
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		// If the key doesn't exist, no remote MCP data
		result.NoRemote = true
		return result, nil
	}

	decrypted, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt MCP data: %w", err)
	}

	if isGzipped(decrypted) {
		decrypted, err = gzipDecompress(decrypted)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress MCP data: %w", err)
		}
	}

	var remoteServers MCPServers
	if err := json.Unmarshal(decrypted, &remoteServers); err != nil {
		return nil, fmt.Errorf("failed to parse remote MCP servers: %w", err)
	}

	// Read local servers
	claudeJSON := s.claudeJSONPath()
	localServers, err := ReadMCPServers(claudeJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to read local MCP servers: %w", err)
	}
	if localServers == nil {
		localServers = make(MCPServers)
	}

	// Normalize local for comparison
	homeDir, _ := os.UserHomeDir()
	localNormalized, err := NormalizeMCPServers(localServers, homeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize local MCP paths: %w", err)
	}

	// Load baseline
	baseline, err := s.state.GetMCPBaseline()
	if err != nil {
		return nil, fmt.Errorf("failed to load MCP baseline: %w", err)
	}
	if baseline == nil {
		baseline = make(MCPServers)
	}

	// Three-way merge
	mergeResult := MergeMCPServers(localNormalized, remoteServers, baseline)

	// Resolve paths in merged result
	resolved, err := ResolveMCPServers(mergeResult.Merged, homeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve MCP paths: %w", err)
	}

	// Write merged result back to claude.json
	if err := WriteMCPServers(claudeJSON, resolved); err != nil {
		return nil, fmt.Errorf("failed to write MCP servers: %w", err)
	}

	// Update baseline to the merged normalized state
	if err := s.state.SetMCPBaseline(mergeResult.Merged); err != nil {
		return nil, fmt.Errorf("failed to save MCP baseline: %w", err)
	}

	// Update file state
	newHash, _ := HashMCPServers(mergeResult.Merged)
	s.state.mu.Lock()
	s.state.Files[config.MCPRemoteKey] = &FileState{
		Path:     config.MCPRemoteKey,
		Hash:     newHash,
		Size:     int64(len(decrypted)),
		ModTime:  time.Now(),
		Uploaded: time.Now(),
	}
	s.state.mu.Unlock()

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	result.Added = mergeResult.Added
	result.Updated = mergeResult.Updated
	result.Kept = mergeResult.Kept
	result.Conflicts = mergeResult.Conflicts
	return result, nil
}

// MCPStatus returns the current state of local MCP servers compared to the last sync.
type MCPStatusResult struct {
	Servers     MCPServers
	HasChanges  bool
	ServerCount int
}

func (s *Syncer) MCPStatus(ctx context.Context) (*MCPStatusResult, error) {
	claudeJSON := s.claudeJSONPath()
	servers, err := ReadMCPServers(claudeJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP servers: %w", err)
	}

	result := &MCPStatusResult{
		Servers:     servers,
		ServerCount: len(servers),
	}

	if servers == nil {
		return result, nil
	}

	homeDir, _ := os.UserHomeDir()
	normalized, err := NormalizeMCPServers(servers, homeDir)
	if err != nil {
		return nil, err
	}

	newHash, err := HashMCPServers(normalized)
	if err != nil {
		return nil, err
	}

	stateFile := s.state.GetFile(config.MCPRemoteKey)
	result.HasChanges = stateFile == nil || stateFile.Hash != newHash

	return result, nil
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
	defer func() { _ = r.Close() }()

	// Limit decompressed size to prevent decompression bomb attacks
	limited := io.LimitReader(r, maxDecompressedSize+1)
	result, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > maxDecompressedSize {
		return nil, fmt.Errorf("decompressed data exceeds %d bytes limit", maxDecompressedSize)
	}
	return result, nil
}
