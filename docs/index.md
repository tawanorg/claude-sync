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
| **Multi-cloud storage** | Cloudflare R2, Amazon S3, or Google Cloud Storage |
| **Interactive wizard** | Arrow-key driven setup with validation |
| **Conflict detection** | Automatic detection and resolution of concurrent edits |
| **Self-updating** | Built-in version management |

---

## Supported Storage Providers

| Provider | Free Tier | Best For |
|----------|-----------|----------|
| **Cloudflare R2** | 10GB storage, no egress fees | Personal use (recommended) |
| **Amazon S3** | 5GB (12 months) | AWS users, enterprise |
| **Google Cloud Storage** | 5GB | GCP users, enterprise |

---

## Quick Start

### First Device

```bash
# Install (pick one)
npm install -g @tawandotorg/claude-sync
# or: go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest

# Set up (interactive wizard)
claude-sync init

# Push your sessions
claude-sync push
```

### Second Device

```bash
# Install
npm install -g @tawandotorg/claude-sync

# Set up with SAME credentials and SAME passphrase
claude-sync init

# Pull sessions
claude-sync pull
```

---

## Installation Options

### npm (Recommended)

```bash
# One-time use
npx @tawandotorg/claude-sync init

# Global install
npm install -g @tawandotorg/claude-sync
```

### Go Install

```bash
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest
```

### Download Binary

Download from [GitHub Releases](https://github.com/tawanorg/claude-sync/releases):

```bash
# macOS ARM (M1/M2/M3)
curl -L https://github.com/tawanorg/claude-sync/releases/latest/download/claude-sync-darwin-arm64 -o claude-sync
chmod +x claude-sync
sudo mv claude-sync /usr/local/bin/

# macOS Intel
curl -L https://github.com/tawanorg/claude-sync/releases/latest/download/claude-sync-darwin-amd64 -o claude-sync

# Linux AMD64
curl -L https://github.com/tawanorg/claude-sync/releases/latest/download/claude-sync-linux-amd64 -o claude-sync

# Linux ARM64
curl -L https://github.com/tawanorg/claude-sync/releases/latest/download/claude-sync-linux-arm64 -o claude-sync
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
| `~/.claude/settings.local.json` | Local settings |
| `~/.claude/CLAUDE.md` | Global instructions |

---

## Commands Reference

| Command | Description |
|---------|-------------|
| `claude-sync init` | Interactive setup (provider selection + encryption) |
| `claude-sync push` | Upload local changes to cloud storage |
| `claude-sync pull` | Download remote changes from cloud storage |
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
claude-sync reset --remote   # Also delete cloud storage data
claude-sync reset --local    # Also clear local sync state
```

---

## Provider Setup Guides

### Cloudflare R2 (Recommended)

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/) → R2 Object Storage
2. Create bucket → name it `claude-sync`
3. Manage R2 API Tokens → Create API Token
4. Select **Object Read & Write** permission

**You'll need:**
- Account ID (in dashboard URL)
- Access Key ID
- Secret Access Key
- Bucket name

### Amazon S3

1. Go to [AWS S3 Console](https://s3.console.aws.amazon.com/)
2. Create bucket → name it `claude-sync`
3. IAM → Create access key for CLI access

**You'll need:**
- Access Key ID
- Secret Access Key
- AWS Region
- Bucket name

### Google Cloud Storage

1. Go to [GCS Console](https://console.cloud.google.com/storage)
2. Create bucket → name it `claude-sync`
3. IAM → Create service account with Storage Object Admin role
4. Download JSON key file (or use `gcloud auth application-default login`)

**You'll need:**
- Project ID
- Credentials file (or use Application Default Credentials)
- Bucket name

---

## Cost

Claude sessions typically use < 50MB. Syncing is effectively **free** on any provider:

| Provider | Free Tier |
|----------|-----------|
| **Cloudflare R2** | 10GB storage, 1M writes, 10M reads/month |
| **AWS S3** | 5GB for 12 months (then ~$0.023/GB) |
| **Google Cloud Storage** | 5GB, 5K writes, 50K reads/month |

---

## Technology Stack

| Component | Technology | Purpose |
|-----------|------------|---------|
| Language | Go 1.21+ | Cross-platform CLI |
| Encryption | [age](https://github.com/FiloSottile/age) | Modern file encryption (X25519 + ChaCha20-Poly1305) |
| KDF | Argon2id | Memory-hard passphrase derivation |
| Storage | R2 / S3 / GCS | S3-compatible object storage |
| CLI Framework | [Cobra](https://github.com/spf13/cobra) | Command parsing |
| Interactive UI | [Survey](https://github.com/AlecAivazis/survey) | Interactive prompts |
| Config | YAML | Human-readable configuration |
| Distribution | npm / Go / Binary | Multiple installation methods |

---

## Learn More

- [Architecture](./architecture) - System design and component overview
- [How It Works](./how-it-works) - Detailed sync workflow and state management
- [Security](./security) - Encryption, key derivation, and threat model

---

## License

MIT License - see [LICENSE](https://github.com/tawanorg/claude-sync/blob/main/LICENSE)
