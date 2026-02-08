---
layout: default
title: Architecture
---

# Architecture

[Home](./index) | [How It Works](./how-it-works) | [Security](./security)

---

## System Overview

Claude Sync follows a **layered architecture** with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────┐
│                    CLI Layer                             │
│         cmd/claude-sync/main.go (1200+ lines)           │
│                                                          │
│  Commands: init, push, pull, status, diff, conflicts,   │
│            reset, update, version                        │
│  Responsibilities: User I/O, progress reporting,        │
│                    interactive prompts, flag parsing     │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│                   Sync Layer                             │
│              internal/sync/                              │
│                                                          │
│  sync.go   - Syncer struct, push/pull orchestration     │
│  state.go  - SyncState, FileState, change detection     │
│                                                          │
│  Responsibilities: Change detection, conflict handling,  │
│                    state persistence, file operations    │
└────────────────────────┬────────────────────────────────┘
                         │
           ┌─────────────┴─────────────┐
           │                           │
┌──────────▼──────────┐    ┌───────────▼───────────┐
│    Crypto Layer     │    │    Storage Layer       │
│  internal/crypto/   │    │   internal/storage/    │
│                     │    │                        │
│  encrypt.go         │    │  r2.go                 │
│  - Encrypt()        │    │  - R2Client            │
│  - Decrypt()        │    │  - Upload()            │
│  - GenerateKey()    │    │  - Download()          │
│  - DeriveKey()      │    │  - List()              │
│                     │    │  - Delete()            │
└──────────┬──────────┘    └───────────┬───────────┘
           │                           │
┌──────────▼───────────────────────────▼───────────┐
│                  Config Layer                     │
│               internal/config/                    │
│                                                   │
│  config.go - YAML config, path resolution         │
│  Paths: ~/.claude-sync/config.yaml                │
│         ~/.claude-sync/age-key.txt                │
│         ~/.claude-sync/state.json                 │
└──────────────────────────────────────────────────┘
```

---

## Package Breakdown

### `cmd/claude-sync/main.go`

The CLI entry point using [Cobra](https://github.com/spf13/cobra).

**Key Components:**
- Command definitions (init, push, pull, etc.)
- Interactive prompts for setup
- Progress reporting with spinners and counts
- Version management and self-update logic

**Design Decisions:**
- Single-file CLI keeps related command logic together
- Interactive mode by default, quiet mode for scripting (`-q` flag)
- Colors and spinners for better UX (disabled in non-TTY environments)

### `internal/sync/`

Orchestrates synchronization operations.

**sync.go - Syncer struct:**
```go
type Syncer struct {
    claudeDir   string     // ~/.claude
    storage     *storage.R2Client
    keyPath     string     // Path to age key
    state       *SyncState
    statePath   string     // ~/.claude-sync/state.json
}
```

**Key Methods:**
- `Push()` - Detect changes, encrypt, upload to R2
- `Pull()` - Fetch remote state, download, decrypt
- `Status()` - List pending local changes
- `Diff()` - Compare local vs remote

**state.go - State Management:**
```go
type FileState struct {
    Path     string    // Relative path (e.g., "projects/foo/session.json")
    Hash     string    // SHA256 hash of file contents
    Size     int64     // File size in bytes
    ModTime  time.Time // Local modification time
    Uploaded time.Time // When last pushed to R2
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

Handles all encryption operations.

**Key Functions:**
```go
// Generate random X25519 key pair
func GenerateKey() (string, error)

// Derive deterministic key from passphrase
func DeriveKeyFromPassphrase(passphrase string) (string, error)

// Encrypt file contents
func Encrypt(data []byte, keyPath string) ([]byte, error)

// Decrypt file contents
func Decrypt(data []byte, keyPath string) ([]byte, error)
```

**Encryption Flow:**
1. Read age identity from key file
2. Extract recipient (public key) from identity
3. Encrypt using age with recipient
4. Return encrypted bytes

### `internal/storage/`

S3-compatible client for Cloudflare R2.

**R2Client struct:**
```go
type R2Client struct {
    client *s3.Client
    bucket string
}
```

**Key Methods:**
```go
func (r *R2Client) Upload(key string, data []byte) error
func (r *R2Client) Download(key string) ([]byte, error)
func (r *R2Client) List(prefix string) ([]ObjectInfo, error)
func (r *R2Client) Delete(key string) error
func (r *R2Client) Exists(key string) (bool, error)
```

### `internal/config/`

Configuration management.

**Config struct:**
```go
type Config struct {
    R2 R2Config `yaml:"r2"`
}

type R2Config struct {
    AccountID  string `yaml:"account_id"`
    AccessKey  string `yaml:"access_key"`
    SecretKey  string `yaml:"secret_key"`
    BucketName string `yaml:"bucket_name"`
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
│  Change Detection │  Compare current files with SyncState
│  (hash, modtime)  │  Result: add/modify/delete lists
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Read & Encrypt   │  For each changed file:
│  (age encryption) │  Read content → Encrypt → Bytes
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Upload to R2     │  PUT object with .age extension
│  (S3 API)         │  Key: original/path.age
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
R2 Storage
        │
        ▼
┌───────────────────┐
│  List Objects     │  GET all objects in bucket
│  (S3 ListObjects) │  Compare with local state
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
│  Download &       │  GET object → Decrypt → Write
│  Decrypt          │  Remove .age extension
└────────┬──────────┘
         │
         ▼
┌───────────────────┐
│  Update State     │  Record new hash, pull time
│  (state.json)     │
└───────────────────┘
```

---

## File Structure

```
~/.claude-sync/
├── config.yaml      # R2 credentials (0600 permissions)
├── age-key.txt      # Encryption key (0600 permissions)
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

---

## Design Decisions

### Why age for Encryption?

- Modern, audited encryption (X25519 + ChaCha20-Poly1305)
- Simple CLI and library interface
- Deterministic key derivation support
- Small ciphertext overhead (~16 bytes header + 16 bytes tag)

### Why Argon2 for Key Derivation?

- Memory-hard: resistant to GPU/ASIC attacks
- Time-memory trade-off resistant
- Winner of Password Hashing Competition
- Parameters: 64MB memory, 3 iterations, 4 threads

### Why Cloudflare R2?

- S3-compatible API (reuse existing tooling)
- Generous free tier (10GB storage)
- No egress fees
- Global edge network

### Why Hash-Based Change Detection?

- More reliable than timestamps alone
- Detects content changes regardless of touch/copy operations
- SHA256 is fast enough for small files (typical < 1MB)

### Why State File Instead of Remote Metadata?

- Works offline (status command doesn't need network)
- Faster change detection
- Enables conflict detection by comparing local vs uploaded state

---

## Error Handling

The codebase uses Go's explicit error handling:

```go
// Errors bubble up with context
if err := syncer.Push(); err != nil {
    return fmt.Errorf("push failed: %w", err)
}
```

**Common Error Scenarios:**
- Network failures → Retry with backoff (not implemented, user retries)
- Invalid credentials → Clear error message, suggest `claude-sync reset`
- Encryption failures → Suggest passphrase mismatch
- File permission issues → Report specific file and required permissions

---

## Next

- [How It Works](./how-it-works) - Detailed sync workflow
- [Security](./security) - Encryption and threat model
