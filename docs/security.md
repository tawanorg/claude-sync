---
layout: default
title: Security
---

# Security

[Home](./index) | [Architecture](./architecture) | [How It Works](./how-it-works)

---

## Security Goals

Claude Sync is designed with the following security objectives:

| Goal | Implementation |
|------|----------------|
| **Confidentiality** | End-to-end encryption with age (X25519 + ChaCha20-Poly1305) |
| **Integrity** | AEAD encryption (Poly1305 MAC) detects tampering |
| **Key Portability** | Deterministic key derivation from passphrase |
| **Minimal Trust** | R2 storage sees only encrypted blobs |

---

## Encryption

### Algorithm: age

Claude Sync uses [age](https://github.com/FiloSottile/age), a modern file encryption tool designed by Filippo Valsorda (Go security lead at Google).

**Encryption Scheme:**
```
┌────────────────────────────────────────────────────────┐
│  age Encryption                                         │
├────────────────────────────────────────────────────────┤
│  1. Generate ephemeral X25519 keypair                   │
│  2. ECDH with recipient's public key → shared secret    │
│  3. HKDF-SHA256 → file key                             │
│  4. ChaCha20-Poly1305 AEAD → encrypted content         │
└────────────────────────────────────────────────────────┘
```

**Properties:**
- **Forward Secrecy**: Ephemeral keys per file encryption
- **Authenticated Encryption**: ChaCha20-Poly1305 AEAD
- **Small Overhead**: ~16 bytes header + 16 bytes auth tag
- **Streaming Support**: Large files don't need to fit in memory

### Key Format

age uses X25519 keys encoded in Bech32:

```
Secret Key: AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ
Public Key: age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq
```

---

## Key Derivation

### Passphrase Mode (Recommended)

When you choose passphrase-based encryption, the key is derived deterministically:

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
│  - Trade-off: rainbow tables possible for common       │
│    passphrases (mitigated by Argon2 cost)              │
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
│                                                         │
│  Why Argon2id?                                          │
│  - Memory-hard: expensive for GPU/ASIC attacks         │
│  - Hybrid mode: resistant to side-channels AND         │
│    time-memory tradeoff attacks                        │
│  - Winner of Password Hashing Competition (2015)       │
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
│  Why clamp?                                             │
│  - Required for X25519 security                        │
│  - Ensures key is valid curve25519 scalar              │
│  - Prevents small-subgroup attacks                     │
└────────────────────────────────────────────────────────┘
                │
                ▼
┌────────────────────────────────────────────────────────┐
│  Bech32 Encoding                                        │
│  Prefix: AGE-SECRET-KEY-1                               │
│  Output: age-compatible secret key string              │
└────────────────────────────────────────────────────────┘
```

### Random Key Mode

For users who prefer random keys:

```go
// Generate 32 random bytes
key := make([]byte, 32)
crypto.Read(key)

// Clamp for X25519
key[0] &= 248
key[31] &= 127
key[31] |= 64

// Encode as age key
bech32.Encode("AGE-SECRET-KEY-", key)
```

**Trade-offs:**
| Mode | Pros | Cons |
|------|------|------|
| Passphrase | Same key on all devices, no file copying | Must remember passphrase |
| Random | Cryptographically stronger | Must copy key file between devices |

---

## File Permissions

All sensitive files are created with restrictive permissions:

| File | Permissions | Contains |
|------|-------------|----------|
| `~/.claude-sync/config.yaml` | `0600` | R2 credentials |
| `~/.claude-sync/age-key.txt` | `0600` | Encryption key |
| `~/.claude-sync/state.json` | `0644` | File hashes (not sensitive) |

**Permission Enforcement:**
```go
os.WriteFile(path, data, 0600)  // Owner read-write only
```

---

## Threat Model

### What Claude Sync Protects Against

| Threat | Mitigation |
|--------|------------|
| **R2 breach** | Files are encrypted; attacker sees only ciphertext |
| **Network interception** | HTTPS to R2; content is pre-encrypted |
| **Cloudflare access** | Same as R2 breach; no plaintext access |
| **Lost device** | Key file is encrypted or derived from passphrase |
| **Weak passphrase** | Argon2 makes brute-force expensive |

### What Claude Sync Does NOT Protect Against

| Threat | Why Not |
|--------|---------|
| **Local malware** | If attacker has local access, they can read `~/.claude` |
| **Compromised passphrase** | All devices become vulnerable |
| **Targeted attack on your device** | Out of scope for sync tool |
| **R2 credential theft** | Attacker can delete your encrypted files (but not read them) |

### Trust Boundaries

```
┌─────────────────────────────────────────────────────────┐
│  TRUSTED                                                 │
│  - Your local machine                                   │
│  - Your passphrase / key file                           │
│  - Claude Sync binary (verify with checksums)           │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│  UNTRUSTED (but used)                                   │
│  - Cloudflare R2 storage                                │
│  - Network between you and R2                           │
│  - GitHub releases (verify signatures if paranoid)      │
└─────────────────────────────────────────────────────────┘
```

---

## Passphrase Recommendations

### Strong Passphrase Guidelines

1. **Length**: Minimum 16 characters (32+ recommended)
2. **Randomness**: Use a password manager to generate
3. **Uniqueness**: Don't reuse from other services

### Example Strong Passphrases

```
# Generated with: openssl rand -base64 24
cP9xK2mQ8jL5nR7vY1wZ4aB6dF3hG0sT

# Diceware (6 words minimum)
correct-horse-battery-staple-xkcd-2024
```

### Passphrase Storage

Since the passphrase is never stored by Claude Sync:

1. **Password Manager**: Store in 1Password, Bitwarden, etc.
2. **Memory**: For frequently-used passphrases
3. **Backup**: Secure offline storage for critical passphrases

---

## Key Recovery

### Forgot Passphrase?

**Bad news**: Encrypted files cannot be recovered without the correct passphrase.

**Recovery steps:**
```bash
# 1. Reset local configuration
claude-sync reset

# 2. Optionally delete unrecoverable R2 data
claude-sync reset --remote

# 3. Set up again with new passphrase
claude-sync init

# 4. Re-upload from current device
claude-sync push
```

### Lost Key File (Random Mode)?

Same as forgot passphrase—encrypted R2 files are unrecoverable.

**Prevention:**
1. Back up `~/.claude-sync/age-key.txt` securely
2. Or use passphrase mode instead

---

## Cryptographic Details

### Libraries Used

| Library | Version | Purpose |
|---------|---------|---------|
| `filippo.io/age` | v1.3.1 | File encryption |
| `golang.org/x/crypto/argon2` | v0.45.0 | Key derivation |
| `btcsuite/btcd/btcutil/bech32` | v1.1.6 | Key encoding |

### Verification

You can verify the encryption yourself:

```bash
# Encrypt a test file
age -r $(age-keygen -y ~/.claude-sync/age-key.txt) -o test.age test.txt

# Decrypt
age -d -i ~/.claude-sync/age-key.txt test.age > test-decrypted.txt

# Verify
diff test.txt test-decrypted.txt
```

---

## Security Checklist

Before using Claude Sync:

- [ ] Created R2 bucket with API token (not root credentials)
- [ ] Used strong passphrase (16+ characters, random)
- [ ] Verified config files have `0600` permissions
- [ ] Stored passphrase in password manager
- [ ] Tested push/pull on a non-critical device first

Ongoing:

- [ ] Don't share passphrase or key file
- [ ] Use `claude-sync update` to get security fixes
- [ ] Periodically review synced content for sensitive data
- [ ] Rotate R2 API keys if compromised

---

## Reporting Security Issues

If you discover a security vulnerability:

1. **Do not** open a public GitHub issue
2. Email the maintainer directly (see GitHub profile)
3. Include reproduction steps and impact assessment

---

## Next

- [Home](./index) - Overview and quick start
- [Architecture](./architecture) - System design
- [How It Works](./how-it-works) - Detailed workflows
