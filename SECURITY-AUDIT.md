# Security Audit Report: claude-sync-enc

**Date:** 2026-04-02
**Version:** 1.8.0
**Previous Audit:** 2026-03-08 (v1.2.2)
**Scope:** Full codebase — cryptography, input validation, cloud auth, network, dependencies, supply chain
**Method:** Three parallel security auditors (crypto/secrets, input/injection, cloud/network)

---

## Executive Summary

claude-sync-enc is a Go CLI tool (with npm wrapper) that syncs `~/.claude` directories across devices using cloud storage (R2/S3/GCS) with age encryption. This audit supersedes the 2026-03-08 report and **escalates several previously-rated findings** based on deeper analysis of attack chains.

Key changes from the previous audit:
- **Path traversal** (previously L1) escalated to **HIGH** — `filepath.Join` does resolve `..` components, enabling arbitrary file writes on bucket compromise
- **Self-update without verification** (previously M2/M3) escalated to **CRITICAL** — enables full binary replacement with no integrity check
- **New findings:** Decompression bomb (HIGH), symlink bypass via `os.Stat` (HIGH), unbounded downloads (HIGH), missing context timeouts (MEDIUM), race condition in state updates (MEDIUM)

| Severity | Count | Previous |
|----------|-------|----------|
| Critical | 1     | 0        |
| High     | 4     | 0        |
| Medium   | 5     | 5        |
| Low      | 4     | 5        |

**None of the findings from the previous audit have been remediated.**

---

## CRITICAL Findings

### C1 — Self-Update and npm Install Without Integrity Verification

**Files:**
- `cmd/claude-sync/main.go` ~line 1679 (`downloadBinary`)
- `install.js` lines 79-110

**Previously:** M2/M3. **Escalated** because the attack surface has grown with user adoption and the binary has write access to `~/.claude` (settings, agents, command history).

The `update` command and npm `postinstall` script both download binaries from GitHub releases and execute them without any checksum or signature verification. The npm installer manually follows HTTP 301/302 redirects to arbitrary `Location` header values without domain pinning.

Attack vectors: compromised GitHub release, CDN redirect manipulation, DNS hijack. Impact: arbitrary code execution as the user, full access to all Claude configurations and session data.

**Remediation:**
1. Publish `SHA256SUMS` file alongside each release binary
2. Verify downloaded binary hash before writing to disk
3. Pin redirect targets to `github.com` / `objects.githubusercontent.com`
4. Apply `io.LimitReader` to cap download size
5. Consider Sigstore/cosign signing for release artifacts

---

## HIGH Findings

### H1 — Path Traversal via Remote Object Keys

**File:** `internal/sync/sync.go` ~line 428

**Previously:** L1. **Escalated** because `filepath.Join` *does* resolve `..` components — `filepath.Join("/home/user/.claude", "../../.ssh/authorized_keys")` resolves to `/home/user/.ssh/authorized_keys`.

```go
fullPath := filepath.Join(s.claudeDir, relativePath)
os.MkdirAll(dir, 0755)
os.WriteFile(fullPath, data, 0644)
```

`relativePath` is derived from remote object keys by stripping `.age` suffix only. No containment check. A malicious actor with bucket write access can write arbitrary files anywhere the process has write permission.

The same issue affects `handleConflict` (~line 452) where conflict paths inherit the unsanitized `relativePath`.

**Remediation:**
```go
fullPath := filepath.Join(s.claudeDir, relativePath)
cleanBase := filepath.Clean(s.claudeDir) + string(os.PathSeparator)
if !strings.HasPrefix(filepath.Clean(fullPath)+string(os.PathSeparator), cleanBase) {
    return fmt.Errorf("path traversal rejected: %s escapes sync directory", relativePath)
}
```

Also apply to `uploadFile` and the `exec.Command("diff", ...)` paths in `main.go` ~line 1409 for defense in depth.

---

### H2 — Symlink Bypass via `os.Stat` Instead of `os.Lstat`

**File:** `internal/sync/state.go` ~line 201

**Previously:** L4 (noted for `filepath.Walk`). **Escalated** with new finding: the top-level sync path uses `os.Stat`, which **follows symlinks** and never sets `ModeSymlink`.

```go
info, err := os.Stat(fullPath)    // follows symlinks — ModeSymlink never set
if info.Mode()&os.ModeSymlink != 0 {  // always false
    continue
}
```

A symlink at `~/.claude/CLAUDE.md` -> `/etc/passwd` would silently pass this check and be uploaded, leaking the target file's contents to the cloud bucket.

Note: Inside `filepath.Walk`, Go uses `os.Lstat` internally, so the symlink check at ~line 227 *does* work for walked directory entries. Only the top-level branch is broken.

**Remediation:** Replace `os.Stat` with `os.Lstat` at line 201.

---

### H3 — Decompression Bomb (Unbounded gzip Decompress)

**File:** `internal/sync/sync.go` ~line 937

```go
func gzipDecompress(data []byte) ([]byte, error) {
    r, err := gzip.NewReader(bytes.NewReader(data))
    return io.ReadAll(r)   // no size limit
}
```

A malicious remote object (encrypted with the correct key) could contain a gzip bomb — a small payload that decompresses to gigabytes, exhausting memory. The same unbounded `io.ReadAll` pattern exists in all three storage provider `Download` methods (r2.go:82, s3.go:82, gcs.go:83) and in `downloadBinary` (main.go:1691).

**Remediation:**
```go
const maxDecompressedSize = 100 * 1024 * 1024 // 100 MB

func gzipDecompress(data []byte) ([]byte, error) {
    r, err := gzip.NewReader(bytes.NewReader(data))
    if err != nil { return nil, err }
    defer r.Close()
    limited := io.LimitReader(r, maxDecompressedSize+1)
    out, err := io.ReadAll(limited)
    if err != nil { return nil, err }
    if int64(len(out)) > maxDecompressedSize {
        return nil, fmt.Errorf("decompressed data exceeds maximum allowed size")
    }
    return out, nil
}
```

Also wrap all storage provider `Download` response bodies with `io.LimitReader`.

---

### H4 — Compress-Then-Encrypt Ordering Leaks Information

**File:** `internal/sync/sync.go` lines 378-389, 735-748

```go
compressed, err := gzipCompress(data)    // compress first
encrypted, err := s.encryptor.Encrypt(compressed)  // then encrypt
```

Compressing before encrypting leaks plaintext information through ciphertext size (CRIME/BREACH class). For structured, predictable content like Claude config files, an observer of ciphertext sizes in cloud storage can infer content patterns.

**Mitigating factor:** Attacker cannot inject chosen plaintext (unlike classic CRIME/BREACH against TLS), reducing exploitability.

**Remediation:** Reverse the order — encrypt first, then optionally compress. Note: encrypted data (pseudorandom) compresses poorly, so this trades bandwidth for confidentiality. For a security tool, confidentiality should take priority.

---

## MEDIUM Findings

### M1 — Fixed Global Salt in Passphrase Key Derivation

**File:** `internal/crypto/encrypt.go` lines 96-100

```go
salt := sha256.Sum256([]byte("claude-sync-v1"))
key := argon2.IDKey([]byte(passphrase), salt[:], 3, 64*1024, 4, 32)
```

Every user shares the same salt. Two users with the same passphrase derive identical keys. An attacker can precompute a single rainbow table targeting all users. Argon2id parameters (64 MB, 3 iterations) provide moderate resistance but cannot compensate for the missing per-user salt.

**Remediation:** Derive salt from user-specific values available on all devices:
```go
saltInput := fmt.Sprintf("claude-sync-v1:%s:%s", cfg.AccountID, cfg.Bucket)
salt := sha256.Sum256([]byte(saltInput))
```
This is a backward-incompatible change requiring a key migration path.

---

### M2 — Weak Passphrase Minimum and Silent Non-Warning

**File:** `internal/crypto/encrypt.go` lines 146-155

```go
func ValidatePassphraseStrength(passphrase string) error {
    if len(passphrase) < 8 {
        return fmt.Errorf("passphrase must be at least 8 characters")
    }
    if len(passphrase) < 12 {
        // Warn but allow — but no warning is actually emitted
        return nil
    }
    return nil
}
```

The 8-11 character range returns `nil` with no warning surfaced to the caller. Combined with M1 (fixed salt), 8-character passphrases are especially dangerous.

**Remediation:**
1. Raise hard minimum to 12 characters
2. Return a distinct warning type for 12-16 character range
3. Consider rejecting common-password-list matches

---

### M3 — Plaintext Cloud Credentials in Config File

**Files:** `internal/config/config.go` lines 27-52, `internal/storage/config.go` lines 13-26

`SecretAccessKey` and `CredentialsJSON` (full GCS service account JSON with RSA private key) are serialized to `~/.claude-sync/config.yaml` in plaintext. File permissions are `0600` (correct), but credentials are exposed to backup tools, bug reports, disk forensics, and any same-user process.

**Remediation:**
- Use OS keychain APIs (`go-keyring`) for secret storage
- For GCS, require a file path reference (`credentials_file`) instead of inline JSON
- At minimum, document this exposure prominently

---

### M4 — No Context Timeouts on Cloud Storage Operations

**Files:** `internal/storage/r2/r2.go` ~line 35, `s3/s3.go` ~line 30, `gcs/gcs.go` ~line 29

All storage constructors pass `context.Background()` with no deadline. GCS performs network calls during `storage.NewClient`. A hung connection blocks the process indefinitely.

**Remediation:** Use `context.WithTimeout(ctx, 30*time.Second)` for client construction and per-operation contexts with appropriate deadlines.

---

### M5 — Race Condition in Concurrent State Updates

**File:** `internal/sync/sync.go` lines 395-402

```go
s.state.UpdateFile(relativePath, info, hash)   // mutex-protected call 1
s.state.MarkUploaded(relativePath)              // mutex-protected call 2
```

Two separate mutex-guarded operations without holding the lock across both. Another goroutine can observe a torn state between the calls.

**Remediation:** Add an `UpdateFileAndMarkUploaded` method that performs both mutations under a single lock.

---

## LOW Findings

### L1 — Downloaded Files Written with 0644, Directories with 0755

**Files:** `internal/sync/sync.go` ~line 430 (dirs), ~line 435 (files)

Synced files containing sensitive Claude config are world-readable. Directories are world-traversable.

**Remediation:** Use `0700` for directories, `0600` for files.

---

### L2 — Key Material Not Zeroed from Memory

**File:** `internal/crypto/encrypt.go` lines 93-119

The derived key (`[]byte`), `privateKey` (`[32]byte`), and passphrase (`string`) are never zeroed after use. Key material persists in memory and may appear in crash dumps or core files.

**Remediation:**
```go
defer func() {
    for i := range key { key[i] = 0 }
    for i := range privateKey { privateKey[i] = 0 }
}()
```
Accept `[]byte` instead of `string` for passphrase to allow caller-side zeroing.

---

### L3 — Integration Test Hardcoded Fallback Passphrase

**File:** `integration/r2_sync_test.go` lines 460-465

```go
func getTestPassphrase() string {
    if p := os.Getenv("CLAUDE_SYNC_TEST_PASSPHRASE"); p != "" { return p }
    return "test-passphrase-123"
}
```

If integration tests run against a real bucket without the env var set, data is encrypted with a publicly known passphrase.

**Remediation:** `t.Skip("CLAUDE_SYNC_TEST_PASSPHRASE not set")` instead of falling back.

---

### L4 — `BucketExists` Silently Swallows Errors

**Files:** `internal/storage/r2/r2.go` ~line 195, `s3/s3.go` ~line 195, `gcs/gcs.go` ~line 169

All return `(false, nil)` on any error, hiding credential errors and network failures as "bucket not found."

**Remediation:** Return the actual error, or distinguish 404 from other error classes.

---

## Verified Clean

| Area | Status |
|------|--------|
| Hardcoded production secrets | None found |
| Command injection | `exec.Command("diff", ...)` uses literal args, no shell invocation |
| SQL injection | N/A (no database) |
| age library usage | Correct — `age.Encrypt`/`age.Decrypt` with `X25519Identity`; v1.3.1 current |
| Argon2id algorithm choice | Correct; parameters acceptable |
| X25519 scalar clamping | Present and correct (RFC 7748) |
| Config file permissions | `0600` files, `0700` directory |
| Age key file permissions | `0600` |
| TLS configuration | Default SDK TLS with full cert validation; no custom overrides |
| Credential logging | No credentials/passphrases/keys logged anywhere |

---

## Remediation Priority

| # | Finding | Severity | Effort | Impact |
|---|---------|----------|--------|--------|
| 1 | C1 — Checksum verification on update/install | Critical | Medium | Prevents binary supply chain attack |
| 2 | H1 — Path traversal containment check | High | Low (5 lines) | Prevents arbitrary file write on bucket compromise |
| 3 | H2 — `os.Lstat` for symlink detection | High | Trivial (1 line) | Prevents sensitive file exfiltration |
| 4 | H3 — Bounded decompression + download sizes | High | Low | Prevents DoS via decompression/download bomb |
| 5 | H4 — Reverse compress/encrypt ordering | High | Medium | Eliminates information leakage class |
| 6 | M1 — Per-user salt derivation | Medium | Low (breaking) | Defeats cross-user rainbow tables |
| 7 | M2 — Raise passphrase minimum to 12 | Medium | Low | Reduces brute-force surface |
| 8 | L1 — File permissions 0600/0700 | Low | Trivial | Prevents local data exposure |
| 9 | L2 — Zero key material after use | Low | Low | Reduces forensic exposure |
| 10 | M4 — Context timeouts on cloud ops | Medium | Low | Prevents indefinite hangs |
| 11 | M5 — Atomic state update | Medium | Low | Fixes race condition |
| 12 | M3 — OS keychain for credentials | Medium | High | Protects credentials at rest |
| 13 | L3 — Require env var for test passphrase | Low | Trivial | Prevents accidental weak-key usage |
| 14 | L4 — Return errors from BucketExists | Low | Low | Surfaces misconfigurations |

---

## Key File Locations

| File | Findings |
|------|----------|
| `internal/crypto/encrypt.go` | M1 (salt), M2 (passphrase), L2 (key zeroing) |
| `internal/sync/sync.go` | H1 (path traversal), H3 (decompression), H4 (compress order), M5 (race), L1 (permissions) |
| `internal/sync/state.go` | H2 (symlink bypass) |
| `cmd/claude-sync/main.go` | C1 (self-update), H1 (diff paths) |
| `install.js` | C1 (npm install) |
| `internal/config/config.go` | M3 (plaintext creds) |
| `internal/storage/config.go` | M3 (GCS credentials inline) |
| `internal/storage/*/` | H3 (unbounded downloads), M4 (no timeouts), L4 (error swallowing) |
| `integration/r2_sync_test.go` | L3 (hardcoded passphrase) |
