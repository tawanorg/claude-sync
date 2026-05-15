# Spec: Extending claude-sync to cover OpenClaw + Bifrost

> Status: **Draft** (2026-05-15) · Owner: Merv · Tracking issue: TBD
> Motivation: today's session created ~4 hours of agent topology + Bifrost configuration that sits in `~/.openclaw/` and `~/.config/bifrost/`, neither of which any sync mechanism covers. A machine failure would lose all of it.

## 1. Context

`claude-sync` v1.8.x today syncs `~/.claude/` only — the SyncPaths in `internal/config/config.go` are `CLAUDE.md`, `settings*.json`, `agents`, `commands`, `skills`, `plugins`, `projects`, `history.jsonl`, `rules`. Plus the MCP special-case (three-way merge against `~/.claude.json` → `_external/mcp-servers.json.age`).

Three sync domains are in play in the Nexura environment:

| Domain | Where | Covered by |
|---|---|---|
| Claude Code state | `~/.claude/` | **claude-sync** ✓ |
| Repo source + docs | `~/nexura/` | git (NexuraPlatform/nexura) ✓ |
| **OpenClaw config + indexed memory** | `~/.openclaw/` | **nothing** ❌ |
| **Bifrost gateway config + history** | `~/.config/bifrost/` (Docker volume) | nightly local backup only ❌ for cross-device |

The OpenClaw gap is the biggest. `~/.openclaw/openclaw.json` contains the entire agent topology — five agent profiles with their model routing, the `models.providers.bifrost.models` catalog with deliberate exclusions, `memorySearch.query` tuning, the OClaw virtual-key apiKey. None of it is backed up or replicable from elsewhere. Losing the laptop loses the topology.

## 2. Goals

**V1 (this spec)** — sync the OpenClaw agent topology + indexed memory store across devices using the existing claude-sync crypto and storage layers.

- [ ] New "openclaw" sync pack. Same R2/S3/GCS backend, same age encryption.
- [ ] `claude-sync push --pack openclaw` / `claude-sync pull --pack openclaw`.
- [ ] Backwards-compatible: existing `~/.claude/` sync unchanged for users not enabling the pack.
- [ ] Smart conflict handling for `openclaw.json` (three-way merge analogous to MCP).
- [ ] First-pull safety (back up existing local `~/.openclaw/` before overwriting).

**V2 (future)** — bifrost pack covering `~/.config/bifrost/config.db` (provider/VK/governance config), excluding `logs.db` (rebuilds from activity). Deferred because the data lives in a Docker named volume, which adds complexity.

**V3 (future)** — auto-sync triggers (LaunchAgent on file change). V1+V2 are manual push/pull.

## 3. Non-goals

- ❌ Syncing `~/.openclaw/agents/*/sessions/*.jsonl` transcripts. They can be hundreds of MB per agent and provide marginal cross-device value.
- ❌ Syncing `~/.openclaw/agents/*/sessions/*.deleted.*` archive files.
- ❌ Syncing `~/.openclaw/agents/*/logs/*`. Logs are local diagnostic state.
- ❌ Syncing `~/.openclaw/cron/runs/*`. Cron run history is local.
- ❌ Real-time replication. Manual push/pull is the V1 model.
- ❌ Conflict-free CRDT merging. We use the same "keep local, write remote to `.conflict.<ts>`" pattern as the existing file-level sync.
- ❌ Syncing Bifrost `logs.db` (~870 MB and growing) in V2. Only `config.db`.

## 4. Design — the "sync pack" concept

Today's `Syncer` walks a single global `SyncPaths`. The V1 refactor turns each domain into a **Pack** with its own configuration, file walker, and merge behavior.

### 4.1 Pack interface (Go)

```go
// internal/sync/pack.go (new)
package sync

type Pack interface {
    // Identity
    Name() string                              // "claude", "mcp", "openclaw"
    Enabled() bool                             // from config

    // Filesystem
    LocalRoot() string                         // e.g. ~/.openclaw
    Walk() ([]LocalFile, error)                // honors Include/Exclude
    RemotePrefix() string                      // "claude/" or "openclaw/"

    // Merge semantics
    DetectChanges(state *SyncState) ([]WorkItem, error)
    MergeStrategy(path string) MergeStrategy   // FileLevel | ThreeWayJSON | SnapshotOnly

    // First-pull safety
    HasNonEmptyLocalState() bool
    BackupLocal(backupDir string) error
}

type MergeStrategy int
const (
    FileLevelKeepLocal MergeStrategy = iota   // default — current claude behavior
    ThreeWayJSONMerge                          // for openclaw.json (analogous to MCP)
    SnapshotOnly                               // for SQLite — newest wins, no merge
)
```

### 4.2 Config schema additions

`~/.claude-sync/config.yaml` gains a `packs:` section. Existing fields stay at top level for backwards-compat.

```yaml
# existing
storage:
  provider: r2
  bucket: <private>
  account_id: <private>
encryption_key_path: ~/.claude-sync/age-key.txt

# NEW
packs:
  claude:
    enabled: true                              # default — preserves current behavior
  mcp:
    enabled: true                              # default — preserves MCP three-way merge
  openclaw:
    enabled: false                             # opt-in
    local_root: ~/.openclaw
    include:
      - openclaw.json
      - agents/**/agent/AGENTS.md              # per-agent charters if any
      - agents/**/agent/models.json
      - memory/main.sqlite                     # snapshot semantics
      - memory/**/short-term-recall.json
      - cron/jobs.json                         # cron definitions (not runs/)
      - skills/                                # custom local skills (NOT ClawHub-installed under nexura/)
    exclude:
      - agents/**/sessions/                    # transcripts — huge, low value
      - agents/**/logs/                        # local diagnostics
      - cron/runs/                             # cron run history
      - "**/.cache/"
    merge:
      openclaw.json: three_way_json            # avoid clobbering local virtual-key changes
      memory/main.sqlite: snapshot_only        # newest wins; rebuildable from source
```

### 4.3 Remote layout

`R2 bucket / <prefix>` is unchanged for the claude pack. New packs land under `_external/<pack-name>/`:

```
<bucket>/
├── CLAUDE.md.age                              # existing
├── agents/                                    # existing
├── projects/                                  # existing
├── _external/
│   ├── mcp-servers.json.age                   # existing MCP
│   └── openclaw/                              # NEW
│       ├── openclaw.json.age
│       ├── agents/main/agent/models.json.age
│       ├── memory/main.sqlite.age
│       └── cron/jobs.json.age
```

The `_external/` convention preserves the "this is sync'd but lives outside `~/.claude/`" semantics already established for MCP.

### 4.4 State tracking

Single `state.json` extended to namespace per-pack:

```json
{
  "version": 2,
  "last_push_at": "2026-05-15T15:58:28Z",
  "packs": {
    "claude": {
      "files": { "CLAUDE.md": { "hash": "...", "size": 1234, "mtime": "...", "uploaded": "..." } }
    },
    "openclaw": {
      "files": { "openclaw.json": { "hash": "...", "size": 5678, "mtime": "...", "uploaded": "..." } }
    }
  }
}
```

A v1→v2 state migration on first run.

### 4.5 Three-way JSON merge for `openclaw.json`

Same pattern as MCP. On push:
1. Load local `openclaw.json` (current state).
2. Load `state.packs.openclaw.baseline` (last-synced version stored in state.json).
3. Pull remote `_external/openclaw/openclaw.json.age`, decrypt.
4. Compute `localΔ = local - baseline` and `remoteΔ = remote - baseline`.
5. Apply both deltas. Conflicts (same key changed differently on both sides) → keep local, log warning.
6. Push merged result, update baseline.

Specific care for two keys:
- `models.providers.bifrost.apiKey` — never merge, always keep local (machine-specific VK)
- `agents.list[].id` — list-of-objects merge by id; conflicts within an entry resolved per-field

### 4.6 SQLite snapshot semantics

For `memory/main.sqlite`:
- Treat as opaque blob.
- On conflict: rename remote to `memory/main.sqlite.conflict.<timestamp>.sqlite`, write to disk for forensic comparison. Local stays canonical.
- Pull-side: if local exists and remote is newer (LastModified > state.uploaded), prompt or auto-backup-then-overwrite (configurable).

## 5. Implementation phases

### Phase 1 — Pack abstraction refactor (1–2 days)
- [ ] Define `Pack` interface in `internal/sync/pack.go`.
- [ ] Extract current `~/.claude/` sync into a `ClaudePack` (no behavior change).
- [ ] Extract MCP sync into an `MCPPack` (no behavior change).
- [ ] State.json v1→v2 migration.
- [ ] All existing tests pass unchanged.
- [ ] **Acceptance**: `claude-sync push` and `pull` behave identically for existing users.

### Phase 2 — OpenClaw pack (2–3 days)
- [ ] Add `OpenClawPack` implementing `Pack`.
- [ ] Implement include/exclude glob walking.
- [ ] Three-way JSON merge for `openclaw.json` (extend `internal/sync/mcp.go` patterns).
- [ ] SQLite snapshot semantics for `memory/main.sqlite`.
- [ ] First-pull safety: detect non-empty local `~/.openclaw/`, prompt/backup to `~/.openclaw.backup.<ts>/`.
- [ ] CLI: `--pack` flag on push/pull/status/diff to scope operations.
- [ ] `claude-sync init` walks the user through enabling the openclaw pack.
- [ ] Conflict resolution UX: extend `claude-sync conflicts` to handle openclaw paths.
- [ ] Integration test (under `integration/`) with a real R2 round-trip.
- [ ] **Acceptance**: push from machine A, pull on machine B, `openclaw memory status` works identically on both.

### Phase 3 — Bifrost pack (deferred, ~3–4 days)
- [ ] Decide source: `docker run --rm -v bifrost-data:/d:ro ... cat /d/config.db` vs. helper sidecar pattern. Probably read directly from the named-volume mount path on Linux, use helper container on macOS.
- [ ] Coordinate with the nightly `backup.sh` so the sync doesn't double-snapshot.
- [ ] Exclude `logs.db` explicitly (size).
- [ ] Pull-side: write into the named volume (requires container coordination — restart Bifrost or accept brief inconsistency).
- [ ] Decide whether to sync `~/.config/bifrost/sync-keys` + `backup.sh` themselves (they live on the bind-mount adjacent to the volume).

### Phase 4 — Auto-sync triggers (deferred)
- [ ] LaunchAgent (macOS) / systemd timer (Linux) for periodic push.
- [ ] File-watcher option (fsnotify on `openclaw.json`).
- [ ] Cap push frequency (debounce 60s).

## 6. Storage policy

All packs use the same R2/S3/GCS backend the user has configured. Today's setup already uses a **private Cloudflare R2 bucket** in the user's own account — no shared/public bucket exposure. Spec requirement: documentation must call this out so a new user setting up claude-sync uses their own private bucket from the start.

Specifically:
- README + `claude-sync init` wizard must require a private bucket choice; never offer a "shared" or "demo" option.
- Encryption at rest: age (X25519 + ChaCha20-Poly1305), already in place. No change.
- Encryption in transit: TLS to R2/S3/GCS, already in place.
- Key material is **local-only** (`~/.claude-sync/age-key.txt`, mode 0600). Never sync the encryption key itself.

## 7. Operational ownership — open-source vs. private fork

`claude-sync` is currently the user's own public Go CLI on GitHub. Question raised: should it be forked into a private nexura repo since it's becoming load-bearing for the company's infrastructure?

### Stay public, contribute upstream (recommended)
**Pros**
- Public release pipeline (semantic-release, GitHub Releases, npm distribution) already works.
- Other users benefit; contributions back are possible.
- Less maintenance overhead — one codebase.

**Cons**
- New features are released publicly. Competitors see your tooling.
- The pack abstraction is non-trivial; reviewers (you) take public scrutiny.

### Private fork (defer unless real need)
**Pros**
- Nexura-specific extensions (e.g., a `nexura-internal` pack with tenant-specific config) don't leak.
- Release cadence is internal.
- Can include private dependencies if needed.

**Cons**
- Two codebases to keep in sync.
- npm distribution gets messier (private packages, auth on installs).
- The pack interface in this spec is generic — no obvious need for private-only code.

**Recommendation**: extend public `claude-sync` with the pack architecture (it's generic and useful to anyone), and only fork if a future Nexura-specific need arises that genuinely doesn't belong upstream. Keep nexura-specific config (paths, exclusions, R2 bucket) in `~/.claude-sync/config.yaml`, not in source.

If you want a "Nexura distribution" feel, an alternative is a thin wrapper repo (`NexuraPlatform/claude-sync-nexura`) that pins a claude-sync version + ships an opinionated `config.yaml` template. That's ~1 file of code, no fork divergence to maintain.

## 8. Test plan

- [ ] Unit: each Pack's Walk, DetectChanges, MergeStrategy in isolation.
- [ ] Unit: state.json v1→v2 migration round-trip.
- [ ] Unit: three-way merge of `openclaw.json` — three scenarios:
  1. No conflict (different keys changed on each side).
  2. Same-key conflict (keep local).
  3. List-of-objects merge by id (e.g., `agents.list[].model` changed on remote but added a new agent locally).
- [ ] Integration: round-trip an `openclaw.json` between two machines via R2.
- [ ] Integration: SQLite snapshot conflict produces `.conflict.<ts>` file.
- [ ] Integration: first pull with existing local `~/.openclaw/` triggers backup-or-abort prompt.
- [ ] Backwards-compat: existing `claude-sync push/pull/status` works on a config.yaml without `packs:` section.

## 9. Open questions

1. **Cron jobs sync**: should `~/.openclaw/cron/jobs.json` sync? On machine B, those crons would auto-fire and could double-charge (run twice across both machines). Probably **don't sync** by default; if needed, manual import command.
2. **Skills under `~/.openclaw/skills/`** vs `~/nexura/skills/`: the latter goes via git. The former (if any) is unclear — what's the difference? May need a one-time audit.
3. **First-pull backup destination**: `~/.openclaw.backup.<ts>/` lives forever unless cleaned. Add a `claude-sync prune-backups` command?
4. **Multi-account**: if a user has both personal and work OpenClaw setups, do they want separate buckets / encryption keys per environment? V1 says no; document the limitation.
5. **Bifrost on Linux vs macOS**: when V2 lands, the Docker named-volume access pattern differs. Probably needs platform-specific source resolvers.
6. **Conflict UX on `openclaw.json`**: a single `.conflict.<ts>` file alongside the live one (matching existing pattern), or interactive merge in the CLI? V1: file-based, document the manual reconciliation flow.

## 10. Acceptance for the V1 ship

- Documented in README under "Packs".
- `claude-sync init` prompts for which packs to enable.
- `claude-sync push --pack openclaw` works against a real R2 bucket.
- `claude-sync pull --pack openclaw` on a fresh machine restores the full agent topology (verified by `openclaw agents list` showing the same 5 agents).
- Memory store round-trip works (`openclaw memory status` shows the same indexed files + chunk count post-pull).
- Existing `claude-sync push/pull/status/diff/conflicts/reset/update` commands still work with no config change.
- All unit + integration tests green.
- 60% test coverage on `internal/` maintained (per existing CI gate in `CLAUDE.md`).
