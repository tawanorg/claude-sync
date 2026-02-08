#!/bin/bash
# Auto-push with debounce (5 minutes)
# Only pushes if there are changes and enough time has passed since last push

CONFIG_DIR="$HOME/.claude-sync"
LAST_PUSH_FILE="$CONFIG_DIR/.last-auto-push"
DEBOUNCE_SECONDS=300  # 5 minutes

# Check if claude-sync is installed and configured
if ! command -v claude-sync &> /dev/null; then
    exit 0
fi

if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    exit 0
fi

# Check debounce
if [ -f "$LAST_PUSH_FILE" ]; then
    LAST_PUSH=$(cat "$LAST_PUSH_FILE" 2>/dev/null || echo 0)
    NOW=$(date +%s)
    ELAPSED=$((NOW - LAST_PUSH))

    if [ $ELAPSED -lt $DEBOUNCE_SECONDS ]; then
        exit 0
    fi
fi

# Check if there are changes to push
if ! claude-sync status -q 2>/dev/null | grep -q .; then
    exit 0
fi

# Push changes
if claude-sync push -q 2>/dev/null; then
    # Update last push timestamp
    date +%s > "$LAST_PUSH_FILE"
fi
