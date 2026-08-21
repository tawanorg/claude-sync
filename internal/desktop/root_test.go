package desktop

import (
	"path/filepath"
	"testing"
)

// Only the darwin layout has been observed on a real machine. The windows and
// linux layouts follow Electron's userData convention, which the desktop app
// uses, but they are inferred rather than verified.
func TestSessionsRootPerPlatform(t *testing.T) {
	const home = "/home/u"
	cases := []struct {
		goos    string
		appData string
		want    string
	}{
		{"darwin", "", filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions")},
		{"linux", "", filepath.Join(home, ".config", "Claude", "claude-code-sessions")},
		{"windows", `C:\Users\u\AppData\Roaming`, filepath.Join(`C:\Users\u\AppData\Roaming`, "Claude", "claude-code-sessions")},
	}

	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			got, err := sessionsRoot(tc.goos, home, tc.appData)
			if err != nil {
				t.Fatalf("sessionsRoot() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("sessionsRoot(%q) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}

// APPDATA is the authoritative location on Windows; falling back to a guessed
// AppData\Roaming path is only correct when the variable is unset.
func TestSessionsRootFallsBackWhenAppDataUnset(t *testing.T) {
	got, err := sessionsRoot("windows", "/home/u", "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/u", "AppData", "Roaming", "Claude", "claude-code-sessions")
	if got != want {
		t.Errorf("sessionsRoot() = %q, want %q", got, want)
	}
}
