---
layout: default
title: Claude Sync - Technical Documentation
---

# Claude Sync

**Encrypted cross-device synchronization for Claude Code sessions**

[Architecture](./architecture) | [How It Works](./how-it-works) | [Security](./security) | [GitHub](https://github.com/tawanorg/claude-sync)

---

## What is Claude Sync?

Claude Sync is a CLI tool that enables seamless synchronization of [Claude Code](https://claude.ai/claude-code) conversations, project sessions, and configurations across multiple devices.

### The Problem

Claude Code maintains local state in the `~/.claude` directory:
- Session files and conversation history
- Project-specific memory and context
- Custom agents, skills, and plugins
- User settings and preferences

When you switch devices, this context is lost. Traditional file sync services (Dropbox, iCloud) expose sensitive data unencrypted.

### The Solution

Claude Sync provides:

| Feature | Description |
|---------|-------------|
| **End-to-end encryption** | Files encrypted with [age](https://github.com/FiloSottile/age) before upload |
| **Passphrase-based keys** | Same passphrase = same key on any device (no file copying) |
| **Cloudflare R2 storage** | S3-compatible storage with 10GB free tier |
| **Conflict detection** | Automatic detection and resolution of concurrent edits |
| **Self-updating** | Built-in version management |

---

## Quick Start

### First Device

```bash
# Install
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest

# Set up (interactive)
claude-sync init

# Push your sessions
claude-sync push
```

### Second Device

```bash
# Install
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest

# Set up with SAME passphrase
claude-sync init

# Pull sessions
claude-sync pull
```

---

## What Gets Synced

| Path | Content |
|------|---------|
| `~/.claude/projects/` | Session files, auto-memory |
| `~/.claude/history.jsonl` | Command history |
| `~/.claude/agents/` | Custom agents |
| `~/.claude/skills/` | Custom skills |
| `~/.claude/plugins/` | Plugins |
| `~/.claude/rules/` | Custom rules |
| `~/.claude/settings.json` | Settings |
| `~/.claude/CLAUDE.md` | Global instructions |

---

## Commands Reference

| Command | Description |
|---------|-------------|
| `claude-sync init` | Interactive setup (R2 credentials + encryption) |
| `claude-sync push` | Upload local changes to R2 |
| `claude-sync pull` | Download remote changes from R2 |
| `claude-sync status` | Show pending local changes |
| `claude-sync diff` | Compare local and remote state |
| `claude-sync conflicts` | List and resolve conflicts |
| `claude-sync reset` | Reset configuration |
| `claude-sync update` | Update to latest version |

### Flags

```bash
claude-sync push -q          # Quiet mode (for scripts)
claude-sync pull -q          # Quiet mode
claude-sync update --check   # Check for updates without installing
claude-sync conflicts --list # List conflicts without resolving
claude-sync conflicts --keep local|remote  # Auto-resolve conflicts
claude-sync reset --remote   # Also delete R2 data
```

---

## Technology Stack

| Component | Technology | Purpose |
|-----------|------------|---------|
| Language | Go 1.21+ | Cross-platform CLI |
| Encryption | [age](https://github.com/FiloSottile/age) | Modern file encryption (X25519 + ChaCha20-Poly1305) |
| KDF | Argon2id | Memory-hard passphrase derivation |
| Storage | Cloudflare R2 | S3-compatible object storage |
| CLI Framework | [Cobra](https://github.com/spf13/cobra) | Command parsing |
| Config | YAML | Human-readable configuration |

---

## Learn More

- [Architecture](./architecture) - System design and component overview
- [How It Works](./how-it-works) - Detailed sync workflow and state management
- [Security](./security) - Encryption, key derivation, and threat model

---

## License

MIT License - see [LICENSE](https://github.com/tawanorg/claude-sync/blob/main/LICENSE)
