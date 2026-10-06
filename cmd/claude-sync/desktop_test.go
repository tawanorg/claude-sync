package main

import (
	"sort"
	"testing"
)

// Sidebar hydration is wired inside `pull`, so exercising it otherwise means
// running a full sync — downloading conversations just to fix a sidebar. MCP
// solved the same problem with standalone subcommands; desktop mirrors that so
// the index can be pushed or hydrated on its own.
func TestDesktopCmdExposesPushAndPull(t *testing.T) {
	cmd := desktopCmd()

	if cmd.Use != "desktop" {
		t.Errorf("Use = %q, want %q", cmd.Use, "desktop")
	}

	found := map[string]bool{}
	for _, sub := range cmd.Commands() {
		found[sub.Name()] = true
	}
	for _, want := range []string{"push", "pull"} {
		if !found[want] {
			names := make([]string, 0, len(found))
			for n := range found {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Errorf("desktop has no %q subcommand; got %v", want, names)
		}
	}
}

// Every subcommand needs a one-line Short, or it renders blank in help output.
func TestDesktopSubcommandsAreDocumented(t *testing.T) {
	for _, sub := range desktopCmd().Commands() {
		if sub.Short == "" {
			t.Errorf("subcommand %q has no Short description", sub.Name())
		}
	}
}
