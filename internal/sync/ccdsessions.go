package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tawanorg/claude-sync/internal/storage"
)

// Claude Code Desktop session-record sync.
//
// The desktop app renders its sidebar from per-machine session records:
// %APPDATA%\Claude\claude-code-sessions\<install-id>\<org-id>\local_*.json.
// Each record carries the title, cwd (project grouping), archive state, and a
// cliSessionId pointing at the transcript in ~/.claude/projects — which this
// tool already syncs. Syncing the records gives every machine's sidebar the
// same conversation list, backed by the transcripts the regular sync delivers.
//
// Remote layout: _ccd-sessions/<org-id>/local_<uuid>.json (encrypted+gzip like
// everything else). The install-id segment is machine-specific and is
// deliberately NOT part of the remote key: on pull, records land under the
// pulling machine's own install directory. Records merge last-writer-wins by
// the record's own lastActivityAt field; remote records are never deleted when
// local ones disappear (same only-grows policy as history.jsonl).
//
// Trust state is intentionally out of scope: it lives in ~/.claude.json and
// gates execution of project hooks/CLAUDE.md — syncing it would let a
// compromised bucket pre-trust projects on every machine.

// CCDSessionsPrefix is the reserved remote prefix for desktop session records.
const CCDSessionsPrefix = "_ccd-sessions/"

// SetCCDSessionsDir overrides the desktop-app session store location
// (default: auto-detected next to the claude dir; tests inject a temp dir).
func (s *Syncer) SetCCDSessionsDir(dir string) { s.ccdDir = dir }

// ccdSessionsDir returns the desktop-app session store, or "" when this
// machine has none (feature silently disabled).
//
// Two real layouts exist. Direct installs put the store at
// <profile>/AppData/Roaming/Claude/claude-code-sessions. MSIX-packaged
// installs make that Roaming path a redirect into the package's private
// store — a redirect WSL's drvfs cannot traverse (os.Stat fails) — with the
// physical directory at
// <profile>/AppData/Local/Packages/Claude_*/LocalCache/Roaming/Claude/....
// Both are probed; the glob covers the publisher-hash suffix.
func (s *Syncer) ccdSessionsDir() string {
	if s.ccdDir != "" {
		return s.ccdDir
	}
	profile := filepath.Dir(s.claudeDir)

	direct := filepath.Join(profile, "AppData", "Roaming", "Claude", "claude-code-sessions")
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		return direct
	}

	matches, _ := filepath.Glob(filepath.Join(profile, "AppData", "Local", "Packages",
		"Claude_*", "LocalCache", "Roaming", "Claude", "claude-code-sessions"))
	// Prefer a store that actually holds records: a hostile or stale package
	// dir matching the glob must not shadow the real one. A single empty
	// match is still accepted (fresh install, records arrive via pull).
	first := ""
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			continue
		}
		if first == "" {
			first = m
		}
		if recs, err := ccdLocalRecords(m); err == nil && len(recs) > 0 {
			return m
		}
	}
	return first
}

// ccdSafeSegment allows only the flat charset used by real org ids
// (UUIDs) and record filenames (local_<uuid>.json): no separators, no dots
// except the .json suffix's, no way to spell "..".
func ccdSafeSegment(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 128 {
		return false
	}
	dots := 0
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return dots <= 1
}

// ccdRecordMeta is the subset of a session record needed for merging.
type ccdRecordMeta struct {
	LastActivityAt int64 `json:"lastActivityAt"`
}

func ccdLastActivity(data []byte) int64 {
	var m ccdRecordMeta
	if json.Unmarshal(data, &m) != nil {
		return 0
	}
	return m.LastActivityAt
}

// ccdNewerThan reports whether record a should win over record b: higher
// lastActivityAt wins; on a tie with different bytes, the higher hash wins on
// every machine, so ties converge instead of ping-ponging forever.
func ccdNewerThan(a, b []byte) bool {
	la, lb := ccdLastActivity(a), ccdLastActivity(b)
	if la != lb {
		return la > lb
	}
	return HashBytes(a) > HashBytes(b)
}

// ccdStripLocalFields removes permission-adjacent fields from a record before
// it crosses machines: permissionMode and enabledMcpTools are this machine's
// permission decisions, and syncing them would let a crafted record pre-open
// tool access on every machine — the same reasoning that keeps trust state
// local. The rewrite is deterministic (sorted keys) so both sides normalize
// to identical bytes.
func ccdStripLocalFields(data []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	delete(m, "permissionMode")
	delete(m, "enabledMcpTools")
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}

// ccdWriteAtomic writes via temp+rename: the desktop app is always running
// during hook-driven syncs and reads these files — it must never observe a
// half-written record.
func ccdWriteAtomic(dest string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".ccd-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// ccdLocalRecords maps remote key -> local file path for every session
// record in every install directory of the store.
func ccdLocalRecords(ccdDir string) (map[string]string, error) {
	records := make(map[string]string)
	installs, err := os.ReadDir(ccdDir)
	if err != nil {
		return nil, err
	}
	for _, inst := range installs {
		if !inst.IsDir() {
			continue
		}
		orgs, err := os.ReadDir(filepath.Join(ccdDir, inst.Name()))
		if err != nil {
			continue
		}
		for _, org := range orgs {
			if !org.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(ccdDir, inst.Name(), org.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasPrefix(f.Name(), "local_") || !strings.HasSuffix(f.Name(), ".json") {
					continue
				}
				key := CCDSessionsPrefix + org.Name() + "/" + f.Name()
				path := filepath.Join(ccdDir, inst.Name(), org.Name(), f.Name())
				// Multiple install dirs (reinstalls) can hold the same record;
				// keep the one that wins LWW, not the alphabetically last dir.
				if prev, ok := records[key]; ok {
					prevData, _ := os.ReadFile(prev)
					curData, _ := os.ReadFile(path)
					if !ccdNewerThan(curData, prevData) {
						continue
					}
				}
				records[key] = path
			}
		}
	}
	return records, nil
}

// ccdInstallDir picks the install directory pulled records are written into:
// the most recently modified one (the active install). Empty when none exist.
func ccdInstallDir(ccdDir string) string {
	installs, err := os.ReadDir(ccdDir)
	if err != nil {
		return ""
	}
	type cand struct {
		name string
		mod  int64
	}
	var cands []cand
	for _, inst := range installs {
		if !inst.IsDir() {
			continue
		}
		info, err := inst.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{inst.Name(), info.ModTime().UnixNano()})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod > cands[j].mod })
	return filepath.Join(ccdDir, cands[0].name)
}

// pushCCDSessions uploads changed session records, letting a newer remote
// copy win. Errors are collected per record; one bad record never blocks the
// rest.
func (s *Syncer) pushCCDSessions(ctx context.Context, result *SyncResult) {
	ccdDir := s.ccdSessionsDir()
	if ccdDir == "" {
		return
	}
	records, err := ccdLocalRecords(ccdDir)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("ccd sessions: %w", err))
		return
	}

	for key, path := range records {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// A torn read (the app writes concurrently) must never become the
		// bucket copy; skip and let the next sync retry a settled file.
		if !json.Valid(raw) {
			continue
		}
		local := ccdStripLocalFields(raw)
		hash := HashBytes(local)
		if st := s.state.GetFile(key); st != nil && st.Hash == hash {
			continue
		}

		// Changed or new: only upload over a losing remote copy.
		if _, err := s.storage.Head(ctx, key); err == nil {
			remote, err := s.fetchDecoded(ctx, key, key)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", key, err))
				continue
			}
			if !ccdNewerThan(local, remote) && HashBytes(remote) != hash {
				// Remote wins; pull will adopt it. Leave state stale.
				continue
			}
		} else if !storage.IsNotFound(err) {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", key, err))
			continue
		}

		if err := s.uploadEncoded(ctx, key, local); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", key, err))
			continue
		}
		info, err := os.Stat(path)
		if err == nil {
			s.state.UpdateFile(key, info, hash)
			s.state.MarkUploaded(key)
		}
		result.Uploaded = append(result.Uploaded, key)
	}
}

// pullCCDSessions materializes newer remote records into this machine's own
// install directory.
func (s *Syncer) pullCCDSessions(ctx context.Context, result *SyncResult) {
	ccdDir := s.ccdSessionsDir()
	if ccdDir == "" {
		return
	}
	installDir := ccdInstallDir(ccdDir)
	if installDir == "" {
		return
	}

	objects, err := s.storage.List(ctx, CCDSessionsPrefix)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("ccd sessions: %w", err))
		return
	}

	for _, obj := range objects {
		rel := strings.TrimPrefix(obj.Key, CCDSessionsPrefix)
		parts := strings.Split(rel, "/")
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "local_") || !strings.HasSuffix(parts[1], ".json") {
			continue
		}
		org, name := parts[0], parts[1]
		// The bucket is treated as hostile: both segments become filepath.Join
		// components, so restrict them to the flat charset real org ids and
		// record names use — this forbids "..", backslashes, and anything
		// path-like.
		if !ccdSafeSegment(org) || !ccdSafeSegment(name) {
			continue
		}

		if st := s.state.GetFile(obj.Key); st != nil && !obj.LastModified.After(st.Uploaded) {
			continue
		}

		remote, err := s.fetchDecoded(ctx, obj.Key, obj.Key)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", obj.Key, err))
			continue
		}
		// Never materialize invalid JSON into the app's live store, and strip
		// permission-adjacent fields a hostile or older-binary record carries.
		if !json.Valid(remote) {
			continue
		}
		remote = ccdStripLocalFields(remote)

		dest := filepath.Join(installDir, org, name)
		if raw, err := os.ReadFile(dest); err == nil {
			local := ccdStripLocalFields(raw)
			if HashBytes(local) == HashBytes(remote) || !ccdNewerThan(remote, local) {
				// Local wins or is identical; push publishes when needed.
				continue
			}
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", obj.Key, err))
			continue
		}
		if err := ccdWriteAtomic(dest, remote); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", obj.Key, err))
			continue
		}
		info, err := os.Stat(dest)
		if err == nil {
			s.state.UpdateFile(obj.Key, info, HashBytes(remote))
			s.state.MarkUploaded(obj.Key)
		}
		result.Downloaded = append(result.Downloaded, obj.Key)
	}
}
