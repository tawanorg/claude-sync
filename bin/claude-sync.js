#!/usr/bin/env node

const { spawn } = require("child_process");
const path = require("path");
const fs = require("fs");

function getBinaryPath() {
  const platform = process.platform;
  const binaryName = platform === "win32" ? "claude-sync.exe" : "claude-sync";
  const binaryPath = path.join(__dirname, binaryName);

  if (!fs.existsSync(binaryPath)) {
    console.error("Error: claude-sync binary not found.");
    console.error("Try reinstalling: npm install -g claude-sync");
    process.exit(1);
  }

  return binaryPath;
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
