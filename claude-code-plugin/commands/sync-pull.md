---
name: sync-pull
description: Pull Claude Code sessions from cloud storage
---

# Sync Pull

Download remote changes from cloud storage.

## Check Configuration

First verify claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

If not configured, tell the user to run `/sync-init` first.

## Preview Changes (Recommended)

First show what would change:
```bash
claude-sync pull --dry-run
```

## Confirm with User

Use `AskUserQuestion` to ask the user if they want to proceed with the pull.

If there are existing local files that would be overwritten, warn the user that:
- A backup will be created at `~/.claude.backup.<timestamp>`
- They can restore from the backup if needed

## Pull Changes

Execute the pull:
```bash
claude-sync pull
```

Or with force flag if user confirmed:
```bash
claude-sync pull --force
```

## Report Results

After the pull completes:
- Report how many files were downloaded
- Note the backup location if one was created
- Mention any conflicts that need resolution with `/sync-conflicts`
