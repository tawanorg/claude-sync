package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// readCCDRecordIfExists returns "" instead of failing the test when the
// record is absent, for assertions that specifically expect it to be gone.
func readCCDRecordIfExists(ccdDir, installID, name string) string {
	data, err := os.ReadFile(filepath.Join(ccdDir, installID, testOrg, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestForgetCCDSession_RemovesLocalAndRemote(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	rec := ccdRecord("local_abc", "Prod cutover", 1000)
	writeCCDRecord(t, ccdDir, "install-aaa", "local_abc.json", rec)
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")

	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	result, err := machine.syncer.ForgetCCDSession(ctx, "local_abc")
	if err != nil {
		t.Fatalf("ForgetCCDSession: %v", err)
	}
	if len(result.RemovedRemoteKeys) != 1 {
		t.Errorf("RemovedRemoteKeys = %v, want 1 entry", result.RemovedRemoteKeys)
	}
	if len(result.RemovedLocalPaths) != 1 {
		t.Errorf("RemovedLocalPaths = %v, want 1 entry", result.RemovedLocalPaths)
	}

	// Local file must actually be gone.
	if _, err := os.Stat(result.RemovedLocalPaths[0]); !os.IsNotExist(err) {
		t.Errorf("local file still exists: %v", err)
	}

	// A subsequent pull on a fresh machine must not resurrect it — the
	// entire point of this command over just deleting the local file.
	other, ccdOther := newCCDMachine(t, store, "install-bbb")
	writeFile(t, other.claudeDir, "CLAUDE.md", "# b")
	if _, err := other.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull on other machine: %v", err)
	}
	if got := readCCDRecordIfExists(ccdOther, "install-bbb", "local_abc.json"); got != "" {
		t.Errorf("forgotten record resurrected on another machine's pull: %s", got)
	}
}

func TestForgetCCDSession_MatchesByCLISessionID(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	rec := ccdRecord("local_abc", "Prod cutover", 1000) // cliSessionId is the fixed 11111111-... in ccdRecord
	writeCCDRecord(t, ccdDir, "install-aaa", "local_abc.json", rec)
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	result, err := machine.syncer.ForgetCCDSession(ctx, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("ForgetCCDSession: %v", err)
	}
	if len(result.RemovedRemoteKeys) != 1 || len(result.RemovedLocalPaths) != 1 {
		t.Errorf("result = %+v, want 1 remote + 1 local removed", result)
	}
}

func TestForgetCCDSession_NoMatchIsNotAnError(t *testing.T) {
	store := newMockStorage()
	machine, _ := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	result, err := machine.syncer.ForgetCCDSession(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("ForgetCCDSession: %v", err)
	}
	if len(result.RemovedRemoteKeys) != 0 || len(result.RemovedLocalPaths) != 0 {
		t.Errorf("result = %+v, want nothing removed", result)
	}
}

func TestForgetCCDSession_LeavesOtherRecordsAlone(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdDir, "install-aaa", "local_target.json", ccdRecord("local_target", "Forget me", 1000))
	writeCCDRecord(t, ccdDir, "install-aaa", "local_keep.json", ccdRecord("local_keep", "Keep me", 2000))
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	if _, err := machine.syncer.ForgetCCDSession(ctx, "local_target"); err != nil {
		t.Fatalf("ForgetCCDSession: %v", err)
	}

	if got := readCCDRecordIfExists(ccdDir, "install-aaa", "local_target.json"); got != "" {
		t.Errorf("target record still present: %s", got)
	}
	if got := readCCDRecordIfExists(ccdDir, "install-aaa", "local_keep.json"); got == "" {
		t.Error("unrelated record was removed too")
	}
}

func TestForgetCCDSession_EmptyIDIsError(t *testing.T) {
	store := newMockStorage()
	machine, _ := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	if _, err := machine.syncer.ForgetCCDSession(ctx, "   "); err == nil {
		t.Error("expected an error for an empty/whitespace session id")
	}
}
