#!/usr/bin/env node

const { spawn } = require("child_process");
const path = require("path");
const fs = require("fs");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "@tawandotorg/claude-sync-darwin-arm64",
  "darwin-x64": "@tawandotorg/claude-sync-darwin-x64",
  "linux-arm64": "@tawandotorg/claude-sync-linux-arm64",
  "linux-x64": "@tawandotorg/claude-sync-linux-x64",
  "win32-arm64": "@tawandotorg/claude-sync-win32-arm64",
  "win32-x64": "@tawandotorg/claude-sync-win32-x64",
};

function getBinaryPath() {
  const platformKey = `${process.platform}-${process.arch}`;
  const packageName = PLATFORM_PACKAGES[platformKey];

  if (!packageName) {
    console.error(`Error: Unsupported platform ${platformKey}`);
    console.error("Supported: darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-arm64, win32-x64");
    process.exit(1);
  }

  const binaryName = process.platform === "win32" ? "claude-sync.exe" : "claude-sync";

  // Try to find the binary from the platform-specific package
  try {
    const packageDir = path.dirname(require.resolve(`${packageName}/package.json`));
    const binaryPath = path.join(packageDir, binaryName);
    if (fs.existsSync(binaryPath)) {
      return binaryPath;
    }
  } catch (e) {
    // Package not installed
  }

  // Fallback: check local bin directory (for development)
  const localPath = path.join(__dirname, binaryName);
  if (fs.existsSync(localPath)) {
    return localPath;
  }

  console.error(`Error: claude-sync binary not found for ${platformKey}.`);
  console.error(`The platform package ${packageName} may not be installed.`);
  console.error("Try reinstalling: npm install -g @tawandotorg/claude-sync");
  process.exit(1);
}

const binary = getBinaryPath();
const args = process.argv.slice(2);

const child = spawn(binary, args, {
  stdio: "inherit",
  env: process.env,
});

child.on("error", (err) => {
  console.error(`Failed to start claude-sync: ${err.message}`);
  process.exit(1);
});

child.on("close", (code) => {
  process.exit(code || 0);
});
