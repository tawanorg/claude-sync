# Security Audit Report: claude-sync

**Date:** 2026-03-08
**Version:** 1.2.2
**Auditor:** WALL-E (AI Security Agent)
**Scope:** Full codebase review — source, dependencies, crypto, supply chain

---

## Executive Summary

claude-sync is a Go CLI tool (with npm wrapper) that syncs `~/.claude` directories across devices using cloud storage (R2/S3/GCS) with age encryption. **No critical or high vulnerabilities found.** Five medium findings relate to supply chain integrity and cryptographic design tradeoffs. Five low findings cover file permissions and path handling.

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 0 |
| Medium   | 5 |
| Low      | 5 |
| Info     | 5 |

---

## Medium Findings

### M1 — Credentials Stored in Plaintext Config

**Files:** `internal/config/config.go:24-26`, `cmd/claude-sync/main.go:394-401`

Cloud storage credentials (`access_key_id`, `secret_access_key`) are stored in plaintext in `~/.claude-sync/config.yaml`. The file is created with `0600` permissions and the directory with `0700`, which is the minimum acceptable protection. Any process running as the user can read these credentials.

**Remediation:** Support OS keychain integration (macOS Keychain, Linux libsecret) or environment variable-based credential resolution. At minimum, document this risk.

---

### M2 — Self-Update Without Integrity Verification

**File:** `cmd/claude-sync/main.go:1636-1676`

The `update` command downloads a binary from GitHub Releases and replaces the running executable without SHA256 checksum or cryptographic signature verification. HTTPS provides transport security, but does not protect against compromised release artifacts.

**Remediation:**
1. Publish SHA256 checksums alongside release binaries
2. Verify checksum after download before replacing binary
3. Optionally sign releases with GPG/Sigstore

---

### M3 — npm postinstall Downloads Unverified Binary

**File:** `install.js:79-147`

The postinstall script downloads a binary from GitHub, follows HTTP redirects, and writes it as executable (`0o755`). No checksum or signature verification is performed.

**Remediation:** Publish a `checksums.txt` in releases and verify the downloaded binary before making it executable.

---

### M4 — Fixed Salt in Passphrase Key Derivation

**File:** `internal/crypto/encrypt.go:96`

```go
salt := sha256.Sum256([]byte("claude-sync-v1"))
```

The salt is intentionally fixed so the same passphrase generates the same key across devices (documented design choice). This means all users share the same salt, making rainbow table attacks feasible against the entire user base. Argon2id parameters (64MB, 3 iterations) partially mitigate this.

**Remediation:** Derive salt from a user-specific value (e.g., bucket name or account ID) naturally available on all devices. This provides per-user salts without requiring manual exchange.

---

### M5 — Weak Passphrase Minimum Length

**File:** `internal/crypto/encrypt.go:146-155`

Minimum passphrase length is 8 characters. For a passphrase-derived key protecting all synced data — combined with the fixed salt (M4) — this is insufficient.

**Remediation:** Increase minimum to 12 characters, or implement entropy-based validation.

---

## Low Findings

### L1 — Path Traversal via Remote Object Keys

**File:** `internal/sync/sync.go:316-348`

The `downloadFile` function joins `claudeDir` with `relativePath` without explicit traversal validation. A malicious actor with bucket write access could craft keys containing `../` sequences.

**Mitigating factors:** Bucket requires authentication, `filepath.Join` normalizes `..` components, only `.age` keys are processed.

**Remediation:**
```go
if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(s.claudeDir)) {
    return fmt.Errorf("path traversal detected: %s", relativePath)
}
```

---

### L2 — Downloaded Files Use 0644 Permissions

**File:** `internal/sync/sync.go:337`

Downloaded files are written with `0644`, allowing other system users to read potentially sensitive Claude configuration.

**Remediation:** Use `0600` for all downloaded files.

---

### L3 — Backup Directory Uses 0755 Permissions

**File:** `cmd/claude-sync/main.go:1929`

Backup directories use `0755` and backup files use `0644`. These contain copies of sensitive configuration.

**Remediation:** Use `0700` for directories and `0600` for files.

---

### L4 — Symlink Detection Broken with filepath.Walk

**File:** `internal/sync/state.go:169, 183`

`fi.Mode()&os.ModeSymlink != 0` does not work with `filepath.Walk` because Walk follows symlinks and reports the target's FileInfo. The `ModeSymlink` bit is never set.

**Remediation:** Use `os.Lstat(path)` or switch to `filepath.WalkDir`.

---

### L5 — Go Version Mismatch Between go.mod and CI

**Files:** `go.mod:3` (Go 1.24), `.github/workflows/ci.yml:16` (Go 1.21), `.github/workflows/release.yml:22` (Go 1.21)

CI builds and tests against Go 1.21 while the module requires Go 1.24. Tests may miss version-specific issues.

**Remediation:** Align CI Go version with `go.mod`.

---

## Clean Areas

| Area | Status |
|------|--------|
| Hardcoded secrets | None found |
| Command injection | No risk — only `exec.Command("diff", ...)` with filesystem-derived paths |
| Data sovereignty | Clean — all endpoints are US/EU (GitHub, R2, S3, GCS) |
| eval/exec injection | No dynamic code execution |
| Encryption | Sound — age v1.3.1, X25519, Argon2id |
| Input validation | CLI inputs properly handled |

---

## Recommendations Priority

| Priority | Action | Effort |
|----------|--------|--------|
| 1 | Add checksum verification to update + install (M2, M3) | Medium |
| 2 | Derive per-user salt from bucket/account ID (M4) | Low |
| 3 | Increase minimum passphrase to 12 chars (M5) | Low |
| 4 | Add path traversal guard (L1) | Low |
| 5 | Fix file permissions to 0600/0700 (L2, L3) | Low |
| 6 | Fix symlink detection (L4) | Low |
| 7 | Align CI Go version (L5) | Low |
| 8 | Support OS keychain for credentials (M1) | High |
