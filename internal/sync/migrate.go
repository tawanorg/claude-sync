package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateResult describes the outcome of migrating legacy remote keys to
// portable (path-normalized) keys.
type MigrateResult struct {
	Migrated []string // local paths re-uploaded under normalized keys
	Foreign  []string // legacy keys owned by another device (run migrate there)
	Errors   []error
}

// MigratePaths rewrites this device's legacy remote project keys to the
// portable token form: each file is re-uploaded under its normalized key
// (with content normalization applied) and the legacy key is deleted.
//
// Keys that don't match any of this device's path mappings — typically
// projects pushed from another machine — are left untouched and reported in
// Foreign; running migrate on that machine completes the migration.
func (s *Syncer) MigratePaths(ctx context.Context) (*MigrateResult, error) {
	result := &MigrateResult{}

	remoteObjects, err := s.storage.List(ctx, "projects/")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	var legacyKeys []string
	for _, obj := range remoteObjects {
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		raw := strings.TrimSuffix(obj.Key, ".age")
		if seg, _, ok := splitProjectsPath(raw); !ok || strings.HasPrefix(seg, tokenPrefix) {
			continue // already normalized
		}
		if s.isExcluded(raw) {
			continue
		}
		normalized := s.paths.NormalizeRelPath(raw)
		if normalized == raw {
			// Not under any of this device's mapped prefixes
			result.Foreign = append(result.Foreign, raw)
			continue
		}
		if _, err := os.Stat(filepath.Join(s.claudeDir, raw)); err != nil {
			// We can't re-encrypt content we don't have locally
			result.Foreign = append(result.Foreign, raw)
			continue
		}
		legacyKeys = append(legacyKeys, raw)
	}

	total := len(legacyKeys)
	for i, raw := range legacyKeys {
		s.progress(ProgressEvent{Action: "upload", Path: raw, Current: i + 1, Total: total})
		if err := s.uploadFile(ctx, raw); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", raw, err))
			continue
		}
		if err := s.storage.Delete(ctx, raw+".age"); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("delete legacy %s: %w", raw, err))
			continue
		}
		result.Migrated = append(result.Migrated, raw)
	}

	if total > 0 {
		if err := s.state.Save(); err != nil {
			return result, fmt.Errorf("failed to save state: %w", err)
		}
	}

	return result, nil
}
