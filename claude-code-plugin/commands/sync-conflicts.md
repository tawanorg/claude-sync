---
name: sync-conflicts
description: List and resolve sync conflicts
---

# Sync Conflicts

Find and resolve conflicts from sync operations. When both local and remote files change, the remote version is saved as a `.conflict` file.

## Check Configuration

First verify claude-sync is configured:
```bash
test -f ~/.claude-sync/config.yaml && echo "configured" || echo "not configured"
```

If not configured, tell the user to run `/sync-init` first.

## List Conflicts

Show all conflict files:
```bash
claude-sync conflicts --list
```

## Interactive Resolution

For interactive resolution, ask the user how they want to proceed:

1. **Resolve one at a time** - Guide through each conflict interactively
2. **Keep all local** - Run `claude-sync conflicts --keep local`
3. **Keep all remote** - Run `claude-sync conflicts --keep remote`

### Batch Resolution

Keep all local versions:
```bash
claude-sync conflicts --keep local
```

Keep all remote versions:
```bash
claude-sync conflicts --keep remote
```

## Manual Resolution

For complex conflicts, you can:
1. Read both files to compare:
   - Local file: the original path
   - Conflict file: `{path}.conflict.{timestamp}`
2. Show diff between them
3. Let user choose which to keep or merge manually

## Report Results

After resolution:
- Report how many conflicts were resolved
- Note any that were skipped
- Remind about `/sync-push` if they made changes
