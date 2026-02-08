---
name: sync-reset
description: Reset claude-sync configuration and optionally clear data
---

# Sync Reset

Reset claude-sync configuration. Use this if you forgot your passphrase or want to start fresh.

## Check Current State

First check if claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

## Confirm with User

Use `AskUserQuestion` to confirm the reset and ask what to clear:

**Options:**
1. **Config only** - Just delete local config and key (can reconfigure)
2. **Config + remote** - Also delete all files from cloud storage
3. **Full reset** - Clear config, remote, AND local sync state

**Warning the user:**
- If clearing remote, all synced files will be deleted from cloud storage
- They'll need to run `/sync-init` again after reset
- If they forgot passphrase, their encrypted remote files cannot be recovered

## Execute Reset

Based on user choice:

**Config only:**
```bash
claude-sync reset --force
```

**Config + remote:**
```bash
claude-sync reset --remote --force
```

**Full reset (nuclear option):**
```bash
claude-sync reset --remote --local --force
```

## After Reset

Inform the user:
- Configuration has been cleared
- Run `/sync-init` to set up again
- If they cleared remote, they can push from current device with new passphrase
