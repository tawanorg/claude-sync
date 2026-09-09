package sync

import (
	"strings"
	"testing"
)

// TestSessionMergeForkKeepsDAGWholeAndBranchOrder is the timestamp-ordering
// audit for forks (the same session continued on two machines). The merge is
// append-only by design — a global timestamp sort would require rewriting a
// file a live session may be appending to, the exact race the history merge
// eliminated. Correct rendering order instead rests on two invariants pinned
// here:
//  1. DAG integrity: every event's parentUuid resolves within the merged
//     file — Claude Code renders threads from these links, not line order.
//  2. Branch chronology: appended remote events keep their remote relative
//     order, which IS their chronological order within that branch.
func TestSessionMergeForkKeepsDAGWholeAndBranchOrder(t *testing.T) {
	// Common prefix: e1(t1) <- e2(t2). Local continued: e3(t4, parent e2).
	// Remote forked earlier with interleaving timestamps: e2b(t3, parent e2),
	// e2c(t5, parent e2b).
	e1 := `{"uuid":"e1","parentUuid":null,"type":"user","timestamp":"2026-08-01T10:01:00Z"}`
	e2 := `{"uuid":"e2","parentUuid":"e1","type":"assistant","timestamp":"2026-08-01T10:02:00Z"}`
	e3 := `{"uuid":"e3","parentUuid":"e2","type":"user","timestamp":"2026-08-01T10:04:00Z"}`
	e2b := `{"uuid":"e2b","parentUuid":"e2","type":"user","timestamp":"2026-08-01T10:03:00Z"}`
	e2c := `{"uuid":"e2c","parentUuid":"e2b","type":"assistant","timestamp":"2026-08-01T10:05:00Z"}`

	local := e1 + "\n" + e2 + "\n" + e3 + "\n"
	remote := e1 + "\n" + e2 + "\n" + e2b + "\n" + e2c + "\n"

	added := mergeStr(t, local, remote, false)
	if len(added) != 2 {
		t.Fatalf("added = %d lines, want 2 (e2b, e2c): %v", len(added), added)
	}
	// Branch chronology: e2b must precede e2c.
	if !strings.Contains(added[0], `"e2b"`) || !strings.Contains(added[1], `"e2c"`) {
		t.Errorf("remote branch order not preserved: %v", added)
	}

	// DAG integrity over the merged file: every parentUuid resolves.
	merged := local + strings.Join(added, "\n") + "\n"
	uuids := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimSpace(merged), "\n") {
		if i := strings.Index(ln, `"uuid":"`); i >= 0 {
			rest := ln[i+8:]
			uuids[rest[:strings.Index(rest, `"`)]] = true
		}
	}
	for _, ln := range strings.Split(strings.TrimSpace(merged), "\n") {
		i := strings.Index(ln, `"parentUuid":"`)
		if i < 0 {
			continue // null parent (root)
		}
		rest := ln[i+14:]
		parent := rest[:strings.Index(rest, `"`)]
		if !uuids[parent] {
			t.Errorf("orphaned event: parentUuid %q not in merged file (line %s)", parent, ln)
		}
	}

	// Idempotence after applying the append: nothing more to add either way.
	if again := mergeStr(t, merged, remote, false); len(again) != 0 {
		t.Errorf("re-merge added %d lines, want 0: %v", len(again), again)
	}
}

func mergeStr(t *testing.T, local, remote string, localNewer bool) []string {
	t.Helper()
	added, err := MergeSessionPayloads([]byte(local), []byte(remote), localNewer)
	if err != nil {
		t.Fatalf("MergeSessionPayloads: %v", err)
	}
	out := make([]string, len(added))
	for i, l := range added {
		out[i] = string(l)
	}
	return out
}

func TestSessionMergeAddsRemoteOnlyEvents(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n" + `{"uuid":"b","type":"assistant","parentUuid":"a"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + `{"uuid":"c","type":"assistant","parentUuid":"a"}` + "\n"
	added := mergeStr(t, local, remote, true)
	if len(added) != 1 || !strings.Contains(added[0], `"uuid":"c"`) {
		t.Errorf("want exactly the remote-only event c, got %v", added)
	}
}

func TestSessionMergeIsIdempotent(t *testing.T) {
	payload := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"mode","mode":"normal"}` + "\n"
	if added := mergeStr(t, payload, payload, true); len(added) != 0 {
		t.Errorf("merge(x,x) must add nothing, got %v", added)
	}
	if added := mergeStr(t, payload, payload, false); len(added) != 0 {
		t.Errorf("merge(x,x) must add nothing even when remote is newer, got %v", added)
	}
}

func TestSessionMergeStateWinsByFileTime(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"custom-title","customTitle":"old"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"custom-title","customTitle":"new"}` + "\n"

	added := mergeStr(t, local, remote, false)
	if len(added) != 1 || !strings.Contains(added[0], `"customTitle":"new"`) {
		t.Errorf("remote-newer: want remote title appended, got %v", added)
	}

	if added := mergeStr(t, local, remote, true); len(added) != 0 {
		t.Errorf("local-newer: remote state must be dropped, got %v", added)
	}
}

func TestSessionMergeKeepsOnlyLastStateRecordPerType(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" +
		`{"type":"mode","mode":"plan"}` + "\n" +
		`{"type":"mode","mode":"normal"}` + "\n"
	added := mergeStr(t, local, remote, false)
	if len(added) != 1 || !strings.Contains(added[0], `"mode":"normal"`) {
		t.Errorf("want only remote's LAST mode record, got %v", added)
	}
}

func TestSessionMergeUnionsUuidlessNonStateByRawLine(t *testing.T) {
	op := `{"type":"queue-operation","op":"enqueue"}`
	local := `{"uuid":"a","type":"user"}` + "\n" + op + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + op + "\n" + op + "\n"
	added := mergeStr(t, local, remote, true)
	if len(added) != 1 || added[0] != op {
		t.Errorf("want one more queue-operation from multiset union, got %v", added)
	}
}

func TestSessionMergeDropsUnparseableRemoteLinesWithoutExactMatch(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + `{"torn`
	if added := mergeStr(t, local, remote, false); len(added) != 0 {
		t.Errorf("unparseable remote-only line must be dropped, got %v", added)
	}
}

func TestSessionMergeIdenticalFinalStateDoesNotResurrectStale(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"mode","mode":"normal"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"mode","mode":"plan"}` + "\n" + `{"type":"mode","mode":"normal"}` + "\n"
	if added := mergeStr(t, local, remote, false); len(added) != 0 {
		t.Errorf("identical final state must append nothing (stale intermediate must not resurrect), got %v", added)
	}
}

func TestSessionMergePreservesStateTypesLocalNeverHad(t *testing.T) {
	local := `{"uuid":"a","type":"user"}` + "\n"
	remote := `{"uuid":"a","type":"user"}` + "\n" + `{"type":"custom-title","customTitle":"set-on-other-machine"}` + "\n"
	added := mergeStr(t, local, remote, true) // local newer — but it never had a title
	if len(added) != 1 || !strings.Contains(added[0], "set-on-other-machine") {
		t.Errorf("state type absent locally must be preserved even when local is newer, got %v", added)
	}
}
