#!/usr/bin/env node

const https = require("https");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");

const REPO = "tawanorg/claude-sync";
const ALLOWED_HOSTS = ["github.com", "objects.githubusercontent.com"];
const MAX_REDIRECTS = 5;

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

function getLatestVersion() {
  return new Promise((resolve, reject) => {
    const options = {
      hostname: "api.github.com",
      path: `/repos/${REPO}/releases/latest`,
      headers: {
        "User-Agent": "claude-sync-installer",
      },
    };

    https
      .get(options, (response) => {
        let data = "";
        response.on("data", (chunk) => (data += chunk));
        response.on("end", () => {
          try {
            const release = JSON.parse(data);
            if (release.tag_name) {
              // Remove 'v' prefix if present
              resolve(release.tag_name.replace(/^v/, ""));
            } else {
              reject(new Error("No release found"));
            }
          } catch (e) {
            reject(e);
          }
        });
      })
      .on("error", reject);
  });
}

function getDownloadUrl(version) {
  const binaryName = getBinaryName();
  return `https://github.com/${REPO}/releases/download/v${version}/${binaryName}`;
}

function getChecksumsUrl(version) {
  return `https://github.com/${REPO}/releases/download/v${version}/checksums.txt`;
}

/**
 * Validates that a URL's host is in the allowed list.
 */
function isAllowedHost(url) {
  try {
    const parsed = new URL(url);
    return ALLOWED_HOSTS.includes(parsed.hostname);
  } catch {
    return false;
  }
}

/**
 * Downloads a URL to a buffer with redirect handling.
 */
function downloadToBuffer(url, redirectCount = 0) {
  return new Promise((resolve, reject) => {
    if (redirectCount > MAX_REDIRECTS) {
      reject(new Error(`Too many redirects (max ${MAX_REDIRECTS})`));
      return;
    }

    if (!isAllowedHost(url)) {
      reject(new Error(`Redirect to untrusted host: ${url}`));
      return;
    }

    https
      .get(url, (response) => {
        // Handle redirects
        if (response.statusCode === 302 || response.statusCode === 301) {
          const location = response.headers.location;
          if (!location) {
            reject(new Error("Redirect without location header"));
            return;
          }
          downloadToBuffer(location, redirectCount + 1)
            .then(resolve)
            .catch(reject);
          return;
        }

        if (response.statusCode !== 200) {
          reject(new Error(`Failed to download: HTTP ${response.statusCode}`));
          return;
        }

        const chunks = [];
        response.on("data", (chunk) => chunks.push(chunk));
        response.on("end", () => resolve(Buffer.concat(chunks)));
        response.on("error", reject);
      })
      .on("error", reject);
  });
}

/**
 * Parses checksums.txt and returns the expected hash for a given filename.
 */
function parseChecksum(checksumsContent, filename) {
  const lines = checksumsContent.toString().split("\n");
  for (const line of lines) {
    // Format: "hash  filename" or "hash *filename" (binary mode)
    const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    if (match && match[2].trim() === filename) {
      return match[1].toLowerCase();
    }
  }
  return null;
}

/**
 * Downloads a file to disk with redirect handling.
 */
function download(url, dest, redirectCount = 0) {
  return new Promise((resolve, reject) => {
    if (redirectCount > MAX_REDIRECTS) {
      reject(new Error(`Too many redirects (max ${MAX_REDIRECTS})`));
      return;
    }

    if (!isAllowedHost(url)) {
      reject(new Error(`Redirect to untrusted host: ${url}`));
      return;
    }

    const file = fs.createWriteStream(dest);

    https
      .get(url, (response) => {
        // Handle redirects
        if (response.statusCode === 302 || response.statusCode === 301) {
          file.close();
          fs.unlinkSync(dest);
          const location = response.headers.location;
          if (!location) {
            reject(new Error("Redirect without location header"));
            return;
          }
          download(location, dest, redirectCount + 1)
            .then(resolve)
            .catch(reject);
          return;
        }

        if (response.statusCode !== 200) {
          file.close();
          fs.unlinkSync(dest);
          reject(new Error(`Failed to download: HTTP ${response.statusCode}`));
          return;
        }

        response.pipe(file);
        file.on("finish", () => {
          file.close();
          resolve();
        });
      })
      .on("error", (err) => {
        file.close();
        fs.unlink(dest, () => {});
        reject(err);
      });
  });
}

/**
 * Computes SHA256 hash of a file.
 */
function hashFile(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash("sha256");
    const stream = fs.createReadStream(filePath);
    stream.on("data", (data) => hash.update(data));
    stream.on("end", () => resolve(hash.digest("hex")));
    stream.on("error", reject);
  });
}

async function install() {
  const binDir = path.join(__dirname, "bin");
  const platform = getPlatform();
  const binaryName = platform === "windows" ? "claude-sync.exe" : "claude-sync";
  const binaryPath = path.join(binDir, binaryName);
  const assetName = getBinaryName();

  // Create bin directory
  if (!fs.existsSync(binDir)) {
    fs.mkdirSync(binDir, { recursive: true });
  }

  try {
    console.log("Fetching latest version...");
    const version = await getLatestVersion();
    const url = getDownloadUrl(version);
    const checksumsUrl = getChecksumsUrl(version);

    console.log(`Downloading claude-sync v${version}...`);
    console.log(`  Platform: ${getPlatform()}-${getArch()}`);

    // Download checksums first
    let expectedHash = null;
    try {
      console.log("  Fetching checksums...");
      const checksumsData = await downloadToBuffer(checksumsUrl);
      expectedHash = parseChecksum(checksumsData, assetName);
      if (expectedHash) {
        console.log(`  Expected SHA256: ${expectedHash.substring(0, 16)}...`);
      }
    } catch (err) {
      console.warn(`  Warning: Could not fetch checksums.txt: ${err.message}`);
      console.warn("  Proceeding without checksum verification.");
    }

    // Download binary
    await download(url, binaryPath);

    // Verify checksum if available
    if (expectedHash) {
      console.log("  Verifying checksum...");
      const actualHash = await hashFile(binaryPath);
      if (actualHash !== expectedHash) {
        fs.unlinkSync(binaryPath);
        throw new Error(
          `Checksum mismatch!\n` +
            `  Expected: ${expectedHash}\n` +
            `  Actual:   ${actualHash}\n` +
            `This could indicate a corrupted download or tampering.`
        );
      }
      console.log("  Checksum verified!");
    }

    // Make executable on Unix
    if (platform !== "windows") {
      fs.chmodSync(binaryPath, 0o755);
    }

    console.log(`  Installed to: ${binaryPath}`);
    console.log(`\n✓ claude-sync v${version} installed successfully!`);
  } catch (err) {
    console.error(`\n✗ Failed to install claude-sync: ${err.message}`);
    console.error(`\nYou can manually download from:`);
    console.error(`  https://github.com/${REPO}/releases/latest`);
    process.exit(1);
  }
}

install();
