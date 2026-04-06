#!/bin/bash
set -euo pipefail

# Publish platform-specific npm packages for claude-sync
# Usage: ./scripts/publish-npm.sh <version>
# Expects binaries in bin/ directory (from build-all or CI artifacts)

VERSION="${1:?Usage: publish-npm.sh <version>}"
VERSION="${VERSION#v}" # Strip v prefix

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
NPM_DIR="$ROOT_DIR/npm"

# Map: npm platform → Go binary name
declare -A PLATFORM_MAP=(
  ["darwin-arm64"]="claude-sync-darwin-arm64"
  ["darwin-x64"]="claude-sync-darwin-amd64"
  ["linux-arm64"]="claude-sync-linux-arm64"
  ["linux-x64"]="claude-sync-linux-amd64"
  ["win32-arm64"]="claude-sync-windows-arm64.exe"
  ["win32-x64"]="claude-sync-windows-amd64.exe"
)

# Binary name per platform
declare -A BINARY_NAME=(
  ["darwin-arm64"]="claude-sync"
  ["darwin-x64"]="claude-sync"
  ["linux-arm64"]="claude-sync"
  ["linux-x64"]="claude-sync"
  ["win32-arm64"]="claude-sync.exe"
  ["win32-x64"]="claude-sync.exe"
)

for platform in "${!PLATFORM_MAP[@]}"; do
  src_binary="${PLATFORM_MAP[$platform]}"
  dst_binary="${BINARY_NAME[$platform]}"
  pkg_dir="$NPM_DIR/$platform"

  echo "Publishing @tawandotorg/claude-sync-${platform}@${VERSION}..."

  # Copy binary into package directory
  if [ -f "$ROOT_DIR/bin/$src_binary" ]; then
    cp "$ROOT_DIR/bin/$src_binary" "$pkg_dir/$dst_binary"
    chmod +x "$pkg_dir/$dst_binary"
  elif [ -f "$ROOT_DIR/$src_binary" ]; then
    cp "$ROOT_DIR/$src_binary" "$pkg_dir/$dst_binary"
    chmod +x "$pkg_dir/$dst_binary"
  else
    echo "  WARNING: Binary $src_binary not found, skipping $platform"
    continue
  fi

  # Update version in package.json
  cd "$pkg_dir"
  npm version "$VERSION" --no-git-tag-version --allow-same-version 2>/dev/null
  npm publish --access public
  echo "  Published!"

  # Clean up binary (don't commit binaries)
  rm -f "$pkg_dir/$dst_binary"
done

# Publish main package
echo "Publishing @tawandotorg/claude-sync@${VERSION}..."
cd "$ROOT_DIR"

# Update optionalDependencies versions
for platform in "${!PLATFORM_MAP[@]}"; do
  sed -i.bak "s|\"@tawandotorg/claude-sync-${platform}\": \".*\"|\"@tawandotorg/claude-sync-${platform}\": \"${VERSION}\"|" package.json
done
rm -f package.json.bak

npm version "$VERSION" --no-git-tag-version --allow-same-version 2>/dev/null
npm publish --access public
echo "Published @tawandotorg/claude-sync@${VERSION}!"
