# claude-sync

Sync your Claude Code sessions across devices using Cloudflare R2 with end-to-end encryption.

## Features

- **Cross-device sync**: Continue Claude Code conversations on any laptop
- **End-to-end encryption**: All files encrypted with age before upload
- **Minimal cost**: Uses Cloudflare R2 free tier (10GB included)
- **Simple CLI**: `push`, `pull`, `status`, `diff` commands
- **Conflict handling**: Automatic detection with `.conflict` file preservation

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

# Initialize with same R2 credentials
claude-sync init

# Copy your encryption key from first device
# (replace with your actual key content)
scp first-laptop:~/.claude-sync/age-key.txt ~/.claude-sync/

# Pull sessions
claude-sync pull
```

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

When both local and remote files have changed:

1. Local version is kept as-is
2. Remote version is saved as `<filename>.conflict.<timestamp>`
3. You can manually review and merge

## Security

- All files are encrypted with [age](https://github.com/FiloSottile/age) before upload
- Encryption key never leaves your devices
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
