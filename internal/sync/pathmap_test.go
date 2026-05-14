package sync

import (
	"testing"
)

func TestNewPathMapper(t *testing.T) {
	tests := []struct {
		name         string
		homeDir      string
		expectedSlug string
	}{
		{"macOS short", "/Users/merv", "-Users-merv"},
		{"macOS long", "/Users/mervynlally", "-Users-mervynlally"},
		{"linux", "/home/merv", "-home-merv"},
		{"trailing slash", "/Users/merv/", "-Users-merv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPathMapper(tt.homeDir)
			if pm.homeDirSlug != tt.expectedSlug {
				t.Errorf("homeDirSlug = %q, want %q", pm.homeDirSlug, tt.expectedSlug)
			}
		})
	}
}

func TestEncodePathSegment(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/Users/merv", "-Users-merv"},
		{"/Users/merv/nexura", "-Users-merv-nexura"},
		{"/Users/merv/.bifrost", "-Users-merv--bifrost"},
		{"/Users/mervynlally/.claude-worktrees/nexura/pedantic", "-Users-mervynlally--claude-worktrees-nexura-pedantic"},
		{"/home/user/.paperclip/instances", "-home-user--paperclip-instances"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := encodePathSegment(tt.input)
			if result != tt.expected {
				t.Errorf("encodePathSegment(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeRemoteKey(t *testing.T) {
	pm := NewPathMapper("/Users/merv")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple project",
			"projects/-Users-merv-nexura/memory/MEMORY.md",
			"projects/${HOME}-nexura/memory/MEMORY.md",
		},
		{
			"project root file",
			"projects/-Users-merv-nexura/session.jsonl",
			"projects/${HOME}-nexura/session.jsonl",
		},
		{
			"dot-dir project",
			"projects/-Users-merv--bifrost/file.txt",
			"projects/${HOME}--bifrost/file.txt",
		},
		{
			"bare project dir reference",
			"projects/-Users-merv-nexura",
			"projects/${HOME}-nexura",
		},
		{
			"non-project path unchanged",
			"CLAUDE.md",
			"CLAUDE.md",
		},
		{
			"settings unchanged",
			"settings.json",
			"settings.json",
		},
		{
			"agents dir unchanged",
			"agents/seo.md",
			"agents/seo.md",
		},
		{
			"different user project unchanged",
			"projects/-Users-alice-code/file.txt",
			"projects/-Users-alice-code/file.txt",
		},
		{
			"home-only project dir",
			"projects/-Users-merv",
			"projects/${HOME}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.NormalizeRemoteKey(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeRemoteKey(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeRemoteKey_PrefixCollision(t *testing.T) {
	// Critical test: /Users/merv must NOT match /Users/mervynlally
	pm := NewPathMapper("/Users/merv")

	input := "projects/-Users-mervynlally-nexura/memory/MEMORY.md"
	result := pm.NormalizeRemoteKey(input)
	if result != input {
		t.Errorf("NormalizeRemoteKey should not match longer username: got %q, want %q", result, input)
	}
}

func TestResolveLocalPath(t *testing.T) {
	pm := NewPathMapper("/Users/mervynlally")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple project",
			"projects/${HOME}-nexura/memory/MEMORY.md",
			"projects/-Users-mervynlally-nexura/memory/MEMORY.md",
		},
		{
			"dot-dir project",
			"projects/${HOME}--bifrost/file.txt",
			"projects/-Users-mervynlally--bifrost/file.txt",
		},
		{
			"bare project dir",
			"projects/${HOME}-nexura",
			"projects/-Users-mervynlally-nexura",
		},
		{
			"home-only project dir",
			"projects/${HOME}",
			"projects/-Users-mervynlally",
		},
		{
			"legacy key without HOME passes through",
			"projects/-Users-merv-nexura/file.txt",
			"projects/-Users-merv-nexura/file.txt",
		},
		{
			"non-project path passes through",
			"CLAUDE.md",
			"CLAUDE.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.ResolveLocalPath(tt.input)
			if result != tt.expected {
				t.Errorf("ResolveLocalPath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCrossDeviceRoundTrip(t *testing.T) {
	// Simulate: push from MacBook (/Users/merv), pull on Studio (/Users/mervynlally)
	macbook := NewPathMapper("/Users/merv")
	studio := NewPathMapper("/Users/mervynlally")

	localPaths := []struct {
		macbookLocal string
		studioLocal  string
	}{
		{
			"projects/-Users-merv-nexura/memory/MEMORY.md",
			"projects/-Users-mervynlally-nexura/memory/MEMORY.md",
		},
		{
			"projects/-Users-merv--bifrost/config.json",
			"projects/-Users-mervynlally--bifrost/config.json",
		},
		{
			"projects/-Users-merv-code-myapp/session.jsonl",
			"projects/-Users-mervynlally-code-myapp/session.jsonl",
		},
	}

	for _, tt := range localPaths {
		t.Run(tt.macbookLocal, func(t *testing.T) {
			// MacBook pushes: local → normalized
			normalized := macbook.NormalizeRemoteKey(tt.macbookLocal)
			if normalized == tt.macbookLocal {
				t.Fatalf("NormalizeRemoteKey did not normalize: %q", tt.macbookLocal)
			}

			// Studio pulls: normalized → studio local
			resolved := studio.ResolveLocalPath(normalized)
			if resolved != tt.studioLocal {
				t.Errorf("Cross-device: MacBook %q → normalized %q → Studio %q, want %q",
					tt.macbookLocal, normalized, resolved, tt.studioLocal)
			}

			// And the reverse: Studio pushes, MacBook pulls
			normalizedFromStudio := studio.NormalizeRemoteKey(tt.studioLocal)
			if normalizedFromStudio != normalized {
				t.Errorf("Both machines should produce same normalized key: MacBook %q, Studio %q",
					normalized, normalizedFromStudio)
			}

			resolvedOnMacbook := macbook.ResolveLocalPath(normalizedFromStudio)
			if resolvedOnMacbook != tt.macbookLocal {
				t.Errorf("Cross-device reverse: Studio %q → normalized %q → MacBook %q, want %q",
					tt.studioLocal, normalizedFromStudio, resolvedOnMacbook, tt.macbookLocal)
			}
		})
	}
}

func TestSameDeviceRoundTrip(t *testing.T) {
	pm := NewPathMapper("/Users/merv")

	paths := []string{
		"projects/-Users-merv-nexura/memory/MEMORY.md",
		"projects/-Users-merv--bifrost/file.txt",
		"projects/-Users-merv-code-app/session.jsonl",
		"CLAUDE.md",
		"settings.json",
		"agents/helper.md",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			normalized := pm.NormalizeRemoteKey(path)
			resolved := pm.ResolveLocalPath(normalized)
			if resolved != path {
				t.Errorf("Round-trip failed: %q → %q → %q", path, normalized, resolved)
			}
		})
	}
}

func TestNormalizeContent(t *testing.T) {
	pm := NewPathMapper("/Users/merv")

	input := []byte(`{"type":"tool_result","path":"/Users/merv/nexura/src/app.ts","content":"hello"}`)
	expected := []byte(`{"type":"tool_result","path":"${HOME}/nexura/src/app.ts","content":"hello"}`)

	result := pm.NormalizeContent(input)
	if string(result) != string(expected) {
		t.Errorf("NormalizeContent:\n  got:  %s\n  want: %s", result, expected)
	}
}

func TestResolveContent(t *testing.T) {
	pm := NewPathMapper("/Users/mervynlally")

	input := []byte(`{"type":"tool_result","path":"${HOME}/nexura/src/app.ts","content":"hello"}`)
	expected := []byte(`{"type":"tool_result","path":"/Users/mervynlally/nexura/src/app.ts","content":"hello"}`)

	result := pm.ResolveContent(input)
	if string(result) != string(expected) {
		t.Errorf("ResolveContent:\n  got:  %s\n  want: %s", result, expected)
	}
}

func TestContentCrossDevice(t *testing.T) {
	macbook := NewPathMapper("/Users/merv")
	studio := NewPathMapper("/Users/mervynlally")

	original := []byte(`Read file /Users/merv/nexura/src/app.ts and also /Users/merv/.config/settings`)
	expected := []byte(`Read file /Users/mervynlally/nexura/src/app.ts and also /Users/mervynlally/.config/settings`)

	normalized := macbook.NormalizeContent(original)
	resolved := studio.ResolveContent(normalized)

	if string(resolved) != string(expected) {
		t.Errorf("Content cross-device:\n  got:  %s\n  want: %s", resolved, expected)
	}
}

func TestIsProjectPath(t *testing.T) {
	pm := NewPathMapper("/Users/merv")

	tests := []struct {
		path     string
		expected bool
	}{
		{"projects/-Users-merv-nexura/file.txt", true},
		{"projects/${HOME}-nexura/file.txt", true},
		{"CLAUDE.md", false},
		{"settings.json", false},
		{"agents/helper.md", false},
		{"history.jsonl", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := pm.IsProjectPath(tt.path)
			if result != tt.expected {
				t.Errorf("IsProjectPath(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestReplaceSlugPrefix_BoundaryCheck(t *testing.T) {
	pm := NewPathMapper("/Users/merv")

	tests := []struct {
		name      string
		slug      string
		oldPrefix string
		newPrefix string
		expected  string
	}{
		{
			"exact match with remainder",
			"-Users-merv-nexura",
			"-Users-merv",
			"${HOME}",
			"${HOME}-nexura",
		},
		{
			"exact match, no remainder",
			"-Users-merv",
			"-Users-merv",
			"${HOME}",
			"${HOME}",
		},
		{
			"prefix of longer token - should NOT match",
			"-Users-mervynlally-nexura",
			"-Users-merv",
			"${HOME}",
			"-Users-mervynlally-nexura",
		},
		{
			"dot-dir boundary",
			"-Users-merv--bifrost",
			"-Users-merv",
			"${HOME}",
			"${HOME}--bifrost",
		},
		{
			"no match at all",
			"-Users-alice-code",
			"-Users-merv",
			"${HOME}",
			"-Users-alice-code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.replaceSlugPrefix(tt.slug, tt.oldPrefix, tt.newPrefix)
			if result != tt.expected {
				t.Errorf("replaceSlugPrefix(%q, %q, %q) = %q, want %q",
					tt.slug, tt.oldPrefix, tt.newPrefix, result, tt.expected)
			}
		})
	}
}

func TestLinuxPaths(t *testing.T) {
	pm := NewPathMapper("/home/deploy")

	input := "projects/-home-deploy-app/memory/MEMORY.md"
	normalized := pm.NormalizeRemoteKey(input)
	expected := "projects/${HOME}-app/memory/MEMORY.md"

	if normalized != expected {
		t.Errorf("Linux normalize: got %q, want %q", normalized, expected)
	}

	// Resolve on a different Linux machine
	pm2 := NewPathMapper("/home/ubuntu")
	resolved := pm2.ResolveLocalPath(normalized)
	expectedResolved := "projects/-home-ubuntu-app/memory/MEMORY.md"
	if resolved != expectedResolved {
		t.Errorf("Linux resolve: got %q, want %q", resolved, expectedResolved)
	}
}

func TestEmptyHomeDir(t *testing.T) {
	pm := NewPathMapper("")

	// Should pass through everything unchanged
	input := "projects/-Users-merv-nexura/file.txt"
	if result := pm.NormalizeRemoteKey(input); result != input {
		t.Errorf("Empty homeDir NormalizeRemoteKey should pass through: got %q", result)
	}

	data := []byte("path /Users/merv/file")
	if result := pm.NormalizeContent(data); string(result) != string(data) {
		t.Errorf("Empty homeDir NormalizeContent should pass through")
	}
	if result := pm.ResolveContent(data); string(result) != string(data) {
		t.Errorf("Empty homeDir ResolveContent should pass through")
	}
}
