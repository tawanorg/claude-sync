# Claude Sync Plugin for Claude Code

Sync your Claude Code sessions across devices with encrypted cloud storage.

## Features

- **Cross-device sync**: Continue Claude Code conversations on any laptop
- **Interactive setup**: Natural conversation-based configuration via `/sync-init`
- **Auto-sync**: Automatically pushes changes on session end or after 5 minutes idle
- **End-to-end encryption**: All files encrypted with age before upload
- **Multi-provider**: Supports Cloudflare R2, AWS S3, and Google Cloud Storage

## Installation

### Prerequisites

1. Install claude-sync CLI:
   ```bash
   npm install -g @tawandotorg/claude-sync
   ```

2. Install Python dependency for key generation:
   ```bash
   pip3 install argon2-cffi
   ```

### Add the Plugin

**Option 1: Install from GitHub (Recommended)**

In a Claude Code session, run:
```
/plugin marketplace add tawanorg/claude-sync
/plugin install claude-sync
```

**Option 2: Load locally for development**
```bash
claude --plugin-dir ./claude-code-plugin
```

## Usage

### Initial Setup

Run the configuration wizard:
```
/sync-init
```

Claude will walk you through:
1. Selecting a storage provider (R2, S3, or GCS)
2. Entering your credentials
3. Setting an encryption passphrase

### Commands

| Command | Description |
|---------|-------------|
| `/sync-init` | Configure cloud storage and encryption |
| `/sync-push` | Upload local changes to cloud |
| `/sync-pull` | Download remote changes |
| `/sync-status` | Show pending local changes |
| `/sync-diff` | Show differences between local and remote |
| `/sync-conflicts` | List and resolve sync conflicts |
| `/sync-reset` | Reset configuration (forgot passphrase) |
| `/sync-update` | Update claude-sync CLI |
| `/sync-help` | Show all commands and options |

### Command Options

#### `/sync-init`
```
/sync-init              # Full setup wizard
/sync-init --passphrase # Re-enter passphrase only (keeps storage config)
/sync-init --force      # Reset everything, start fresh
```

#### `/sync-pull`
```
/sync-pull              # Pull with safety prompts
/sync-pull --dry-run    # Preview what would change
/sync-pull --force      # Skip confirmation prompts
```

#### `/sync-conflicts`
```
/sync-conflicts         # Interactive resolution
/sync-conflicts --list  # Just list conflicts
/sync-conflicts --keep local   # Keep all local versions
/sync-conflicts --keep remote  # Keep all remote versions
```

#### `/sync-reset`
```
/sync-reset             # Clear local config only
/sync-reset --remote    # Also delete files from cloud storage
/sync-reset --local     # Also clear local sync state
```

#### `/sync-update`
```
/sync-update            # Update to latest version
/sync-update --check    # Only check for updates
```

### Auto-Sync

The plugin automatically:
- Checks configuration status on session start
- Pushes changes when the session ends
- Pushes changes after 5 minutes of idle time (debounced)

## Setting Up a Second Device

1. Install the CLI: `npm install -g @tawandotorg/claude-sync`
2. Install argon2: `pip3 install argon2-cffi`
3. Install the plugin:
   ```
   /plugin marketplace add tawanorg/claude-sync
   /plugin install claude-sync
   ```
4. Run `/sync-init`
5. Enter the **same** storage credentials
6. Enter the **same** encryption passphrase
7. Run `/sync-pull` to download your sessions

The passphrase generates the same encryption key on any device, so there's no need to copy key files.

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

## Security

- Files encrypted with [age](https://github.com/FiloSottile/age) before upload
- Passphrase-derived keys use Argon2id (memory-hard KDF)
- Passphrase is never stored - only the derived key at `~/.claude-sync/age-key.txt`
- Config files stored with 0600 permissions

## Troubleshooting

### "claude-sync is not installed"

Install the CLI:
```bash
npm install -g @tawandotorg/claude-sync
```

### "argon2-cffi package required"

Install the Python dependency:
```bash
pip3 install argon2-cffi
```

### Wrong Passphrase on New Device

Re-run init to enter the correct passphrase:
```
/sync-init --passphrase
```
Or via CLI:
```bash
claude-sync init --passphrase
```

### Forgot Passphrase

The passphrase cannot be recovered. You'll need to reset and start fresh:
```
/sync-reset --remote
/sync-init
/sync-push
```

### Conflicts After Pull

When both local and remote files change, conflicts are created:
```
/sync-conflicts
```

## Plugin Structure

```
claude-code-plugin/
├── .claude-plugin/
│   └── plugin.json          # Plugin metadata
├── hooks/
│   └── hooks.json           # SessionStart, SessionEnd, Idle hooks
├── scripts/
│   ├── check-config.sh      # Check if configured on session start
│   ├── auto-push.sh         # Auto-push with 5-minute debounce
│   └── generate-key.py      # Argon2id key derivation from passphrase
├── commands/
│   ├── sync-init.md         # Interactive configuration wizard
│   ├── sync-push.md         # Push command
│   ├── sync-pull.md         # Pull command
│   ├── sync-status.md       # Status command
│   ├── sync-diff.md         # Diff command
│   ├── sync-conflicts.md    # Conflict resolution
│   ├── sync-reset.md        # Reset configuration
│   ├── sync-update.md       # Update CLI
│   └── sync-help.md         # Help and reference
└── README.md
```

## License

MIT
