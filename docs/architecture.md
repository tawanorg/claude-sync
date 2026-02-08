---
layout: default
title: Architecture
---

# Architecture

[Home](./index) | [How It Works](./how-it-works) | [Security](./security)

---

## System Overview

Claude Sync follows a **layered architecture** with a pluggable storage abstraction:

```
┌─────────────────────────────────────────────────────────┐
│                    CLI Layer                             │
│         cmd/claude-sync/main.go (~1500 lines)           │
│                                                          │
│  Commands: init, push, pull, status, diff, conflicts,   │
│            reset, update, version                        │
│  UI: Interactive prompts (survey), progress reporting   │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                   Sync Layer                             │
│              internal/sync/                              │
│                                                          │
│  sync.go   - Syncer struct, push/pull orchestration     │
│  state.go  - SyncState, FileState, change detection     │
└────────────────────────┬────────────────────────────────┘
                         │
           ┌─────────────┴─────────────┐
           │                           │
┌──────────▼──────────┐    ┌───────────▼───────────┐
│    Crypto Layer     │    │    Storage Layer       │
│  internal/crypto/   │    │   internal/storage/    │
│                     │    │                        │
│  encrypt.go         │    │  storage.go (interface)│
│  - Encrypt()        │    │  config.go  (unified)  │
│  - Decrypt()        │    │                        │
│  - GenerateKey()    │    │  ┌─────────────────┐   │
│  - DeriveKey()      │    │  │   Adapters      │   │
│                     │    │  ├─────────────────┤   │
│                     │    │  │ r2/r2.go        │   │
│                     │    │  │ s3/s3.go        │   │
│                     │    │  │ gcs/gcs.go      │   │
│                     │    │  └─────────────────┘   │
└──────────┬──────────┘    └───────────┬───────────┘
           │                           │
┌──────────▼───────────────────────────▼───────────┐
│                  Config Layer                     │
│               internal/config/                    │
│                                                   │
│  config.go - YAML config, path resolution         │
│  Backward compatible with legacy R2-only format   │
└──────────────────────────────────────────────────┘
```

---

## Storage Abstraction

The storage layer uses an **interface-based adapter pattern** to support multiple cloud providers:

### Storage Interface

```go
// internal/storage/storage.go
type Storage interface {
    Upload(ctx context.Context, key string, data []byte) error
    Download(ctx context.Context, key string) ([]byte, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]ObjectInfo, error)
    Head(ctx context.Context, key string) (*ObjectInfo, error)
    BucketExists(ctx context.Context) (bool, error)
}
```

### Provider Adapters

| Adapter | File | SDK Used |
|---------|------|----------|
| **R2** | `internal/storage/r2/r2.go` | AWS SDK v2 (S3-compatible) |
| **S3** | `internal/storage/s3/s3.go` | AWS SDK v2 |
| **GCS** | `internal/storage/gcs/gcs.go` | Google Cloud Storage SDK |

### Unified Configuration

```go
// internal/storage/config.go
type StorageConfig struct {
    Provider Provider  // r2, s3, or gcs
    Bucket   string

    // R2/S3 common
    AccessKeyID     string
    SecretAccessKey string
    Endpoint        string
    Region          string

    // R2-specific
    AccountID string

    // GCS-specific
    ProjectID             string
    CredentialsFile       string
    CredentialsJSON       string
    UseDefaultCredentials bool
}
```

### Factory Pattern

```go
func New(cfg *StorageConfig) (Storage, error) {
    switch cfg.Provider {
    case ProviderR2:
        return NewR2(cfg)
    case ProviderS3:
        return NewS3(cfg)
    case ProviderGCS:
        return NewGCS(cfg)
    default:
        return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
    }
}
```

### Adapter Registration

Adapters self-register using Go's `init()` pattern:

```go
// internal/storage/r2/r2.go
func init() {
    storage.NewR2 = NewR2Adapter
}
```

This allows the main binary to include only needed adapters via imports:

```go
// cmd/claude-sync/main.go
import (
    _ "github.com/tawanorg/claude-sync/internal/storage/gcs"
    _ "github.com/tawanorg/claude-sync/internal/storage/r2"
    _ "github.com/tawanorg/claude-sync/internal/storage/s3"
)
```

---

## Package Breakdown

### `cmd/claude-sync/main.go`

The CLI entry point using [Cobra](https://github.com/spf13/cobra) and [Survey](https://github.com/AlecAivazis/survey).

**Key Components:**
- Command definitions (init, push, pull, etc.)
- Interactive provider wizards (`runR2Wizard`, `runS3Wizard`, `runGCSWizard`)
- Progress reporting with ANSI colors
- Self-update logic via GitHub API

**Interactive Wizards:**
```
init → Select Provider → Provider-specific wizard → Encryption setup → Test connection
         ↓
    ┌────┴────┬────────────┐
    │         │            │
   R2        S3          GCS
  wizard    wizard      wizard
```

### `internal/sync/`

Orchestrates synchronization operations.

**sync.go - Syncer struct:**
```go
type Syncer struct {
    claudeDir   string           // ~/.claude
    storage     storage.Storage  // Provider-agnostic interface
    keyPath     string           // Path to age key
    state       *SyncState
    statePath   string           // ~/.claude-sync/state.json
    quiet       bool
    progressFn  func(ProgressEvent)
}
```

**Key Methods:**
- `Push(ctx)` - Detect changes, encrypt, upload
- `Pull(ctx)` - Fetch remote state, download, decrypt
- `Status(ctx)` - List pending local changes
- `Diff(ctx)` - Compare local vs remote

**state.go - State Management:**
```go
type FileState struct {
    Path     string    // Relative path (e.g., "projects/foo/session.json")
    Hash     string    // SHA256 hash of file contents
    Size     int64     // File size in bytes
    ModTime  time.Time // Local modification time
    Uploaded time.Time // When last pushed to storage
}

type SyncState struct {
    Files    map[string]*FileState
    LastSync time.Time
    DeviceID string    // Hostname
    LastPush time.Time
    LastPull time.Time
}
```

### `internal/crypto/`

Handles all encryption operations using the [age](https://github.com/FiloSottile/age) library.

**Key Functions:**
```go
func GenerateKey(keyPath string) error
func GenerateKeyFromPassphrase(keyPath, passphrase string) error
func ValidatePassphraseStrength(passphrase string) error
func KeyExists(keyPath string) bool
func Encrypt(data []byte, keyPath string) ([]byte, error)
func Decrypt(data []byte, keyPath string) ([]byte, error)
```

### `internal/config/`

Configuration management with backward compatibility.

**Config struct:**
```go
type Config struct {
    // New format (preferred)
    Storage *storage.StorageConfig `yaml:"storage,omitempty"`

    // Legacy R2 fields (backward compatible)
    AccountID       string `yaml:"account_id,omitempty"`
    AccessKeyID     string `yaml:"access_key_id,omitempty"`
    SecretAccessKey string `yaml:"secret_access_key,omitempty"`
    Bucket          string `yaml:"bucket,omitempty"`

    EncryptionKey string `yaml:"encryption_key_path"`
}
```

**Sync Paths:**
```go
var SyncPaths = []string{
    "CLAUDE.md",
    "settings.json",
    "settings.local.json",
    "agents",
    "skills",
    "plugins",
    "projects",
    "history.jsonl",
    "rules",
}
```

---

## Data Flow

### Push Operation

```
Local Files (~/.claude/)
        │
        ▼
┌───────────────────┐
│  Change Detection │  Compare with SyncState (hash, modtime)
│                   │  Result: add/modify/delete lists
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Read & Encrypt   │  For each changed file:
│  (age encryption) │  Read → Encrypt → Bytes
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Upload via       │  storage.Upload(key, data)
│  Storage Interface│  Provider handles specifics
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Update State     │  Record hash, size, upload time
│  (state.json)     │  Persist to ~/.claude-sync/state.json
└───────────────────┘
```

### Pull Operation

```
Cloud Storage (via Storage interface)
        │
        ▼
┌───────────────────┐
│  List Objects     │  storage.List("")
│                   │  Compare with local state
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Conflict Check   │  If local AND remote changed:
│                   │  → Keep local, save remote as .conflict
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Download &       │  storage.Download(key) → Decrypt → Write
│  Decrypt          │
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Update State     │  Record new hash, pull time
└───────────────────┘
```

---

## File Structure

```
~/.claude-sync/
├── config.yaml      # Storage + encryption config (0600)
├── age-key.txt      # Encryption key (0600)
└── state.json       # Sync state (file hashes, timestamps)

~/.claude/           # Claude Code directory (synced)
├── CLAUDE.md
├── settings.json
├── settings.local.json
├── history.jsonl
├── agents/
├── skills/
├── plugins/
├── projects/
│   └── <project-hash>/
│       ├── session.json
│       └── auto-memory.jsonl
└── rules/
```

### Config File Format (New)

```yaml
storage:
  provider: r2           # or s3, gcs
  bucket: claude-sync
  account_id: abc123     # R2 only
  access_key_id: AKIA...
  secret_access_key: xxx
  region: us-east-1      # S3 only
  project_id: my-project # GCS only
encryption_key_path: ~/.claude-sync/age-key.txt
```

### Config File Format (Legacy)

```yaml
# Still supported for backward compatibility
account_id: abc123
access_key_id: AKIA...
secret_access_key: xxx
bucket: claude-sync
encryption_key_path: ~/.claude-sync/age-key.txt
```

---

## Design Decisions

### Why Interface-Based Storage?

- **Flexibility**: Add new providers without changing sync logic
- **Testability**: Mock storage for unit tests
- **User Choice**: Different providers for different use cases
- **Future-Proof**: Easy to add Azure Blob, MinIO, etc.

### Why Adapter Self-Registration?

- **Clean Imports**: Main package just imports adapters
- **Optional Adapters**: Could build with subset of providers
- **No Circular Deps**: Adapters depend on interface, not vice versa

### Why age for Encryption?

- Modern, audited encryption (X25519 + ChaCha20-Poly1305)
- Simple CLI and library interface
- Deterministic key derivation support
- Small ciphertext overhead

### Why Argon2 for Key Derivation?

- Memory-hard: resistant to GPU/ASIC attacks
- Winner of Password Hashing Competition
- Parameters: 64MB memory, 3 iterations, 4 threads

### Why Hash-Based Change Detection?

- More reliable than timestamps alone
- Detects content changes regardless of touch/copy
- SHA256 is fast enough for small files

### Why Survey for Interactive UI?

- Rich prompt types (select, password, input)
- Validation support
- Cross-platform terminal support
- Clean, intuitive UX

---

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `filippo.io/age` | v1.3.1 | File encryption |
| `golang.org/x/crypto/argon2` | v0.45.0+ | Key derivation |
| `aws/aws-sdk-go-v2` | v1.41.1+ | R2/S3 storage |
| `cloud.google.com/go/storage` | v1.50.0+ | GCS storage |
| `spf13/cobra` | v1.10.2 | CLI framework |
| `AlecAivazis/survey/v2` | v2.3.7 | Interactive prompts |
| `gopkg.in/yaml.v3` | v3.0.1 | Config parsing |

---

## Next

- [How It Works](./how-it-works) - Detailed sync workflow
- [Security](./security) - Encryption and threat model
