package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	// Test missing file returns nil
	servers, err := ReadMCPServers(path)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if servers != nil {
		t.Fatal("expected nil servers for missing file")
	}

	// Test file with mcpServers
	content := `{
  "numStartups": 5,
  "mcpServers": {
    "my-server": {"command": "node", "args": ["server.js"]},
    "other-server": {"command": "python", "args": ["-m", "mcp"]}
  },
  "otherKey": true
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	servers, err = ReadMCPServers(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if _, ok := servers["my-server"]; !ok {
		t.Fatal("missing 'my-server' key")
	}
	if _, ok := servers["other-server"]; !ok {
		t.Fatal("missing 'other-server' key")
	}
}

func TestReadMCPServers_NoMCPKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	content := `{"numStartups": 5, "otherKey": true}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	servers, err := ReadMCPServers(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if servers != nil {
		t.Fatal("expected nil servers when no mcpServers key")
	}
}

func TestWriteMCPServers_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	original := `{
  "numStartups": 5,
  "mcpServers": {
    "old-server": {"command": "old"}
  },
  "otherKey": true
}`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	newServers := MCPServers{
		"new-server": json.RawMessage(`{"command": "new"}`),
	}

	if err := WriteMCPServers(path, newServers); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify backup was created
	bakData, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	if string(bakData) != original {
		t.Fatal("backup content doesn't match original")
	}

	// Read back and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse written file: %v", err)
	}

	// Verify other keys preserved
	if _, ok := raw["numStartups"]; !ok {
		t.Fatal("numStartups key was not preserved")
	}
	if _, ok := raw["otherKey"]; !ok {
		t.Fatal("otherKey was not preserved")
	}

	// Verify mcpServers updated
	var servers MCPServers
	if err := json.Unmarshal(raw["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["new-server"]; !ok {
		t.Fatal("new-server not found in written file")
	}
	if _, ok := servers["old-server"]; ok {
		t.Fatal("old-server should have been replaced")
	}
}

func TestWriteMCPServers_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	servers := MCPServers{
		"test-server": json.RawMessage(`{"command": "test"}`),
	}

	if err := WriteMCPServers(path, servers); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	readBack, err := ReadMCPServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(readBack) != 1 {
		t.Fatalf("expected 1 server, got %d", len(readBack))
	}
}

func TestNormalizeMCPPaths(t *testing.T) {
	input := []byte(`{"command":"/Users/alice/.nvm/versions/node/v20/bin/node","args":["/Users/alice/projects/server.js"],"env":{"HOME":"/Users/alice"}}`)
	result := NormalizeMCPPaths(input, "/Users/alice")
	expected := `{"command":"${HOME}/.nvm/versions/node/v20/bin/node","args":["${HOME}/projects/server.js"],"env":{"HOME":"${HOME}"}}`

	if string(result) != expected {
		t.Fatalf("normalization failed.\nGot:    %s\nExpect: %s", result, expected)
	}
}

func TestResolveMCPPaths(t *testing.T) {
	input := []byte(`{"command":"${HOME}/.nvm/versions/node/v20/bin/node","args":["${HOME}/projects/server.js"]}`)
	result := ResolveMCPPaths(input, "/Users/bob")
	expected := `{"command":"/Users/bob/.nvm/versions/node/v20/bin/node","args":["/Users/bob/projects/server.js"]}`

	if string(result) != expected {
		t.Fatalf("resolution failed.\nGot:    %s\nExpect: %s", result, expected)
	}
}

func TestNormalizeResolveRoundTrip(t *testing.T) {
	original := MCPServers{
		"server": json.RawMessage(`{"command":"/Users/alice/bin/node","args":["/Users/alice/app/server.js"]}`),
	}

	normalized, err := NormalizeMCPServers(original, "/Users/alice")
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveMCPServers(normalized, "/Users/alice")
	if err != nil {
		t.Fatal(err)
	}

	// Compare server configs
	origJSON, _ := json.Marshal(original["server"])
	resolvedJSON, _ := json.Marshal(resolved["server"])
	if !mcpServerEqual(origJSON, resolvedJSON) {
		t.Fatalf("round-trip failed.\nOriginal: %s\nResolved: %s", origJSON, resolvedJSON)
	}
}

func TestNormalizeMCPPaths_EmptyHomeDir(t *testing.T) {
	input := []byte(`{"command":"/usr/bin/node"}`)
	result := NormalizeMCPPaths(input, "")
	if string(result) != string(input) {
		t.Fatal("empty homeDir should return input unchanged")
	}
}

func TestMergeMCPServers_AddFromRemote(t *testing.T) {
	local := MCPServers{}
	remote := MCPServers{
		"new-server": json.RawMessage(`{"command":"node"}`),
	}
	baseline := MCPServers{}

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Added) != 1 || result.Added[0] != "new-server" {
		t.Fatalf("expected new-server to be added, got added=%v", result.Added)
	}
	if _, ok := result.Merged["new-server"]; !ok {
		t.Fatal("new-server not in merged result")
	}
}

func TestMergeMCPServers_KeepLocalOnly(t *testing.T) {
	local := MCPServers{
		"local-only": json.RawMessage(`{"command":"python"}`),
	}
	remote := MCPServers{}
	baseline := MCPServers{}

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Kept) != 1 || result.Kept[0] != "local-only" {
		t.Fatalf("expected local-only to be kept, got kept=%v", result.Kept)
	}
	if _, ok := result.Merged["local-only"]; !ok {
		t.Fatal("local-only not in merged result")
	}
}

func TestMergeMCPServers_UpdateFromRemote(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node","args":["v1"]}`)
	serverV2 := json.RawMessage(`{"command":"node","args":["v2"]}`)

	local := MCPServers{"s": serverV1}
	remote := MCPServers{"s": serverV2}
	baseline := MCPServers{"s": serverV1} // local matches baseline

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Updated) != 1 || result.Updated[0] != "s" {
		t.Fatalf("expected s to be updated, got updated=%v", result.Updated)
	}
	// Merged should have v2
	if !mcpServerEqual(result.Merged["s"], serverV2) {
		t.Fatalf("merged should have remote version, got: %s", result.Merged["s"])
	}
}

func TestMergeMCPServers_KeepLocalChanged(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node","args":["v1"]}`)
	serverV2 := json.RawMessage(`{"command":"node","args":["v2"]}`)

	local := MCPServers{"s": serverV2}    // local changed
	remote := MCPServers{"s": serverV1}   // remote unchanged
	baseline := MCPServers{"s": serverV1} // matches remote

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Kept) != 1 {
		t.Fatalf("expected s to be kept (local changed), got kept=%v, updated=%v", result.Kept, result.Updated)
	}
	if !mcpServerEqual(result.Merged["s"], serverV2) {
		t.Fatal("merged should have local version")
	}
}

func TestMergeMCPServers_ConflictBothChanged(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node","args":["v1"]}`)
	serverV2 := json.RawMessage(`{"command":"node","args":["v2"]}`)
	serverV3 := json.RawMessage(`{"command":"node","args":["v3"]}`)

	local := MCPServers{"s": serverV2}
	remote := MCPServers{"s": serverV3}
	baseline := MCPServers{"s": serverV1}

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(result.Conflicts))
	}
	if result.Conflicts[0].Key != "s" {
		t.Fatalf("expected conflict on 's', got '%s'", result.Conflicts[0].Key)
	}
	// Local version should be kept in merged
	if !mcpServerEqual(result.Merged["s"], serverV2) {
		t.Fatal("merged should keep local version on conflict")
	}
}

func TestMergeMCPServers_BothChangedIdentically(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node","args":["v1"]}`)
	serverV2 := json.RawMessage(`{"command":"node","args":["v2"]}`)

	local := MCPServers{"s": serverV2}
	remote := MCPServers{"s": serverV2}
	baseline := MCPServers{"s": serverV1}

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Conflicts) != 0 {
		t.Fatal("expected no conflicts when both changed identically")
	}
	if len(result.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(result.Kept))
	}
}

func TestMergeMCPServers_RemoteDeletion(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node"}`)

	local := MCPServers{"s": serverV1}
	remote := MCPServers{}
	baseline := MCPServers{"s": serverV1} // matches local

	result := MergeMCPServers(local, remote, baseline)

	// Local unchanged, remote deleted -> honor deletion
	if _, ok := result.Merged["s"]; ok {
		t.Fatal("server should have been removed (remote deletion)")
	}
}

func TestMergeMCPServers_LocalDeletion(t *testing.T) {
	serverV1 := json.RawMessage(`{"command":"node"}`)

	local := MCPServers{}
	remote := MCPServers{"s": serverV1}
	baseline := MCPServers{"s": serverV1} // matches remote

	result := MergeMCPServers(local, remote, baseline)

	// Remote unchanged, locally deleted -> honor deletion
	if _, ok := result.Merged["s"]; ok {
		t.Fatal("server should have been removed (local deletion)")
	}
}

func TestMergeMCPServers_NoBaseline_Conflict(t *testing.T) {
	local := MCPServers{
		"s": json.RawMessage(`{"command":"local"}`),
	}
	remote := MCPServers{
		"s": json.RawMessage(`{"command":"remote"}`),
	}
	baseline := MCPServers{} // no baseline

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict with no baseline, got %d", len(result.Conflicts))
	}
}

func TestMergeMCPServers_NoBaseline_SameValue(t *testing.T) {
	server := json.RawMessage(`{"command":"same"}`)

	local := MCPServers{"s": server}
	remote := MCPServers{"s": server}
	baseline := MCPServers{}

	result := MergeMCPServers(local, remote, baseline)

	if len(result.Conflicts) != 0 {
		t.Fatal("expected no conflicts when both have same value")
	}
	if len(result.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(result.Kept))
	}
}

func TestHashMCPServers(t *testing.T) {
	servers := MCPServers{
		"a": json.RawMessage(`{"command":"node"}`),
	}

	hash1, err := HashMCPServers(servers)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == "" {
		t.Fatal("hash should not be empty")
	}

	// Same content should produce same hash
	hash2, err := HashMCPServers(servers)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 {
		t.Fatal("same content should produce same hash")
	}

	// Different content should produce different hash
	servers2 := MCPServers{
		"a": json.RawMessage(`{"command":"python"}`),
	}
	hash3, err := HashMCPServers(servers2)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == hash3 {
		t.Fatal("different content should produce different hash")
	}
}

func TestHashMCPServers_Nil(t *testing.T) {
	hash, err := HashMCPServers(nil)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Fatal("nil servers should produce empty hash")
	}
}

func TestMCPServerEqual(t *testing.T) {
	// Same content, different formatting
	a := json.RawMessage(`{"command":"node","args":["a"]}`)
	b := json.RawMessage(`{"command":"node",  "args": ["a"]}`)
	if !mcpServerEqual(a, b) {
		t.Fatal("same content with different whitespace should be equal")
	}

	// Different content
	c := json.RawMessage(`{"command":"python"}`)
	if mcpServerEqual(a, c) {
		t.Fatal("different content should not be equal")
	}
}
