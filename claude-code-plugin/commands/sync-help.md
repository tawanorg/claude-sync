---
name: sync-help
description: Show claude-sync commands and options
---

# Claude Sync Help

Display available commands and their options.

## Commands Overview

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
| `/sync-help` | Show this help |

## Init Options

```
/sync-init              # Full setup wizard
/sync-init --passphrase # Re-enter passphrase only (keeps storage config)
/sync-init --force      # Reset everything, start fresh
```

Use `--passphrase` if you entered the wrong passphrase on a new device.

## Pull Options

```
/sync-pull              # Pull with safety prompts
/sync-pull --dry-run    # Preview what would change
/sync-pull --force      # Skip confirmation prompts
```

## Conflicts Options

```
/sync-conflicts         # Interactive resolution
/sync-conflicts --list  # Just list conflicts
/sync-conflicts --keep local   # Keep all local versions
/sync-conflicts --keep remote  # Keep all remote versions
```

## Reset Options

```
/sync-reset             # Clear local config only
/sync-reset --remote    # Also delete files from cloud storage
/sync-reset --local     # Also clear local sync state
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

## Common Workflows

**First device setup:**
1. `/sync-init` - configure storage and passphrase
2. `/sync-push` - upload sessions

**Second device setup:**
1. `/sync-init` - use SAME passphrase
2. `/sync-pull` - download sessions

**Daily use:**
- Push happens automatically on session end
- Use `/sync-pull` when starting on a different device

**Wrong passphrase?**
- Run `/sync-init --passphrase` to re-enter

**Forgot passphrase?**
- Run `/sync-reset --remote` then `/sync-init` with new passphrase

## More Info

- CLI docs: https://github.com/tawanorg/claude-sync
- Report issues: https://github.com/tawanorg/claude-sync/issues
