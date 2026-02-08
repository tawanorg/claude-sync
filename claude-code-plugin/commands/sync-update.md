---
name: sync-update
description: Update claude-sync CLI to the latest version
---

# Sync Update

Check for and install updates to the claude-sync CLI.

## Check for Updates

First check if an update is available:
```bash
claude-sync update --check
```

## Install Update

If an update is available and user wants to install:
```bash
claude-sync update
```

## Report Results

After the check/update:
- Report current version and latest available
- If update was installed, remind user to restart their terminal
- If already up to date, confirm current version

## Alternative Installation Methods

If the update command fails, suggest alternatives:

**npm:**
```bash
npm update -g @tawandotorg/claude-sync
```

**Direct download:**
```bash
# macOS ARM
curl -L https://github.com/tawanorg/claude-sync/releases/latest/download/claude-sync-darwin-arm64 -o /usr/local/bin/claude-sync
chmod +x /usr/local/bin/claude-sync
```
