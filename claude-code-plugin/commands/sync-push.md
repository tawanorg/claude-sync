---
name: sync-push
description: Push local Claude Code sessions to cloud storage
---

# Sync Push

Push local changes to cloud storage.

## Check Configuration

First verify claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

If not configured, tell the user to run `/sync-init` first.

## Show Pending Changes

Show what will be uploaded:
```bash
claude-sync status
```

## Push Changes

Execute the push:
```bash
claude-sync push
```

## Report Results

After the push completes:
- Report how many files were uploaded
- Note any errors or conflicts
- Remind user they can run `/sync-status` to verify
