# Integration Tests

This directory contains integration tests that verify cross-device sync with real R2 storage.

## Prerequisites

1. **R2 Bucket**: Create a dedicated test bucket in Cloudflare R2
2. **API Token**: Create an R2 API token with read/write access
3. **Docker**: Required for multi-device simulation

## Running Tests

### With Docker (Recommended)

This simulates two separate devices with isolated filesystems:

```bash
# Set environment variables
export CLAUDE_SYNC_R2_ACCOUNT_ID=your_account_id
export CLAUDE_SYNC_R2_ACCESS_KEY_ID=your_access_key
export CLAUDE_SYNC_R2_SECRET_ACCESS_KEY=your_secret_key
export CLAUDE_SYNC_R2_BUCKET=claude-sync-test

# Optional: custom passphrase (default: test-passphrase-123)
export CLAUDE_SYNC_TEST_PASSPHRASE=your-test-passphrase

# Run tests
cd integration
docker-compose up --build
```

### Without Docker

Run Go integration tests directly (requires R2 credentials):

```bash
export CLAUDE_SYNC_R2_ACCOUNT_ID=xxx
export CLAUDE_SYNC_R2_ACCESS_KEY_ID=xxx
export CLAUDE_SYNC_R2_SECRET_ACCESS_KEY=xxx
export CLAUDE_SYNC_R2_BUCKET=claude-sync-test

go test -v -tags=integration ./integration/...
```

## Test Scenarios

### 1. Basic Cross-Device Sync
- Device A: init with passphrase, create files, push
- Device B: init with same passphrase, pull
- Verify: Device B has same files as Device A

### 2. Key Mismatch Detection
- Device A: init with passphrase-1, push files
- Device B: init with passphrase-2
- Verify: init detects mismatch and offers options

### 3. Conflict Resolution
- Both devices modify same file
- Device A pushes first
- Device B pulls (should create .conflict file)
- Verify: conflict file exists with correct content

### 4. Reset Remote and Re-push
- Setup with mismatched keys
- Device B: reset --remote, init, push
- Device A: pull
- Verify: Device A gets Device B's files

## Cleanup

Tests automatically clean up the remote bucket after completion. If tests fail mid-execution, manually clear the test bucket:

```bash
# Using claude-sync
claude-sync reset --remote --force

# Or using AWS CLI with R2 endpoint
aws s3 rm s3://claude-sync-test --recursive \
  --endpoint-url https://<account_id>.r2.cloudflarestorage.com
```

## CI/CD Integration

For GitHub Actions, store R2 credentials as secrets:

```yaml
env:
  CLAUDE_SYNC_R2_ACCOUNT_ID: ${{ secrets.R2_ACCOUNT_ID }}
  CLAUDE_SYNC_R2_ACCESS_KEY_ID: ${{ secrets.R2_ACCESS_KEY_ID }}
  CLAUDE_SYNC_R2_SECRET_ACCESS_KEY: ${{ secrets.R2_SECRET_ACCESS_KEY }}
  CLAUDE_SYNC_R2_BUCKET: claude-sync-ci-test
```
