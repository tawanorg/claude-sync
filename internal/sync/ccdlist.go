package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// CCDSessionSummary is the subset of a Desktop session record useful for
// deciding what to pass to ForgetCCDSession.
type CCDSessionSummary struct {
	RemoteKey      string
	SessionID      string
	CLISessionID   string
	Title          string
	CWD            string
	IsArchived     bool
	LastActivityAt int64 // epoch milliseconds, as Desktop writes it; 0 if absent
}

// ListCCDSessions returns every Desktop session pointer record on remote
// storage, decrypted and decoded, newest lastActivityAt first. This is the
// remote (account-wide, every device) view rather than just this machine's
// local session store, since that's what ForgetCCDSession's id argument
// ultimately has to match on remote to actually remove anything.
func (s *Syncer) ListCCDSessions(ctx context.Context) ([]CCDSessionSummary, error) {
	objects, err := s.storage.List(ctx, CCDSessionsPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list desktop session records: %w", err)
	}

	var out []CCDSessionSummary
	for _, obj := range objects {
		data, err := s.fetchDecoded(ctx, obj.Key, obj.Key)
		if err != nil {
			// A record this device can't decrypt/decompress still exists;
			// surface it as best-effort rather than silently dropping it.
			out = append(out, CCDSessionSummary{RemoteKey: obj.Key, Title: "(unreadable record)"})
			continue
		}
		var rec struct {
			SessionID      string `json:"sessionId"`
			CLISessionID   string `json:"cliSessionId"`
			Title          string `json:"title"`
			CWD            string `json:"cwd"`
			IsArchived     bool   `json:"isArchived"`
			LastActivityAt int64  `json:"lastActivityAt"`
		}
		if json.Unmarshal(data, &rec) != nil {
			out = append(out, CCDSessionSummary{RemoteKey: obj.Key, Title: "(invalid record)"})
			continue
		}
		// fetchDecoded doesn't resolve ${HOME} tokens for _ccd-sessions/ keys
		// (IsPortableContentPath is false for that prefix by design — push/
		// pull need the raw token form to compare records consistently, see
		// pushCCDSessions/pullCCDSessions). For a listing meant to be read by
		// a person, the actual local path is what's useful; resolve it here
		// rather than showing "${HOME}\claude\blink-re" verbatim.
		//
		// jsonMode=false: rec.CWD is already a decoded Go string (json.Unmarshal
		// above stripped any JSON escaping), not raw serialized JSON bytes, so
		// the substituted path must go in raw — jsonMode=true here would
		// JSON-escape it a second time (e.g. "C:\\Users\\austi", doubled).
		cwd := string(s.paths.ResolveContent([]byte(rec.CWD), false))
		out = append(out, CCDSessionSummary{
			RemoteKey:      obj.Key,
			SessionID:      rec.SessionID,
			CLISessionID:   rec.CLISessionID,
			Title:          rec.Title,
			CWD:            cwd,
			IsArchived:     rec.IsArchived,
			LastActivityAt: rec.LastActivityAt,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].LastActivityAt > out[j].LastActivityAt })
	return out, nil
}
