# claude-sync

Sync your Claude Code sessions across devices using Cloudflare R2 with end-to-end encryption.

## Features

- **Cross-device sync**: Continue Claude Code conversations on any laptop
- **End-to-end encryption**: All files encrypted with age before upload
- **Passphrase-based keys**: Same passphrase = same key on any device (no file copying)
- **Minimal cost**: Uses Cloudflare R2 free tier (10GB included)
- **Simple CLI**: `push`, `pull`, `status`, `diff`, `conflicts` commands
- **Conflict resolution**: Interactive tool to review and resolve sync conflicts

## What gets synced

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

## Installation

### From source

```bash
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest
```

### Build manually

```bash
git clone https://github.com/tawanorg/claude-sync
cd claude-sync
make build
# Binary is at ./bin/claude-sync
```

## Setup

### 1. Create Cloudflare R2 bucket

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/)
2. Navigate to R2 Object Storage
3. Create a bucket named `claude-sync`
4. Go to "Manage R2 API Tokens" and create a token with read/write access

### 2. Initialize claude-sync

```bash
claude-sync init
```

You'll be prompted for:
- Cloudflare Account ID (found in R2 dashboard URL)
- R2 Access Key ID
- R2 Secret Access Key
- Bucket name (default: `claude-sync`)

This creates:
- `~/.claude-sync/config.yaml` - Your R2 credentials
- `~/.claude-sync/age-key.txt` - Encryption key (back this up!)
- `~/.claude-sync/state.json` - Sync state tracking

### 3. Push your sessions

```bash
claude-sync push
```

### 4. Set up second device

On your other laptop:

```bash
# Install
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest

# Initialize with same R2 credentials and passphrase
claude-sync init --passphrase

# Pull sessions
claude-sync pull
```

**That's it!** If you used passphrase mode on your first device, just enter the same passphrase. The encryption key is derived deterministically - no file copying needed.

<details>
<summary>Alternative: Using random key (more secure)</summary>

If you chose random key generation instead of passphrase:

```bash
# Install
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest

# Initialize with same R2 credentials
claude-sync init --skip-guide

# Copy your encryption key from first device
scp first-laptop:~/.claude-sync/age-key.txt ~/.claude-sync/

# Pull sessions
claude-sync pull
```

</details>

## Usage

```bash
# Upload local changes to R2
claude-sync push

# Download remote changes from R2
claude-sync pull

# Show pending local changes
claude-sync status

# Show differences between local and remote
claude-sync diff

# List and resolve conflicts
claude-sync conflicts

# Quiet mode (for scripts)
claude-sync push -q
claude-sync pull -q
```

## Shell integration

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
# Auto-pull Claude sessions on shell start
if command -v claude-sync &> /dev/null; then
  claude-sync pull --quiet &
fi

# Sync on shell exit
trap 'claude-sync push --quiet' EXIT
```

## Conflict resolution

When both local and remote files have changed, the remote version is saved as a `.conflict` file.

```bash
# List all conflicts
claude-sync conflicts --list

# Interactive resolution (review each conflict)
claude-sync conflicts

# Batch resolve - keep all local versions
claude-sync conflicts --keep local

# Batch resolve - keep all remote versions
claude-sync conflicts --keep remote
```

In interactive mode, you can:
- **[l]** Keep local version (delete conflict file)
- **[r]** Keep remote version (replace local with conflict)
- **[d]** Show diff between versions
- **[s]** Skip (decide later)
- **[q]** Quit

## Encryption & Passphrase

### How it works

When you run `claude-sync init`, you choose between:

| Mode | How it works | Pros | Cons |
|------|-------------|------|------|
| **Passphrase** | Key derived from passphrase using Argon2 | Same passphrase = same key on any device | Must remember passphrase |
| **Random key** | Generates random age key | Most secure | Must copy key file to other devices |

The derived/generated key is stored at `~/.claude-sync/age-key.txt`.

### Forgot your passphrase?

**The passphrase is never stored anywhere.** If you forget it:

1. Your encrypted files on R2 **cannot be recovered**
2. You must reset and start fresh:

```bash
# Reset everything (deletes R2 files too)
claude-sync reset --remote

# Set up again with new passphrase
claude-sync init --passphrase
```

### Reset command

Use `reset` when you need to start fresh:

```bash
# Clear local config only
claude-sync reset

# Also delete all files from R2
claude-sync reset --remote

# Also clear local sync state
claude-sync reset --local

# Full reset (nuclear option)
claude-sync reset --remote --local

# Skip confirmation
claude-sync reset --remote --force
```

## Security

- All files are encrypted with [age](https://github.com/FiloSottile/age) before upload
- Encryption key never leaves your devices
- Passphrase is **never stored** - only the derived key
- R2 bucket is private (API key authentication)
- Credentials stored with 0600 permissions

## Cost

Cloudflare R2 free tier includes:
- 10 GB storage
- 1 million Class A operations/month
- 10 million Class B operations/month

Claude sessions typically use < 50MB, so syncing is effectively **free**.

## Architecture

```
┌─────────────────┐         ┌─────────────────┐         ┌─────────────────┐
│   Laptop A      │         │   Cloudflare R2 │         │   Laptop B      │
│                 │         │                 │         │                 │
│  ~/.claude/     │◄───────►│  claude-sync    │◄───────►│  ~/.claude/     │
│                 │  push   │  bucket         │  pull   │                 │
│  claude-sync    │  pull   │  (encrypted)    │  push   │  claude-sync    │
└─────────────────┘         └─────────────────┘         └─────────────────┘
```

## Development

```bash
# Run tests
make test

# Format code
make fmt

# Build for all platforms
make build-all
```

## License

MIT
