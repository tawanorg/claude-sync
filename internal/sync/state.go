package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Files       map[string]*FileState `json:"files"`
	LastSync    time.Time             `json:"last_sync"`
	DeviceID    string                `json:"device_id"`
	LastPush    time.Time             `json:"last_push,omitempty"`
	LastPull    time.Time             `json:"last_pull,omitempty"`
}

func LoadState() (*SyncState, error) {
	statePath := config.StateFilePath()

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
	statePath := config.StateFilePath()

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
	s.Files[relativePath] = &FileState{
		Path:    relativePath,
		Hash:    hash,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
}

func (s *SyncState) MarkUploaded(relativePath string) {
	if f, ok := s.Files[relativePath]; ok {
		f.Uploaded = time.Now()
	}
}

func (s *SyncState) GetFile(relativePath string) *FileState {
	return s.Files[relativePath]
}

func (s *SyncState) RemoveFile(relativePath string) {
	delete(s.Files, relativePath)
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

func GetLocalFiles(claudeDir string, syncPaths []string) (map[string]os.FileInfo, error) {
	files := make(map[string]os.FileInfo)

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
				if fi.IsDir() {
					return nil
				}
				// Skip symlinks
				if fi.Mode()&os.ModeSymlink != 0 {
					return nil
				}

				relPath, _ := filepath.Rel(claudeDir, path)
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

func (s *SyncState) DetectChanges(claudeDir string, syncPaths []string) ([]FileChange, error) {
	var changes []FileChange

	localFiles, err := GetLocalFiles(claudeDir, syncPaths)
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
