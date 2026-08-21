package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// sessionsDirName is the directory the desktop app keeps its sidebar index in,
// below the app's Electron userData directory.
const sessionsDirName = "claude-code-sessions"

// ErrNoHomeDir is returned when the user's home directory cannot be determined.
var ErrNoHomeDir = errors.New("could not determine home directory")

// sessionsRoot resolves the index root for a platform.
//
// The darwin layout is confirmed against a real installation. The windows and
// linux layouts follow Electron's userData convention, which this app is built
// on, but have not been verified on those platforms.
func sessionsRoot(goos, home, appData string) (string, error) {
	if home == "" && appData == "" {
		return "", ErrNoHomeDir
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", sessionsDirName), nil
	case "windows":
		base := appData
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Claude", sessionsDirName), nil
	default:
		return filepath.Join(home, ".config", "Claude", sessionsDirName), nil
	}
}

// SessionsRoot returns the desktop session index root for this machine.
func SessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ErrNoHomeDir
	}
	return sessionsRoot(runtime.GOOS, home, os.Getenv("APPDATA"))
}
