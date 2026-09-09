package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ForgetCCDSessionResult describes what a forget operation removed.
type ForgetCCDSessionResult struct {
	RemovedRemoteKeys []string
	RemovedLocalPaths []string
}

// ccdRecordMatches reports whether a session record's own sessionId or
// cliSessionId equals id.
func ccdRecordMatches(data []byte, id string) bool {
	var m struct {
		SessionID    string `json:"sessionId"`
		CLISessionID string `json:"cliSessionId"`
	}
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	return m.SessionID == id || m.CLISessionID == id
}

// ForgetCCDSession permanently removes every Desktop session pointer record
// matching id — either a record's own sessionId (e.g. "local_<uuid>") or its
// cliSessionId — both locally (this machine's session store) and from
// remote. id must match exactly; there is no partial or fuzzy matching, so a
// caller can't accidentally forget more than one unrelated record.
//
// CCD records are deliberately "only grows" (see pushCCDSessions /
// pullCCDSessions): a normal push or pull never deletes one just because a
// local copy disappeared, precisely so a session doesn't vanish from a
// device's sidebar just because another device's file briefly went missing.
// That means a genuinely broken record — an orphaned fork with no
// cliSessionId, a duplicate left over from before a project moved, or
// anything else you actually want gone — has no way to leave the bucket on
// its own. This is the explicit, opt-in escape hatch for that. It does not
// touch the underlying conversation transcript under ~/.claude/projects,
// only the Desktop-visible pointer; the transcript has its own deletion path
// (delete the local file, then push).
func (s *Syncer) ForgetCCDSession(ctx context.Context, id string) (*ForgetCCDSessionResult, error) {
	result := &ForgetCCDSessionResult{}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("session id required")
	}

	// Remote: list every record under the prefix and match by content, since
	// the id a caller has (sessionId or cliSessionId) isn't derivable from
	// the remote key alone (the key is org-id/filename).
	objects, err := s.storage.List(ctx, CCDSessionsPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list desktop session records: %w", err)
	}
	var toDelete []string
	for _, obj := range objects {
		data, err := s.fetchDecoded(ctx, obj.Key, obj.Key)
		if err != nil {
			// A record this device can't decrypt/decompress can't be
			// matched; skip rather than fail the whole operation over one
			// unrelated bad record.
			continue
		}
		if ccdRecordMatches(data, id) {
			toDelete = append(toDelete, obj.Key)
		}
	}
	if len(toDelete) > 0 {
		if err := s.storage.DeleteBatch(ctx, toDelete); err != nil {
			return nil, fmt.Errorf("failed to delete desktop session records: %w", err)
		}
		for _, key := range toDelete {
			s.state.RemoveFile(key)
		}
		result.RemovedRemoteKeys = toDelete
	}

	// Local: every install/org directory in this machine's own store.
	// ccdLocalRecords collapses duplicates across install dirs (reinstalls)
	// to the LWW winner, so a stale loser in another install dir is not
	// covered here — a rare case, left for a future pass if it matters.
	if ccdDir := s.ccdSessionsDir(); ccdDir != "" {
		if records, err := ccdLocalRecords(ccdDir); err == nil {
			for _, path := range records {
				raw, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				if ccdRecordMatches(raw, id) {
					if err := os.Remove(path); err == nil {
						result.RemovedLocalPaths = append(result.RemovedLocalPaths, path)
					}
				}
			}
		}
	}

	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}

	return result, nil
}
