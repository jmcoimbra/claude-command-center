package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withOrchestratorTopic sets up a temp orchestrator root and a fake session
// topic file resolving to the supplied name.
func withOrchestratorTopic(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	topics := t.TempDir()
	t.Setenv("CCC_ORCHESTRATOR_ROOT", root)
	t.Setenv("CCC_SESSION_TOPICS_DIR", topics)
	t.Setenv("CCC_SESSION_ID", "test-sess")
	if name != "" {
		if err := os.WriteFile(filepath.Join(topics, "test-sess.txt"), []byte("ORCHESTRATE: "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRunOrchInit_CreatesDirectory(t *testing.T) {
	root := withOrchestratorTopic(t, "alpha")
	if err := runOrchestrator([]string{"init", "--project", "/proj"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha", "state.md")); err != nil {
		t.Errorf("state.md not created: %v", err)
	}
}

func TestRunOrchInit_FailsWithoutTopic(t *testing.T) {
	withOrchestratorTopic(t, "") // no topic file
	err := runOrchestrator([]string{"init"})
	if err == nil {
		t.Fatal("expected error when topic is missing")
	}
}

func TestRunOrchThreadAdd_RequiresName(t *testing.T) {
	withOrchestratorTopic(t, "alpha")
	if err := runOrchestrator([]string{"init"}); err != nil {
		t.Fatal(err)
	}
	err := runOrchestrator([]string{"thread", "add"})
	if err == nil {
		t.Fatal("expected --name required")
	}
}

func TestRunOrchFullFlow(t *testing.T) {
	root := withOrchestratorTopic(t, "alpha")

	steps := [][]string{
		{"init", "--project", "/proj"},
		{"thread", "add", "--name", "t1", "--project", "/proj", "--branch", "main", "--status", "in-flight"},
		{"thread", "set-status", "--name", "t1", "--status", "blocked", "--reason", "waiting"},
		{"decision", "add", "--body", "use postgres 16", "--thread", "t1"},
		{"question", "add", "--body", "indexes first?", "--thread", "t1"},
		{"question", "resolve", "--id", "Q1", "--note", "yes, before data"},
		{"thread", "complete", "--name", "t1"},
	}
	for _, args := range steps {
		if err := runOrchestrator(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	state, err := os.ReadFile(filepath.Join(root, "alpha", "state.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"t1", "status: complete", "postgres 16", "Q1", "resolved"} {
		if !strings.Contains(string(state), want) {
			t.Errorf("state.md missing %q\n%s", want, string(state))
		}
	}
	// state.log should contain the intermediate "blocked" transition that
	// was later overwritten by complete.
	logData, err := os.ReadFile(filepath.Join(root, "alpha", "state.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "status=blocked") {
		t.Errorf("state.log missing blocked transition:\n%s", string(logData))
	}
}

func TestRunOrchOverlapCheck_EmitsJSON(t *testing.T) {
	withOrchestratorTopic(t, "alpha")
	if err := runOrchestrator([]string{"init", "--project", "/Users/aaron/Personal/sherlock"}); err != nil {
		t.Fatal(err)
	}
	// overlap-check writes JSON to stdout — capture by redirecting.
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := runOrchestrator([]string{"overlap-check", "--project", "/Users/aaron/Personal/sherlock"})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("overlap-check: %v", err)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, `"name": "alpha"`) {
		t.Errorf("overlap-check did not include alpha: %s", out)
	}
}

func TestRunOrchPasteHeader_FailsForUnknownThread(t *testing.T) {
	withOrchestratorTopic(t, "alpha")
	if err := runOrchestrator([]string{"init"}); err != nil {
		t.Fatal(err)
	}
	err := runOrchestrator([]string{"paste-header", "--thread", "ghost"})
	if err == nil {
		t.Fatal("expected unknown thread to fail")
	}
}

func TestRunOrchComplete_Idempotent(t *testing.T) {
	withOrchestratorTopic(t, "alpha")
	if err := runOrchestrator([]string{"init"}); err != nil {
		t.Fatal(err)
	}
	if err := runOrchestrator([]string{"complete"}); err != nil {
		t.Fatal(err)
	}
	if err := runOrchestrator([]string{"complete"}); err != nil {
		t.Fatalf("second complete should be no-op success: %v", err)
	}
}

func TestRunOrchUnknownVerb(t *testing.T) {
	withOrchestratorTopic(t, "alpha")
	err := runOrchestrator([]string{"frobnicate"})
	if err == nil {
		t.Fatal("expected unknown verb to fail")
	}
}
