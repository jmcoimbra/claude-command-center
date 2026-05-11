package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendMessage_AssignsSequentialIDs(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	id1, err := AppendMessage("o", Message{From: "orchestrator", To: "a", Kind: KindHandoff, Body: "first"})
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	id2, err := AppendMessage("o", Message{From: "a", To: "orchestrator", Kind: KindCheckin, Body: "ack"})
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if id1 != 1 || id2 != 2 {
		t.Errorf("expected ids 1,2 — got %d,%d", id1, id2)
	}
}

func TestAppendMessage_RequiresFields(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cases := []Message{
		{From: "orchestrator", Kind: KindHandoff, Body: "x"},      // no To
		{To: "a", Kind: KindHandoff, Body: "x"},                   // no From
		{From: "orchestrator", To: "a", Body: "x"},                // no Kind
	}
	for i, m := range cases {
		if _, err := AppendMessage("o", m); err == nil {
			t.Errorf("case %d: expected error for missing field, got nil", i)
		}
	}
}

func TestListMessages_EmptyInbox(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	msgs, err := ListMessages("o")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty inbox, got %d messages", len(msgs))
	}
}

func TestListMessages_PreservesAppendOrder(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	bodies := []string{"one", "two", "three"}
	for _, b := range bodies {
		if _, err := AppendMessage("o", Message{From: "orchestrator", To: "a", Kind: KindHandoff, Body: b}); err != nil {
			t.Fatalf("append %q: %v", b, err)
		}
	}
	msgs, err := ListMessages("o")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != len(bodies) {
		t.Fatalf("expected %d messages, got %d", len(bodies), len(msgs))
	}
	for i, m := range msgs {
		if m.Body != bodies[i] {
			t.Errorf("msg %d body = %q, want %q", i, m.Body, bodies[i])
		}
	}
}

func TestListMessages_SkipsMalformedLines(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := AppendMessage("o", Message{From: "orchestrator", To: "a", Kind: KindHandoff, Body: "good"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Tack on a hand-edited malformed line — must not crash the read.
	f, err := os.OpenFile(filepath.Join(root, "o", "inbox.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json at all\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	msgs, err := ListMessages("o")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected malformed line skipped, got %d messages", len(msgs))
	}
}

func TestFilterMessages_ToRecipientAndBroadcast(t *testing.T) {
	msgs := []Message{
		{ID: 1, To: "a", From: "orchestrator", Body: "for-a"},
		{ID: 2, To: "b", From: "orchestrator", Body: "for-b"},
		{ID: 3, To: RecipientBroadcast, From: "orchestrator", Body: "for-all"},
	}
	got := FilterMessages(msgs, MessageFilter{To: "a"}, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages for a (own + broadcast), got %d", len(got))
	}
	bodies := []string{got[0].Body, got[1].Body}
	if bodies[0] != "for-a" || bodies[1] != "for-all" {
		t.Errorf("unexpected bodies %v", bodies)
	}
}

func TestFilterMessages_UnreadRespectsBroadcast(t *testing.T) {
	msgs := []Message{
		{ID: 1, To: "a", Body: "x"},
		{ID: 2, To: RecipientBroadcast, Body: "y"},
		{ID: 3, To: "a", Body: "z"},
	}
	// Cursor at 1 — should leave ids 2 and 3 unread for a.
	got := FilterMessages(msgs, MessageFilter{To: "a", Unread: true}, 1)
	if len(got) != 2 {
		t.Fatalf("expected 2 unread, got %d", len(got))
	}
}

func TestCursors_RoundTrip(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if v, err := ReadCursor("o", "a"); err != nil || v != 0 {
		t.Fatalf("fresh cursor: got %d,%v", v, err)
	}
	if err := SetCursor("o", "a", 7); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	v, err := ReadCursor("o", "a")
	if err != nil {
		t.Fatalf("ReadCursor: %v", err)
	}
	if v != 7 {
		t.Errorf("expected cursor 7, got %d", v)
	}
}

func TestSetCursor_AutoUsesMaxID(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := AppendMessage("o", Message{From: "orchestrator", To: "a", Kind: KindHandoff, Body: "x"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := SetCursor("o", "a", 0); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	v, _ := ReadCursor("o", "a")
	if v != 3 {
		t.Errorf("expected cursor to jump to max id 3, got %d", v)
	}
}

func TestUnreadFor_FiltersByCursor(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := AppendMessage("o", Message{From: "orchestrator", To: "a", Kind: KindHandoff, Body: "x"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := SetCursor("o", "a", 3); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	unread, err := UnreadFor("o", "a")
	if err != nil {
		t.Fatalf("UnreadFor: %v", err)
	}
	if len(unread) != 2 {
		t.Errorf("expected 2 unread (ids 4,5), got %d", len(unread))
	}
	if unread[0].ID != 4 || unread[1].ID != 5 {
		t.Errorf("unread ids = %d,%d; want 4,5", unread[0].ID, unread[1].ID)
	}
}

func TestResolveRole_MatchesWorktreeFirst(t *testing.T) {
	withTempRoot(t)
	if err := Init("o1", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o1", Thread{Name: "a", Project: "/tmp/p", Worktree: "/tmp/p/.wt/a"}); err != nil {
		t.Fatalf("AddThread: %v", err)
	}
	if err := Init("o2", "/tmp/q"); err != nil {
		t.Fatalf("Init o2: %v", err)
	}
	if err := AddThread("o2", Thread{Name: "b", Project: "/tmp/q"}); err != nil {
		t.Fatalf("AddThread b: %v", err)
	}
	matches, err := ResolveRole("/tmp/p/.wt/a", "", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 || matches[0].Role != "a" || matches[0].Orchestrator != "o1" {
		t.Errorf("unexpected matches: %+v", matches)
	}
}

func TestResolveRole_FallsBackToProject(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o", Thread{Name: "main", Project: "/tmp/p"}); err != nil {
		t.Fatalf("AddThread: %v", err)
	}
	matches, err := ResolveRole("", "/tmp/p", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 || matches[0].Role != "main" {
		t.Errorf("expected project-fallback match, got %+v", matches)
	}
}

func TestResolveRole_NoMatchReturnsEmpty(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	matches, err := ResolveRole("/nowhere", "/also-nowhere", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected no matches, got %+v", matches)
	}
}

func TestResolveRole_ReturnsStoredRole(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o", Thread{
		Name:     "wave 0c: typed API spine",
		Role:     "spine",
		Project:  "/tmp/p",
		Worktree: "/tmp/p/.wt/spine",
	}); err != nil {
		t.Fatalf("AddThread: %v", err)
	}
	matches, err := ResolveRole("/tmp/p/.wt/spine", "", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d (%+v)", len(matches), matches)
	}
	if matches[0].Role != "spine" {
		t.Errorf("expected role 'spine' (stored), got %q", matches[0].Role)
	}
}

func TestResolveRole_FallsBackToThreadName(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o", Thread{
		Name:     "alpha",
		Project:  "/tmp/p",
		Worktree: "/tmp/p/.wt/alpha",
	}); err != nil {
		t.Fatalf("AddThread: %v", err)
	}
	matches, err := ResolveRole("/tmp/p/.wt/alpha", "", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 || matches[0].Role != "alpha" {
		t.Errorf("expected role fallback to thread name 'alpha', got %+v", matches)
	}
}

func TestResolveRole_ExcludesCompletedByDefault(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o", Thread{
		Name:     "wave-0",
		Project:  "/tmp/p",
		Worktree: "/tmp/p/.wt/shared",
	}); err != nil {
		t.Fatalf("AddThread wave-0: %v", err)
	}
	if err := AddThread("o", Thread{
		Name:     "wave-1",
		Project:  "/tmp/p",
		Worktree: "/tmp/p/.wt/shared",
	}); err != nil {
		t.Fatalf("AddThread wave-1: %v", err)
	}
	if err := CompleteThread("o", "wave-0"); err != nil {
		t.Fatalf("CompleteThread: %v", err)
	}

	matches, err := ResolveRole("/tmp/p/.wt/shared", "", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 || matches[0].Role != "wave-1" {
		t.Errorf("expected only wave-1 (wave-0 is complete), got %+v", matches)
	}

	allMatches, err := ResolveRole("/tmp/p/.wt/shared", "", true)
	if err != nil {
		t.Fatalf("ResolveRole include-completed: %v", err)
	}
	if len(allMatches) != 2 {
		t.Errorf("expected both threads with include-completed, got %+v", allMatches)
	}
}

func TestResolveRole_IncludeCompletedOptIn(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", "/tmp/p"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := AddThread("o", Thread{
		Name:     "wave-0",
		Project:  "/tmp/p",
		Worktree: "/tmp/p/.wt/wave-0",
	}); err != nil {
		t.Fatalf("AddThread wave-0: %v", err)
	}
	if err := CompleteThread("o", "wave-0"); err != nil {
		t.Fatalf("CompleteThread: %v", err)
	}

	matches, err := ResolveRole("/tmp/p/.wt/wave-0", "", true)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if len(matches) != 1 || matches[0].Role != "wave-0" {
		t.Errorf("expected completed thread returned with opt-in, got %+v", matches)
	}
}

func TestAppendMessage_PreservesFieldsOnDisk(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	id, err := AppendMessage("o", Message{
		From:      "orchestrator",
		To:        "a",
		Kind:      KindHandoff,
		Body:      "do the thing",
		Topic:     "the-thing",
		Project:   "/p",
		Branch:    "feat/x",
		Worktree:  "/p/.wt/a",
		SessionID: "sess-123",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "o", "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := json.Unmarshal(data[:len(data)-1], &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.ID != id || m.Topic != "the-thing" || m.Project != "/p" ||
		m.Branch != "feat/x" || m.Worktree != "/p/.wt/a" || m.SessionID != "sess-123" {
		t.Errorf("round-trip lost fields: %+v", m)
	}
}
