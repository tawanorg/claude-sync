---
layout: default
title: How It Works
---

# How It Works

[Home](./index) | [Architecture](./architecture) | [Security](./security)

---

## Initialization

When you run `claude-sync init`, the following happens:

### Step 1: Gather R2 Credentials

```
┌─────────────────────────────────────────────┐
│  Interactive Prompts                        │
├─────────────────────────────────────────────┤
│  1. Cloudflare Account ID                   │
│  2. R2 Access Key ID                        │
│  3. R2 Secret Access Key                    │
│  4. Bucket Name                             │
└─────────────────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────┐
│  Validate Credentials                        │
│  - Attempt to list bucket contents          │
│  - Verify read/write permissions            │
└─────────────────────────────────────────────┘
```

### Step 2: Generate Encryption Key

**Option 1: Passphrase (Recommended)**
```
Passphrase Input
       │
       ▼
┌──────────────────────────────────────┐
│  Salt = SHA256("claude-sync-v1")     │  Fixed salt for reproducibility
└──────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────┐
│  Argon2id KDF                        │
│  - Memory: 64 MB                     │
│  - Iterations: 3                     │
│  - Threads: 4                        │
│  - Output: 32 bytes                  │
└──────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────┐
│  Scalar Clamping (RFC 7748)          │
│  Required for X25519 compatibility   │
└──────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────┐
│  Bech32 Encode                       │
│  Prefix: AGE-SECRET-KEY-             │
└──────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────┐
│  Write to ~/.claude-sync/age-key.txt │
│  Permissions: 0600                   │
└──────────────────────────────────────┘
```

**Option 2: Random Key**
```
crypto/rand.Read(32 bytes)
       │
       ▼
X25519 Scalar Clamping
       │
       ▼
Bech32 Encode → ~/.claude-sync/age-key.txt
```

### Step 3: Save Configuration

```yaml
# ~/.claude-sync/config.yaml
r2:
  account_id: "your-account-id"
  access_key: "your-access-key"
  secret_key: "your-secret-key"
  bucket_name: "claude-sync"
```

---

## Push Workflow

When you run `claude-sync push`:

### Phase 1: Change Detection

```go
for each path in SyncPaths {
    if isDirectory(path) {
        walkDirectory(path)
    } else if isFile(path) {
        checkFile(path)
    }
}
```

**For each file:**
```
┌─────────────────────────────────────┐
│  Read file content                   │
│  Calculate SHA256 hash               │
│  Get file size and modification time │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Compare with SyncState             │
│                                      │
│  if not in state → ADD              │
│  if hash changed → MODIFY           │
│  if in state but not on disk → DEL  │
└─────────────────────────────────────┘
```

**Result:**
```go
type ChangeSet struct {
    Add    []string  // New files
    Modify []string  // Changed files
    Delete []string  // Removed files
}
```

### Phase 2: Upload

For each file to add or modify:

```
┌─────────────────────────────────────┐
│  Read local file                     │
│  path: ~/.claude/projects/foo/x.json │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Encrypt with age                    │
│  - Read identity from age-key.txt   │
│  - Encrypt to recipient (public key)│
│  - Output: encrypted bytes          │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Upload to R2                        │
│  Key: projects/foo/x.json.age        │
│  Content-Type: application/octet    │
└─────────────────────────────────────┘
```

For each file to delete:
```
┌─────────────────────────────────────┐
│  Delete from R2                      │
│  Key: original/path.age             │
└─────────────────────────────────────┘
```

### Phase 3: Update State

```go
state.Files[path] = &FileState{
    Path:     path,
    Hash:     sha256Hash,
    Size:     fileSize,
    ModTime:  modificationTime,
    Uploaded: time.Now(),
}
state.LastPush = time.Now()
state.Save()  // Write to ~/.claude-sync/state.json
```

---

## Pull Workflow

When you run `claude-sync pull`:

### Phase 1: Fetch Remote State

```
┌─────────────────────────────────────┐
│  R2 ListObjects                      │
│  - Get all objects in bucket        │
│  - Returns: key, size, lastModified │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Build remote file list              │
│  Strip .age extension               │
│  e.g., "foo.json.age" → "foo.json"  │
└─────────────────────────────────────┘
```

### Phase 2: Determine Downloads

```
For each remote file:
┌─────────────────────────────────────────────────────────┐
│  localState = SyncState.Files[path]                      │
│  localFile = read ~/.claude/{path}                       │
│                                                          │
│  if localFile not exists:                                │
│      → DOWNLOAD (new file)                               │
│                                                          │
│  if localFile.hash == localState.hash:                  │
│      if remote.modTime > localState.uploaded:           │
│          → DOWNLOAD (remote is newer)                    │
│      else:                                               │
│          → SKIP (already synced)                         │
│                                                          │
│  if localFile.hash != localState.hash:                  │
│      if remote.modTime > localState.uploaded:           │
│          → CONFLICT (both changed)                       │
│      else:                                               │
│          → SKIP (local is newer, push will upload)       │
└─────────────────────────────────────────────────────────┘
```

### Phase 3: Handle Conflicts

When both local and remote have changed:

```
┌─────────────────────────────────────┐
│  Download remote file                │
│  Decrypt content                     │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Save as conflict file:              │
│  {path}.conflict.20260208-153045     │
│                                      │
│  Keep local file unchanged           │
└─────────────────────────────────────┘
```

### Phase 4: Download & Decrypt

For non-conflict files:

```
┌─────────────────────────────────────┐
│  Download from R2                    │
│  Key: path.age                       │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Decrypt with age                    │
│  - Read identity from age-key.txt   │
│  - Decrypt encrypted bytes          │
│  - Output: original content         │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Write to local file                 │
│  path: ~/.claude/{path}             │
│  Preserve directory structure        │
└─────────────────────────────────────┘
```

### Phase 5: Update State

```go
state.Files[path] = &FileState{
    Path:     path,
    Hash:     newHash,
    Size:     newSize,
    ModTime:  time.Now(),
    Uploaded: remoteModTime,
}
state.LastPull = time.Now()
state.Save()
```

---

## Conflict Resolution

### Detection

A conflict occurs when:
1. Local file has changed since last sync (`localHash != stateHash`)
2. AND remote file has changed since last sync (`remoteModTime > stateUploaded`)

### Resolution Options

```bash
# Interactive mode (default)
claude-sync conflicts

# Available actions:
# [l] Keep local version (delete conflict file)
# [r] Keep remote version (replace local with conflict file)
# [d] Show diff between versions
# [s] Skip this conflict
# [q] Quit resolution
```

```bash
# Batch mode
claude-sync conflicts --keep local   # Keep all local versions
claude-sync conflicts --keep remote  # Keep all remote versions
```

### Resolution Flow

**Keep Local:**
```
1. Delete {path}.conflict.{timestamp}
2. No changes to local file
3. Next push will upload local version
```

**Keep Remote:**
```
1. mv {path}.conflict.{timestamp} → {path}
2. Delete conflict file
3. Update state with new hash
```

---

## State Management

### State File Location

```
~/.claude-sync/state.json
```

### State Structure

```json
{
  "files": {
    "projects/abc123/session.json": {
      "path": "projects/abc123/session.json",
      "hash": "sha256:a1b2c3...",
      "size": 4096,
      "modTime": "2026-02-08T10:30:00Z",
      "uploaded": "2026-02-08T10:31:00Z"
    },
    "settings.json": {
      "path": "settings.json",
      "hash": "sha256:d4e5f6...",
      "size": 512,
      "modTime": "2026-02-07T15:00:00Z",
      "uploaded": "2026-02-07T15:01:00Z"
    }
  },
  "lastSync": "2026-02-08T10:31:00Z",
  "deviceId": "macbook-pro.local",
  "lastPush": "2026-02-08T10:31:00Z",
  "lastPull": "2026-02-08T09:00:00Z"
}
```

### Why Track State?

1. **Efficient Change Detection**: Compare hashes without reading R2
2. **Conflict Detection**: Know if local changed since last sync
3. **Offline Status**: `claude-sync status` works without network
4. **Device Tracking**: Identify which device last synced

---

## Self-Update Mechanism

When you run `claude-sync update`:

```
┌─────────────────────────────────────┐
│  Query GitHub API                    │
│  GET /repos/tawanorg/claude-sync/   │
│      releases/latest                 │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Compare versions                    │
│  Current: from git describe --tags   │
│  Latest: from GitHub release         │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Download binary                     │
│  Based on GOOS/GOARCH:               │
│  - darwin-arm64 (M1/M2/M3)          │
│  - darwin-amd64 (Intel Mac)          │
│  - linux-amd64                       │
│  - linux-arm64                       │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Replace binary                      │
│  1. Rename current → current.old    │
│  2. Write new binary                 │
│  3. chmod +x                         │
└─────────────────────────────────────┘
```

---

## Next

- [Architecture](./architecture) - System design and components
- [Security](./security) - Encryption and threat model
