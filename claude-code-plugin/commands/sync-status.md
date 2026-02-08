---
name: sync-status
description: Show pending local changes for claude-sync
---

# Sync Status

Show the current sync status and any pending local changes.

## Check Configuration

First verify claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

If not configured, tell the user to run `/sync-init` first.

## Show Status

Get the current status:
```bash
claude-sync status
```

## Report Results

Summarize the status:
- Number of files with local changes (need to push)
- Number of files that would be updated from remote (need to pull)
- Any conflicts that need resolution

Suggest next actions:
- Use `/sync-push` to upload local changes
- Use `/sync-pull` to download remote changes
- Use `/sync-diff` to see detailed differences
