package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempRoot points the orchestrator package at a fresh temp directory for
// the duration of a test, restoring the env var afterward.
func withTempRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CCC_ORCHESTRATOR_ROOT", dir)
	return dir
}

func TestInit_CreatesDirectoryAndFiles(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("postgres-migration", "/tmp/proj"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir := filepath.Join(root, "postgres-migration")
	for _, f := range []string{"state.md", "transcript.md", "state.log", "log.sh"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	// log.sh is executable
	info, err := os.Stat(filepath.Join(dir, "log.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("log.sh should be executable, got mode %v", info.Mode().Perm())
	}
}

func TestInit_Idempotent(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", "/tmp/proj"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	o1, _ := Load("foo")
	if err := Init("foo", "/different"); err != nil {
		t.Fatalf("Init second time: %v", err)
	}
	o2, _ := Load("foo")
	// state.md must NOT have been overwritten — project stays as first init.
	if o2.Project != o1.Project {
		t.Errorf("Init overwrote state.md project: was %q, now %q", o1.Project, o2.Project)
	}
}

func TestInit_RecreatesMissingAncillaryFiles(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("foo", "/tmp/proj"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Delete state.log and ensure Init recreates it without touching state.md.
	logPath := filepath.Join(root, "foo", "state.log")
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(root, "foo", "state.md"))
	if err := Init("foo", "/tmp/proj"); err != nil {
		t.Fatalf("Init second time: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("state.log not recreated: %v", err)
	}
	current, _ := os.ReadFile(filepath.Join(root, "foo", "state.md"))
	if string(original) != string(current) {
		t.Errorf("state.md was modified by Init")
	}
}

func TestRoundTrip_PreservesCoreFields(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", "/repo"); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{
		Name:        "alpha",
		Status:      "in-flight",
		Project:     "/repo",
		Branch:      "feature/x",
		SessionID:   "sess-1",
		LastSummary: "stage 8 done",
	}); err != nil {
		t.Fatal(err)
	}
	if err := AddDecision("foo", "use postgres 16", ""); err != nil {
		t.Fatal(err)
	}
	id, err := AddQuestion("foo", "indexes before or after data?", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if id != "Q1" {
		t.Errorf("first question id should be Q1, got %s", id)
	}
	o, err := Load("foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Threads) != 1 || o.Threads[0].Name != "alpha" {
		t.Errorf("threads: %+v", o.Threads)
	}
	if len(o.Decisions) != 1 || !strings.Contains(o.Decisions[0].Body, "postgres 16") {
		t.Errorf("decisions: %+v", o.Decisions)
	}
	if len(o.Questions) != 1 || o.Questions[0].ID != "Q1" || o.Questions[0].Thread != "alpha" {
		t.Errorf("questions: %+v", o.Questions)
	}
}

func TestAddThread_DuplicateFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	err := AddThread("foo", Thread{Name: "alpha"})
	if err == nil {
		t.Fatal("expected duplicate thread to fail")
	}
}

func TestSetThreadStatus_UpdatesAndLogs(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{Name: "alpha", Status: "planning"}); err != nil {
		t.Fatal(err)
	}
	if err := SetThreadStatus("foo", "alpha", "blocked", "waiting on legal"); err != nil {
		t.Fatal(err)
	}
	o, _ := Load("foo")
	if o.Threads[0].Status != "blocked" {
		t.Errorf("status not updated: %s", o.Threads[0].Status)
	}
	logData, _ := os.ReadFile(filepath.Join(root, "foo", "state.log"))
	if !strings.Contains(string(logData), "thread set-status alpha status=blocked") {
		t.Errorf("state.log missing entry: %s", string(logData))
	}
	if !strings.Contains(string(logData), "reason=waiting on legal") {
		t.Errorf("state.log missing reason: %s", string(logData))
	}
}

func TestSetThreadStatus_UnknownThreadFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	err := SetThreadStatus("foo", "ghost", "blocked", "")
	if err == nil {
		t.Fatal("expected unknown thread to fail")
	}
}

func TestQuestionLifecycle(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	id1, _ := AddQuestion("foo", "first", "")
	id2, _ := AddQuestion("foo", "second", "")
	if id1 != "Q1" || id2 != "Q2" {
		t.Errorf("ids: %s %s", id1, id2)
	}
	if err := ResolveQuestion("foo", "Q1", "decided yes"); err != nil {
		t.Fatal(err)
	}
	o, _ := Load("foo")
	q1Status := ""
	q1Note := ""
	for _, q := range o.Questions {
		if q.ID == "Q1" {
			q1Status = q.Status
			q1Note = q.Note
		}
	}
	if q1Status != QuestionResolved {
		t.Errorf("Q1 status: %s", q1Status)
	}
	if q1Note != "decided yes" {
		t.Errorf("Q1 note: %s", q1Note)
	}
}

func TestResolveQuestion_AlreadyResolvedFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	id, _ := AddQuestion("foo", "x", "")
	if err := ResolveQuestion("foo", id, ""); err != nil {
		t.Fatal(err)
	}
	err := ResolveQuestion("foo", id, "")
	if err == nil {
		t.Fatal("expected double-resolve to fail")
	}
}

func TestResolveQuestion_UnknownIDFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	err := ResolveQuestion("foo", "Q42", "")
	if err == nil {
		t.Fatal("expected unknown qid to fail")
	}
}

func TestComplete_Idempotent(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	changed, err := Complete("foo")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("first Complete should report changed=true")
	}
	changed2, err := Complete("foo")
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("second Complete should report changed=false")
	}
	o, _ := Load("foo")
	if o.Status != StatusComplete || o.CompletedAt == nil {
		t.Errorf("complete state: %+v", o)
	}
}

func TestList_FiltersCompleted(t *testing.T) {
	withTempRoot(t)
	if err := Init("alpha", ""); err != nil {
		t.Fatal(err)
	}
	if err := Init("beta", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete("alpha"); err != nil {
		t.Fatal(err)
	}
	active, err := List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Name != "beta" {
		t.Errorf("active list: %+v", active)
	}
	all, err := List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("all list: %+v", all)
	}
}

func TestOverlapCheck_ProjectSubstring(t *testing.T) {
	withTempRoot(t)
	if err := Init("alpha", "/Users/aaron/Personal/sherlock"); err != nil {
		t.Fatal(err)
	}
	if err := Init("beta", "/Users/aaron/Personal/other"); err != nil {
		t.Fatal(err)
	}
	matches, err := OverlapCheck("/Users/aaron/Personal/sherlock", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "alpha" {
		t.Errorf("matches: %+v", matches)
	}
}

func TestOverlapCheck_EmptyOnNoMatch(t *testing.T) {
	withTempRoot(t)
	if err := Init("alpha", "/some/path"); err != nil {
		t.Fatal(err)
	}
	matches, err := OverlapCheck("/totally/different", nil)
	if err != nil {
		t.Fatal(err)
	}
	if matches != nil && len(matches) != 0 {
		t.Errorf("expected empty, got %+v", matches)
	}
}

func TestOverlapCheck_ExcludesCompleted(t *testing.T) {
	withTempRoot(t)
	if err := Init("alpha", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete("alpha"); err != nil {
		t.Fatal(err)
	}
	matches, _ := OverlapCheck("/repo", nil)
	if len(matches) != 0 {
		t.Errorf("completed orchestrator should not match: %+v", matches)
	}
}

func TestPasteHeader_IncludesThreadFields(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{
		Name:     "alpha",
		Project:  "/repo",
		Branch:   "main",
		Worktree: "/tmp/wt",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := PasteHeader("foo", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alpha", "/repo", "/tmp/wt", "PASTE INTO"} {
		if !strings.Contains(out, want) {
			t.Errorf("paste header missing %q:\n%s", want, out)
		}
	}
}

func TestPasteHeader_UnknownThreadFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := PasteHeader("foo", "ghost"); err == nil {
		t.Fatal("expected unknown thread to fail")
	}
}

func TestResolveFromTopic_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CCC_SESSION_TOPICS_DIR", dir)
	t.Setenv("CCC_SESSION_ID", "test-sess")
	if err := os.WriteFile(filepath.Join(dir, "test-sess.txt"), []byte("ORCHESTRATE: postgres-migration"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := ResolveFromTopic()
	if err != nil {
		t.Fatal(err)
	}
	if name != "postgres-migration" {
		t.Errorf("name: %s", name)
	}
}

func TestResolveFromTopic_WrongPrefixFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CCC_SESSION_TOPICS_DIR", dir)
	t.Setenv("CCC_SESSION_ID", "test-sess")
	// Wrong-case prefix should not match.
	if err := os.WriteFile(filepath.Join(dir, "test-sess.txt"), []byte("Orchestrate: foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFromTopic(); err == nil {
		t.Fatal("expected wrong-case prefix to fail")
	}
}

func TestResolveFromTopic_NoTopicFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CCC_SESSION_TOPICS_DIR", dir)
	t.Setenv("CCC_SESSION_ID", "test-sess")
	// No topic file written
	if _, err := ResolveFromTopic(); err == nil {
		t.Fatal("expected missing topic to fail")
	}
}

func TestMutators_PreserveOtherSections(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", "/repo"); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{Name: "alpha", Status: "planning"}); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{Name: "beta", Status: "in-flight"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddQuestion("foo", "q1", ""); err != nil {
		t.Fatal(err)
	}
	// Now add a decision; threads + questions must survive intact.
	if err := AddDecision("foo", "decided X", ""); err != nil {
		t.Fatal(err)
	}
	o, _ := Load("foo")
	if len(o.Threads) != 2 {
		t.Errorf("threads lost: %+v", o.Threads)
	}
	if len(o.Questions) != 1 {
		t.Errorf("questions lost: %+v", o.Questions)
	}
	if len(o.Decisions) != 1 {
		t.Errorf("decisions: %+v", o.Decisions)
	}
}

func TestStateLog_AppendsTimestamp(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("foo", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "foo", "state.log"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// init + thread add = 2 entries
	if len(lines) != 2 {
		t.Errorf("expected 2 state.log entries, got %d:\n%s", len(lines), string(data))
	}
	for _, line := range lines {
		if len(line) < 20 {
			t.Errorf("state.log line too short: %q", line)
		}
	}
}

func TestList_EmptyRootReturnsNil(t *testing.T) {
	withTempRoot(t)
	out, err := List(false)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && len(out) != 0 {
		t.Errorf("expected empty, got %+v", out)
	}
}

// Sanity: rendered + parsed state.md should preserve core fields after several mutators.
func TestRenderParseFidelity(t *testing.T) {
	withTempRoot(t)
	if err := Init("foo", "/Users/x/repo"); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("foo", Thread{
		Name: "t1", Status: "planning", Project: "/Users/x/repo",
		Branch: "main", Worktree: "/tmp/wt", SessionID: "abc",
		LastSummary: "did the thing",
	}); err != nil {
		t.Fatal(err)
	}
	if err := AddDecision("foo", "decision body with: colons", "t1"); err != nil {
		t.Fatal(err)
	}
	id, _ := AddQuestion("foo", "complex? question with [brackets]", "t1")
	if err := ResolveQuestion("foo", id, "yes"); err != nil {
		t.Fatal(err)
	}
	o, err := Load("foo")
	if err != nil {
		t.Fatal(err)
	}
	if o.Threads[0].Branch != "main" || o.Threads[0].SessionID != "abc" {
		t.Errorf("thread fields lost after roundtrip: %+v", o.Threads[0])
	}
	// Body preserved through colons
	if !strings.HasSuffix(o.Decisions[0].Body, "colons") {
		t.Errorf("decision body lost colons: %q", o.Decisions[0].Body)
	}
	if o.Questions[0].Status != QuestionResolved {
		t.Errorf("question not resolved: %+v", o.Questions[0])
	}
	if o.Questions[0].Note != "yes" {
		t.Errorf("resolution note lost: %q", o.Questions[0].Note)
	}
}

func TestAddThread_RolePersistsAndRoundTrips(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "wave-0c", Role: "spine"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "o", "state.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- role: spine") {
		t.Errorf("state.md missing role line:\n%s", string(data))
	}
	loaded, err := Load("o")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Threads[0].Role != "spine" {
		t.Errorf("Role lost on round-trip: %q", loaded.Threads[0].Role)
	}
}

func TestAddThread_RoleEmptyByDefault(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "o", "state.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "- role:") {
		t.Errorf("state.md should not emit role line when empty:\n%s", string(data))
	}
	loaded, _ := Load("o")
	if loaded.Threads[0].Role != "" {
		t.Errorf("Role should be empty, got %q", loaded.Threads[0].Role)
	}
}

func TestSetThreadRole_UpdatesAndLogs(t *testing.T) {
	root := withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "wave 0c: typed API spine"}); err != nil {
		t.Fatal(err)
	}
	if err := SetThreadRole("o", "wave 0c: typed API spine", "spine"); err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load("o")
	if loaded.Threads[0].Role != "spine" {
		t.Errorf("Role not updated: %q", loaded.Threads[0].Role)
	}
	logData, _ := os.ReadFile(filepath.Join(root, "o", "state.log"))
	if !strings.Contains(string(logData), "thread set-role wave 0c: typed API spine role=spine") {
		t.Errorf("state.log missing set-role entry:\n%s", string(logData))
	}
}

func TestSetThreadRole_UnknownThreadFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetThreadRole("o", "ghost", "spine"); err == nil {
		t.Fatal("expected unknown thread to fail")
	}
}

func TestSetThreadRole_RequiresRole(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := SetThreadRole("o", "alpha", ""); err == nil {
		t.Fatal("expected empty role to fail")
	}
}

func TestRoleForThread_StoredOverridesName(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "wave 0c", Role: "spine"}); err != nil {
		t.Fatal(err)
	}
	role, err := RoleForThread("o", "wave 0c")
	if err != nil {
		t.Fatal(err)
	}
	if role != "spine" {
		t.Errorf("expected spine, got %q", role)
	}
}

func TestRoleForThread_FallsBackToName(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddThread("o", Thread{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	role, err := RoleForThread("o", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if role != "alpha" {
		t.Errorf("expected alpha fallback, got %q", role)
	}
}

func TestRoleForThread_UnknownThreadFails(t *testing.T) {
	withTempRoot(t)
	if err := Init("o", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RoleForThread("o", "ghost"); err == nil {
		t.Fatal("expected unknown thread to fail")
	}
}

// dummy helper to surface unused-import warnings if the import block is wrong
var _ = fmt.Sprintf
