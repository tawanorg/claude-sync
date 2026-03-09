package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
)

type FileState struct {
	Path     string    `json:"path"`
	Hash     string    `json:"hash"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Uploaded time.Time `json:"uploaded,omitempty"`
}

type SyncState struct {
	Files    map[string]*FileState `json:"files"`
	LastSync time.Time             `json:"last_sync"`
	DeviceID string                `json:"device_id"`
	LastPush time.Time             `json:"last_push,omitempty"`
	LastPull time.Time             `json:"last_pull,omitempty"`

	// savePath is the custom path to save state to (if set)
	savePath string `json:"-"`
	mu       sync.Mutex `json:"-"`
}

func LoadState() (*SyncState, error) {
	return loadStateFromPath(config.StateFilePath())
}

// LoadStateFromDir loads state from a custom directory (for testing)
func LoadStateFromDir(dir string) (*SyncState, error) {
	statePath := filepath.Join(dir, config.StateFile)
	state, err := loadStateFromPath(statePath)
	if err != nil {
		return nil, err
	}
	state.savePath = statePath
	return state, nil
}

func loadStateFromPath(statePath string) (*SyncState, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("failed to read state: %w", err)
	}

	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state: %w", err)
	}

	if state.Files == nil {
		state.Files = make(map[string]*FileState)
	}

	return &state, nil
}

func NewState() *SyncState {
	hostname, _ := os.Hostname()
	return &SyncState{
		Files:    make(map[string]*FileState),
		DeviceID: hostname,
	}
}

func (s *SyncState) Save() error {
	statePath := s.savePath
	if statePath == "" {
		statePath = config.StateFilePath()
	}

	// Ensure directory exists
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}

	return nil
}

func (s *SyncState) UpdateFile(relativePath string, info os.FileInfo, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Files[relativePath] = &FileState{
		Path:    relativePath,
		Hash:    hash,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
}

func (s *SyncState) MarkUploaded(relativePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.Files[relativePath]; ok {
		f.Uploaded = time.Now()
	}
}

func (s *SyncState) GetFile(relativePath string) *FileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Files[relativePath]
}

func (s *SyncState) RemoveFile(relativePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Files, relativePath)
}

// IsEmpty returns true if no files have been synced yet (first sync)
func (s *SyncState) IsEmpty() bool {
	return len(s.Files) == 0 && s.LastSync.IsZero()
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func GetLocalFiles(claudeDir string, syncPaths []string, excludeFn ...func(string) bool) (map[string]os.FileInfo, error) {
	files := make(map[string]os.FileInfo)

	// Use the first exclude function if provided
	var isExcluded func(string) bool
	if len(excludeFn) > 0 && excludeFn[0] != nil {
		isExcluded = excludeFn[0]
	}

	for _, syncPath := range syncPaths {
		fullPath := filepath.Join(claudeDir, syncPath)

		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to stat %s: %w", syncPath, err)
		}

		if info.IsDir() {
			err := filepath.Walk(fullPath, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				relPath, _ := filepath.Rel(claudeDir, path)
				// Normalize to forward slashes for consistent matching
				relPath = filepath.ToSlash(relPath)

				// Skip excluded directories entirely
				if fi.IsDir() {
					if isExcluded != nil && isExcluded(relPath) {
						return filepath.SkipDir
					}
					return nil
				}
				// Skip symlinks
				if fi.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				// Skip excluded files
				if isExcluded != nil && isExcluded(relPath) {
					return nil
				}

				files[relPath] = fi
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("failed to walk %s: %w", syncPath, err)
			}
		} else {
			// Skip symlinks
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			// Skip excluded files
			if isExcluded != nil && isExcluded(syncPath) {
				continue
			}
			files[syncPath] = info
		}
	}

	return files, nil
}

type FileChange struct {
	Path      string
	Action    string // "add", "modify", "delete"
	LocalHash string
	LocalSize int64
	LocalTime time.Time
}

func (s *SyncState) DetectChanges(claudeDir string, syncPaths []string, excludeFn ...func(string) bool) ([]FileChange, error) {
	var changes []FileChange

	localFiles, err := GetLocalFiles(claudeDir, syncPaths, excludeFn...)
	if err != nil {
		return nil, err
	}

	// Check for new or modified files
	for relPath, info := range localFiles {
		fullPath := filepath.Join(claudeDir, relPath)
		hash, err := HashFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s: %w", relPath, err)
		}

		existing := s.GetFile(relPath)
		if existing == nil {
			changes = append(changes, FileChange{
				Path:      relPath,
				Action:    "add",
				LocalHash: hash,
				LocalSize: info.Size(),
				LocalTime: info.ModTime(),
			})
		} else if existing.Hash != hash {
			changes = append(changes, FileChange{
				Path:      relPath,
				Action:    "modify",
				LocalHash: hash,
				LocalSize: info.Size(),
				LocalTime: info.ModTime(),
			})
		}
	}

	// Check for deleted files
	for relPath := range s.Files {
		if _, exists := localFiles[relPath]; !exists {
			changes = append(changes, FileChange{
				Path:   relPath,
				Action: "delete",
			})
		}
	}

	return changes, nil
}
