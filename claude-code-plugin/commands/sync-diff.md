---
name: sync-diff
description: Show differences between local and remote Claude Code sessions
---

# Sync Diff

Show detailed differences between local and remote files.

## Check Configuration

First verify claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

If not configured, tell the user to run `/sync-init` first.

## Show Differences

Get the diff:
```bash
claude-sync diff
```

## Report Results

Summarize the differences:
- Files only on local (would be uploaded)
- Files only on remote (would be downloaded)
- Files with different content (show which is newer if possible)

Suggest next actions based on what was found:
- `/sync-push` to upload local changes
- `/sync-pull` to download remote changes
- `/sync-status` for a quick summary
