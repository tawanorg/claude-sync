package sync

import (
	"context"
	"testing"
)

func TestListCCDSessions_ReturnsAllRecordsNewestFirst(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdDir, "install-aaa", "local_old.json", ccdRecord("local_old", "Older", 1000))
	writeCCDRecord(t, ccdDir, "install-aaa", "local_new.json", ccdRecord("local_new", "Newer", 2000))
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	records, err := machine.syncer.ListCCDSessions(ctx)
	if err != nil {
		t.Fatalf("ListCCDSessions: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Title != "Newer" || records[1].Title != "Older" {
		t.Errorf("order = [%s, %s], want [Newer, Older]", records[0].Title, records[1].Title)
	}
	if records[0].CLISessionID == "" {
		t.Error("CLISessionID should be populated from the record")
	}
}

func TestListCCDSessions_EmptyWhenNoRecords(t *testing.T) {
	store := newMockStorage()
	machine, _ := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	records, err := machine.syncer.ListCCDSessions(ctx)
	if err != nil {
		t.Fatalf("ListCCDSessions: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}

// ListCCDSessions must resolve ${HOME} tokens in cwd for display, even
// though the raw token form is what push/pull actually compare and store —
// a person reading `desktop list` wants their real path, not the wire form.
func TestListCCDSessions_ResolvesCwdForDisplay(t *testing.T) {
	store := newMockStorage()
	pusher, ccdPusher := newCCDMachine(t, store, "install-aaa")
	pusher.syncer.paths = mustMapper(t, "/home/user", nil)
	ctx := context.Background()

	rec := `{"sessionId":"local_abc","cliSessionId":"11111111-2222-4333-8444-555555555555",` +
		`"cwd":"/home/user/claude/blink-re","title":"Blink-re","isArchived":false,"lastActivityAt":1000}`
	writeCCDRecord(t, ccdPusher, "install-aaa", "local_abc.json", rec)
	writeFile(t, pusher.claudeDir, "CLAUDE.md", "# a")
	if _, err := pusher.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	reader, _ := newCCDMachine(t, store, "install-bbb")
	reader.syncer.paths = mustMapper(t, `C:\Users\austi`, nil)

	records, err := reader.syncer.ListCCDSessions(ctx)
	if err != nil {
		t.Fatalf("ListCCDSessions: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if want := `C:\Users\austi/claude/blink-re`; records[0].CWD != want {
		t.Errorf("CWD = %q, want %q", records[0].CWD, want)
	}
}

// A record ListCCDSessions surfaces must carry an id that ForgetCCDSession
// can actually match — the two are meant to be used together.
func TestListCCDSessions_IDsRoundTripThroughForget(t *testing.T) {
	store := newMockStorage()
	machine, ccdDir := newCCDMachine(t, store, "install-aaa")
	ctx := context.Background()

	writeCCDRecord(t, ccdDir, "install-aaa", "local_abc.json", ccdRecord("local_abc", "Target", 1000))
	writeFile(t, machine.claudeDir, "CLAUDE.md", "# a")
	if _, err := machine.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	records, err := machine.syncer.ListCCDSessions(ctx)
	if err != nil {
		t.Fatalf("ListCCDSessions: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	result, err := machine.syncer.ForgetCCDSession(ctx, records[0].SessionID)
	if err != nil {
		t.Fatalf("ForgetCCDSession: %v", err)
	}
	if len(result.RemovedRemoteKeys) != 1 {
		t.Errorf("forget using listed sessionId removed %d records, want 1", len(result.RemovedRemoteKeys))
	}
}
