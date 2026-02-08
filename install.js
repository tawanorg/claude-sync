#!/usr/bin/env node

const https = require("https");
const fs = require("fs");
const path = require("path");
const { execSync } = require("child_process");

const VERSION = require("./package.json").version;
const REPO = "tawanorg/claude-sync";

function getPlatform() {
  const platform = process.platform;
  switch (platform) {
    case "darwin":
      return "darwin";
    case "linux":
      return "linux";
    case "win32":
      return "windows";
    default:
      throw new Error(`Unsupported platform: ${platform}`);
  }
}

function getArch() {
  const arch = process.arch;
  switch (arch) {
    case "x64":
      return "amd64";
    case "arm64":
      return "arm64";
    default:
      throw new Error(`Unsupported architecture: ${arch}`);
  }
}

function getBinaryName() {
  const platform = getPlatform();
  const arch = getArch();
  const ext = platform === "windows" ? ".exe" : "";
  return `claude-sync-${platform}-${arch}${ext}`;
}

function getDownloadUrl() {
  const binaryName = getBinaryName();
  return `https://github.com/${REPO}/releases/download/v${VERSION}/${binaryName}`;
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);

    const request = (url) => {
      https
        .get(url, (response) => {
          // Handle redirects
          if (response.statusCode === 302 || response.statusCode === 301) {
            request(response.headers.location);
            return;
          }

          if (response.statusCode !== 200) {
            reject(new Error(`Failed to download: ${response.statusCode}`));
            return;
          }

          response.pipe(file);
          file.on("finish", () => {
            file.close();
            resolve();
          });
        })
        .on("error", (err) => {
          fs.unlink(dest, () => {});
          reject(err);
        });
    };

    request(url);
  });
}

async function install() {
  const binDir = path.join(__dirname, "bin");
  const platform = getPlatform();
  const binaryName = platform === "windows" ? "claude-sync.exe" : "claude-sync";
  const binaryPath = path.join(binDir, binaryName);

  // Create bin directory
  if (!fs.existsSync(binDir)) {
    fs.mkdirSync(binDir, { recursive: true });
  }

  const url = getDownloadUrl();
  console.log(`Downloading claude-sync v${VERSION}...`);
  console.log(`  Platform: ${getPlatform()}-${getArch()}`);

  try {
    await download(url, binaryPath);

    // Make executable on Unix
    if (platform !== "windows") {
      fs.chmodSync(binaryPath, 0o755);
    }

    console.log(`  Installed to: ${binaryPath}`);
    console.log(`\n✓ claude-sync installed successfully!`);
  } catch (err) {
    console.error(`\n✗ Failed to install claude-sync: ${err.message}`);
    console.error(`\nYou can manually download from:`);
    console.error(`  ${url}`);
    process.exit(1);
  }
}

install();
