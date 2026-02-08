---
name: sync-init
description: Configure claude-sync for cross-device session synchronization
---

# Claude Sync Configuration Wizard

You are helping the user set up claude-sync for cross-device synchronization of their Claude Code sessions.

## Mode Detection

First, check the current state to determine which mode to use:

```bash
test -f ~/.claude-sync/config.yaml && echo "has_config" || echo "no_config"
test -f ~/.claude-sync/age-key.txt && echo "has_key" || echo "no_key"
```

### Passphrase-Only Mode

If config exists but user wants to re-enter passphrase (wrong passphrase on new device):
- Use `AskUserQuestion` to ask: "Do you want to re-enter your passphrase? (Use this if you entered the wrong passphrase)"
- If yes, skip to the **Encryption Passphrase** step - keep existing storage config
- This is equivalent to `claude-sync init --passphrase`

### Full Setup Mode

If no config exists, or user wants to start fresh:
- Run the full wizard below

## Prerequisites Check

First, check if claude-sync is installed:

```bash
command -v claude-sync
```

If not installed, tell the user to install it first:
```bash
npm install -g @tawandotorg/claude-sync
```

## Configuration Flow

Use `AskUserQuestion` to gather the following information step by step:

### Step 1: Storage Provider

Ask which cloud storage provider they want to use:
- **Cloudflare R2** (Recommended) - 10GB free, best for personal use
- **AWS S3** - For AWS users
- **Google Cloud Storage** - For GCP users

### Step 2: Provider-Specific Credentials

Based on the provider selected:

**For Cloudflare R2:**
- Bucket name (suggest: `claude-sync`)
- Account ID (found in Cloudflare dashboard URL)
- Access Key ID (from R2 API Tokens)
- Secret Access Key

**For AWS S3:**
- Bucket name
- Region (e.g., `us-east-1`)
- Access Key ID
- Secret Access Key

**For Google Cloud Storage:**
- Bucket name
- Project ID
- Ask if they want to use:
  - Default credentials (`gcloud auth application-default login`)
  - Service account JSON file path

### Step 3: Encryption Passphrase

Ask for an encryption passphrase:
- Must be at least 8 characters
- Recommend 12+ characters for security
- **Important**: The same passphrase must be used on all devices to sync
- The passphrase is never stored - only the derived encryption key

## Writing Configuration

After gathering all information, create the config directory and files:

```bash
mkdir -p ~/.claude-sync
chmod 700 ~/.claude-sync
```

Write `~/.claude-sync/config.yaml` with the appropriate format:

**For R2:**
```yaml
storage:
  provider: r2
  bucket: <bucket_name>
  account_id: <account_id>
  access_key_id: <access_key_id>
  secret_access_key: <secret_access_key>
encryption_key_path: ~/.claude-sync/age-key.txt
```

**For S3:**
```yaml
storage:
  provider: s3
  bucket: <bucket_name>
  region: <region>
  access_key_id: <access_key_id>
  secret_access_key: <secret_access_key>
encryption_key_path: ~/.claude-sync/age-key.txt
```

**For GCS with default credentials:**
```yaml
storage:
  provider: gcs
  bucket: <bucket_name>
  project_id: <project_id>
  use_default_credentials: true
encryption_key_path: ~/.claude-sync/age-key.txt
```

**For GCS with service account:**
```yaml
storage:
  provider: gcs
  bucket: <bucket_name>
  project_id: <project_id>
  credentials_file: <path_to_json>
encryption_key_path: ~/.claude-sync/age-key.txt
```

Set proper permissions:
```bash
chmod 600 ~/.claude-sync/config.yaml
```

## Generate Encryption Key

Run the key generation script with the passphrase:

```bash
python3 {{plugin_dir}}/scripts/generate-key.py "<passphrase>" ~/.claude-sync/age-key.txt
```

Note: If argon2-cffi is not installed, install it first:
```bash
pip3 install argon2-cffi
```

## Verify Configuration

Test the connection:
```bash
claude-sync status
```

If this is not the first device and there are existing remote files, verify the passphrase works:
```bash
claude-sync pull --dry-run
```

## Success Message

After successful configuration, inform the user:

- Configuration saved to `~/.claude-sync/config.yaml`
- Encryption key saved to `~/.claude-sync/age-key.txt`
- They can now use `/sync-push` to upload and `/sync-pull` to download
- Auto-sync is enabled: changes will be pushed on session end or after 5 minutes of idle time
- **Remember the passphrase** - use the same one on other devices to sync

## Security Notes

- Credentials are stored with 600 permissions (owner read/write only)
- All files are encrypted with age before upload
- The passphrase is never stored - only the derived key
- Cloud storage should be private (API key authenticated)
