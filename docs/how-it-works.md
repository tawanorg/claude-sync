---
layout: default
title: How It Works
---

# How It Works

[Home](./index) | [Architecture](./architecture) | [Security](./security)

---

## Initialization

When you run `claude-sync init`, an interactive wizard guides you through setup:

### Step 1: Select Storage Provider

```
┌─────────────────────────────────────────────────────────┐
│  ? Choose your cloud storage provider:                  │
│  > Cloudflare R2 (recommended - free tier: 10GB)        │
│    Amazon S3                                            │
│    Google Cloud Storage                                 │
└─────────────────────────────────────────────────────────┘
```

### Step 2: Provider-Specific Credentials

**Cloudflare R2:**
```
┌─────────────────────────────────────────┐
│  Cloudflare R2 Setup                    │
├─────────────────────────────────────────┤
│  ? Account ID: abc123def               │
│  ? Access Key ID: xxxxxxxxxx           │
│  ? Secret Access Key: **************** │
│  ? Bucket name: claude-sync             │
└─────────────────────────────────────────┘
```

**Amazon S3:**
```
┌─────────────────────────────────────────┐
│  Amazon S3 Setup                        │
├─────────────────────────────────────────┤
│  ? Access Key ID: AKIAXXXXXXX          │
│  ? Secret Access Key: **************** │
│  ? AWS Region: us-east-1               │
│  ? Bucket name: claude-sync             │
└─────────────────────────────────────────┘
```

**Google Cloud Storage:**
```
┌─────────────────────────────────────────┐
│  Google Cloud Storage Setup             │
├─────────────────────────────────────────┤
│  ? GCP Project ID: my-project-123      │
│  ? Authentication method:               │
│    > Application Default Credentials    │
│      Service Account JSON file          │
│  ? Bucket name: claude-sync             │
└─────────────────────────────────────────┘
```

### Step 3: Encryption Setup

```
┌─────────────────────────────────────────────────────────┐
│  ? Choose encryption key method:                        │
│  > Passphrase (recommended) - same key on all devices   │
│    Random key - must copy key file to other devices     │
└─────────────────────────────────────────────────────────┘
                    │
                    ▼ (if passphrase)
┌─────────────────────────────────────────────────────────┐
│  ? Passphrase (min 8 chars): ********                   │
│  ? Confirm passphrase: ********                         │
└─────────────────────────────────────────────────────────┘
```

### Step 4: Test Connection

```
[3/3] Test Connection
  ✓ Connected to 'claude-sync'

  Setup complete!

      Run 'claude-sync push' to upload your sessions
      Run 'claude-sync pull' on other devices to sync
```

---

## Key Derivation (Passphrase Mode)

When you choose passphrase-based encryption:

```
Passphrase: "my-secret-phrase"
                │
                ▼
┌────────────────────────────────────────────────────────┐
│  Salt Generation                                        │
│  salt = SHA256("claude-sync-v1")                       │
│  = fixed 32 bytes                                       │
│                                                         │
│  Why fixed salt?                                        │
│  - Same passphrase on different devices = same key     │
│  - No need to sync salt between devices                │
└────────────────────────────────────────────────────────┘
                │
                ▼
┌────────────────────────────────────────────────────────┐
│  Argon2id KDF                                           │
│                                                         │
│  Parameters:                                            │
│  - Memory: 64 MB                                        │
│  - Iterations: 3                                        │
│  - Parallelism: 4 threads                               │
│  - Output: 32 bytes                                     │
└────────────────────────────────────────────────────────┘
                │
                ▼
┌────────────────────────────────────────────────────────┐
│  Scalar Clamping (RFC 7748)                             │
│                                                         │
│  key[0] &= 248                                          │
│  key[31] &= 127                                         │
│  key[31] |= 64                                          │
│                                                         │
│  Required for X25519 security                           │
└────────────────────────────────────────────────────────┘
                │
                ▼
┌────────────────────────────────────────────────────────┐
│  Bech32 Encoding                                        │
│  Prefix: AGE-SECRET-KEY-1                               │
│  Output: age-compatible secret key string              │
│                                                         │
│  Saved to: ~/.claude-sync/age-key.txt                  │
└────────────────────────────────────────────────────────┘
```

---

## Push Workflow

When you run `claude-sync push`:

### Phase 1: Change Detection

```go
for each path in SyncPaths {
    if isDirectory(path) {
        walkDirectory(path)  // Recursively check files
    } else if isFile(path) {
        checkFile(path)      // Compare with state
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

### Phase 2: Upload

```
For each file to upload:
┌─────────────────────────────────────┐
│  Read local file                     │
│  path: ~/.claude/projects/foo/x.json │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Encrypt with age                    │
│  - Read identity from age-key.txt   │
│  - Extract recipient (public key)   │
│  - Encrypt content                  │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Upload via Storage interface        │
│  storage.Upload("path.age", data)   │
│                                      │
│  Provider-specific implementation:  │
│  - R2: S3 PutObject to Cloudflare   │
│  - S3: S3 PutObject to AWS          │
│  - GCS: Objects.Insert              │
└─────────────────────────────────────┘
```

### Phase 3: Progress Reporting

```
↑ [1/5] projects/abc123/session.json (4.2 KB)
↑ [2/5] settings.json (512 B)
↑ [3/5] CLAUDE.md (1.1 KB)
✗ [4/5] history.jsonl (deleted)
↑ [5/5] agents/custom.json (2.3 KB)

✓ Push complete: 4 uploaded, 1 deleted
```

### Phase 4: Update State

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
│  storage.List("")                    │
│                                      │
│  Returns all objects in bucket:     │
│  - Key (path.age)                   │
│  - Size                             │
│  - LastModified                     │
│  - ETag                             │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Build remote file map              │
│  Strip .age extension               │
│  "foo.json.age" → "foo.json"       │
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
│  storage.Download("path.age")       │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Decrypt content                     │
│  (age decryption)                   │
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
│  storage.Download("path.age")       │
│  Returns encrypted bytes            │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Decrypt with age                    │
│  - Read identity from age-key.txt   │
│  - Decrypt bytes → plaintext        │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Write to local file                 │
│  path: ~/.claude/{path}             │
│  Create parent directories           │
└─────────────────────────────────────┘
```

### Phase 5: Progress Reporting

```
↓ [1/3] projects/xyz789/session.json (8.5 KB)
↓ [2/3] skills/custom-skill.json (1.2 KB)
⚠ Conflict: settings.json (saved as .conflict)

✓ Pull complete: 2 downloaded, 1 conflicts

Conflicts (both local and remote changed):
  • settings.json

Local versions kept. Remote saved as .conflict files.
Run 'claude-sync conflicts' to review and resolve.
```

---

## Conflict Resolution

### Detection

A conflict occurs when:
1. Local file has changed since last sync (`localHash != stateHash`)
2. AND remote file has changed since last sync (`remoteModTime > stateUploaded`)

### Resolution Options

**Interactive mode (default):**
```bash
claude-sync conflicts

Found 2 conflict(s):

  1. settings.json
     Conflict from: 20260208-153045

  2. projects/abc123/session.json
     Conflict from: 20260208-154512

For each conflict, choose how to resolve:
  [l] Keep local  [r] Keep remote  [d] Show diff  [s] Skip  [q] Quit

[1/2] settings.json
        Local: 512 B  |  Remote: 498 B  |  Conflict from: 20260208-153045
        Resolve [l/r/d/s/q]: d

        --- Local
        +++ Remote (conflict)

        @@ -1,5 +1,5 @@
         {
        -  "theme": "dark",
        +  "theme": "light",
           "autoSave": true
         }

        Resolve [l/r/d/s/q]: l
        ✓ Kept local version

✓ Resolved 1 of 2 conflict(s)
```

**Batch mode:**
```bash
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
      "hash": "sha256:a1b2c3d4e5f6...",
      "size": 4096,
      "modTime": "2026-02-08T10:30:00Z",
      "uploaded": "2026-02-08T10:31:00Z"
    },
    "settings.json": {
      "path": "settings.json",
      "hash": "sha256:9f8e7d6c5b4a...",
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

1. **Efficient Change Detection**: Compare hashes without reading storage
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
│  Current: from build ldflags        │
│  Latest: from GitHub release         │
└─────────────────────────────────────┘
                │
                ▼ (if update available)
┌─────────────────────────────────────┐
│  Download binary                     │
│  Based on GOOS/GOARCH:               │
│  - darwin-arm64                      │
│  - darwin-amd64                      │
│  - linux-amd64                       │
│  - linux-arm64                       │
└─────────────────────────────────────┘
                │
                ▼
┌─────────────────────────────────────┐
│  Replace binary                      │
│  1. Write new binary as .new        │
│  2. Rename current → .old           │
│  3. Rename .new → current           │
│  4. Delete .old                     │
└─────────────────────────────────────┘
```

**Output:**
```
⋯ Checking for updates...
↑ New version available: v0.3.2 → v0.4.0
⋯ Downloading claude-sync-darwin-arm64...
⋯ Installing update...
✓ Updated to v0.4.0

Restart claude-sync to use the new version
```

---

## Shell Integration

For automatic syncing, add to your shell profile:

```bash
# ~/.zshrc or ~/.bashrc

# Auto-pull on shell start (background, quiet)
if command -v claude-sync &> /dev/null; then
  claude-sync pull -q &
fi

# Auto-push on shell exit (quiet)
trap 'claude-sync push -q' EXIT
```

This ensures:
- Fresh sessions are pulled when you start a terminal
- Local changes are pushed when you close a terminal

---

## Next

- [Architecture](./architecture) - System design and components
- [Security](./security) - Encryption and threat model
