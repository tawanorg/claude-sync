<div align="center">

```
┌────────────────────────────────────────────────────┐
│  ✦ Welcome to Claude Sync                          │
└────────────────────────────────────────────────────┘

 ██████╗██╗      █████╗ ██╗   ██╗██████╗ ███████╗
██╔════╝██║     ██╔══██╗██║   ██║██╔══██╗██╔════╝
██║     ██║     ███████║██║   ██║██║  ██║█████╗
██║     ██║     ██╔══██║██║   ██║██║  ██║██╔══╝
╚██████╗███████╗██║  ██║╚██████╔╝██████╔╝███████╗
 ╚═════╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝

███████╗██╗   ██╗███╗   ██╗ ██████╗
██╔════╝╚██╗ ██╔╝████╗  ██║██╔════╝
███████╗ ╚████╔╝ ██╔██╗ ██║██║
╚════██║  ╚██╔╝  ██║╚██╗██║██║
███████║   ██║   ██║ ╚████║╚██████╗
╚══════╝   ╚═╝   ╚═╝  ╚═══╝ ╚═════╝
```

**Sync your Claude Code sessions across all your devices**

*Encrypted with [age](https://github.com/FiloSottile/age) • Stored on Cloudflare R2*

[![Release](https://img.shields.io/github/v/release/tawanorg/claude-sync)](https://github.com/tawanorg/claude-sync/releases)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Quick Start](#quick-start) • [Setup Guide](#setup-guide) • [Commands](#commands) • [Security](#security)

</div>

---

## Features

- **Cross-device sync**: Continue Claude Code conversations on any laptop
- **End-to-end encryption**: All files encrypted with age before upload
- **Passphrase-based keys**: Same passphrase = same key on any device (no file copying)
- **Self-updating**: `claude-sync update` to get the latest version
- **Minimal cost**: Uses Cloudflare R2 free tier (10GB included)
- **Simple CLI**: `push`, `pull`, `status`, `diff`, `conflicts` commands

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

# Set up with SAME R2 credentials and SAME passphrase
claude-sync init

# Pull sessions
claude-sync pull
```

**That's it!** Same passphrase = same encryption key. No file copying needed.

## Setup Guide

### Step 1: Create R2 Bucket

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/) → R2 Object Storage
2. Click "Create bucket" → name it anything (e.g., `claude-sync`)
3. Go to "Manage R2 API Tokens" → "Create API Token"
4. Select **Object Read & Write** permission → Create

You'll need:
- **Account ID** (in the dashboard URL: `dash.cloudflare.com/<ACCOUNT_ID>/r2`)
- **Access Key ID** (from the API token you just created)
- **Secret Access Key** (shown once when creating token)

### Step 2: Run Init

```bash
claude-sync init
```

The interactive setup will:

1. **Ask for R2 credentials** (Account ID, Access Key, Secret, Bucket name)
2. **Ask for encryption method**:
   - **[1] Passphrase** (recommended) - same passphrase on all devices = same key
   - **[2] Random key** - must copy `~/.claude-sync/age-key.txt` to other devices
3. **Test the connection** to verify everything works

### Step 3: Push and Pull

```bash
# Upload local changes
claude-sync push

# Download remote changes
claude-sync pull
```

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

## Commands

```bash
claude-sync init        # Set up configuration
claude-sync push        # Upload local changes to R2
claude-sync pull        # Download remote changes from R2
claude-sync status      # Show pending local changes
claude-sync diff        # Show differences between local and remote
claude-sync conflicts   # List and resolve conflicts
claude-sync reset       # Reset configuration (forgot passphrase)
claude-sync update      # Update to latest version
claude-sync --help      # Show all commands
```

### Quiet Mode

```bash
claude-sync push -q     # No output (for scripts)
claude-sync pull -q
```

### Check for Updates

```bash
claude-sync update --check   # Check without installing
claude-sync update           # Download and install latest version
```

## Shell Integration

Add to `~/.zshrc` or `~/.bashrc`:

```bash
# Auto-pull on shell start
if command -v claude-sync &> /dev/null; then
  claude-sync pull -q &
fi

# Auto-push on shell exit
trap 'claude-sync push -q' EXIT
```

## Conflict Resolution

When both local and remote files change, the remote version is saved as `.conflict`:

```bash
claude-sync conflicts            # Interactive resolution
claude-sync conflicts --list     # Just list conflicts
claude-sync conflicts --keep local   # Keep all local versions
claude-sync conflicts --keep remote  # Keep all remote versions
```

Interactive options:
- **[l]** Keep local (delete conflict file)
- **[r]** Keep remote (replace local)
- **[d]** Show diff
- **[s]** Skip
- **[q]** Quit

## Forgot Passphrase?

The passphrase is **never stored**. If you forget it:

1. Your encrypted R2 files cannot be recovered
2. Reset and start fresh:

```bash
claude-sync reset --remote   # Delete R2 files and local config
claude-sync init             # Set up again with new passphrase
claude-sync push             # Re-upload from this device
```

## Security

- Files encrypted with [age](https://github.com/FiloSottile/age) before upload
- Passphrase-derived keys use Argon2 (memory-hard KDF)
- Passphrase is never stored - only the derived key at `~/.claude-sync/age-key.txt`
- R2 bucket is private (API key auth)
- Config files stored with 0600 permissions

## Cost

Cloudflare R2 free tier:
- 10 GB storage
- 1M Class A ops/month
- 10M Class B ops/month

Claude sessions typically use < 50MB. Syncing is effectively **free**.

## Installation Options

### From Source (recommended)

```bash
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest
```

### Build Manually

```bash
git clone https://github.com/tawanorg/claude-sync
cd claude-sync
make build
./bin/claude-sync --version
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

## Development

```bash
make test          # Run tests
make fmt           # Format code
make check         # Run all pre-commit checks
make build-all     # Build for all platforms
make setup-hooks   # Enable git pre-commit hooks
```

## License

MIT
