// Package desktop reads and writes the Claude desktop app's session index.
//
// The desktop app does not render its sidebar from ~/.claude/projects. It keeps
// a separate index of small pointer records at
// claude-code-sessions/<account>/<workspace>/local_<uuid>.json, each naming a
// transcript through its cliSessionId field. Syncing ~/.claude alone therefore
// restores conversations for `claude --resume` while leaving the sidebar empty.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoIndex reports that no desktop session index exists. A machine that has
// never opened the desktop app is an ordinary skip, not a sync failure.
var ErrNoIndex = errors.New("no desktop session index found")

// ErrAmbiguousIndex reports that several <account>/<workspace> leaves exist and
// none can be chosen safely.
var ErrAmbiguousIndex = errors.New("multiple desktop session index directories")

// IndexDir returns the <account>/<workspace> leaf directory holding the index
// records under root.
func IndexDir(root string) (string, error) {
	var found []string

	accounts, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w under %s", ErrNoIndex, root)
		}
		return "", err
	}
	for _, account := range accounts {
		if !account.IsDir() {
			continue
		}
		workspaces, err := os.ReadDir(filepath.Join(root, account.Name()))
		if err != nil {
			continue
		}
		for _, workspace := range workspaces {
			if !workspace.IsDir() {
				continue
			}
			found = append(found, filepath.Join(root, account.Name(), workspace.Name()))
		}
	}

	sort.Strings(found)
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%w under %s", ErrNoIndex, root)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%w under %s: %s", ErrAmbiguousIndex, root, strings.Join(found, ", "))
	}
}
