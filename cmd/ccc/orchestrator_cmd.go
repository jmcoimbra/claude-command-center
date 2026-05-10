package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/orchestrator"
)

// runOrchestrator dispatches `ccc orchestrator <verb> [args]`.
func runOrchestrator(args []string) error {
	if len(args) == 0 {
		printOrchestratorUsage()
		return fmt.Errorf("orchestrator: subcommand required")
	}
	verb := args[0]
	rest := args[1:]
	switch verb {
	case "init":
		return runOrchInit(rest)
	case "status":
		return runOrchStatus(rest)
	case "thread":
		return runOrchThread(rest)
	case "decision":
		return runOrchDecision(rest)
	case "question":
		return runOrchQuestion(rest)
	case "overlap-check":
		return runOrchOverlapCheck(rest)
	case "paste-header":
		return runOrchPasteHeader(rest)
	case "complete":
		return runOrchComplete(rest)
	case "list":
		return runOrchList(rest)
	case "-h", "--help", "help":
		printOrchestratorUsage()
		return nil
	default:
		printOrchestratorUsage()
		return fmt.Errorf("orchestrator: unknown subcommand %q", verb)
	}
}

func printOrchestratorUsage() {
	fmt.Fprintln(os.Stderr, "Usage: ccc orchestrator <verb> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Verbs:")
	fmt.Fprintln(os.Stderr, "  init [--project <path>]                Create or no-op the orchestrator named by current session topic")
	fmt.Fprintln(os.Stderr, "  status [--json]                        Print current state")
	fmt.Fprintln(os.Stderr, "  thread add --name N [flags]            Add a thread")
	fmt.Fprintln(os.Stderr, "  thread set-status --name N --status S  Update a thread's status")
	fmt.Fprintln(os.Stderr, "  thread complete --name N               Mark a thread complete")
	fmt.Fprintln(os.Stderr, "  decision add --body T [--thread N]     Append a decision")
	fmt.Fprintln(os.Stderr, "  question add --body T [--thread N]     Append an open question")
	fmt.Fprintln(os.Stderr, "  question resolve --id Q1 [--note T]    Resolve a question")
	fmt.Fprintln(os.Stderr, "  overlap-check [--project P] [--themes T] List active orchestrators that overlap (JSON)")
	fmt.Fprintln(os.Stderr, "  paste-header --thread N                Emit the standardized PASTE INTO block")
	fmt.Fprintln(os.Stderr, "  complete                               Mark the current orchestrator complete")
	fmt.Fprintln(os.Stderr, "  list [--all] [--json]                  List orchestrators (active by default)")
}

// resolveName resolves the orchestrator name from the current session topic
// and prints a useful error if the topic is missing or malformed.
func resolveName() (string, error) {
	name, err := orchestrator.ResolveFromTopic()
	if err != nil {
		return "", fmt.Errorf("orchestrator: %w (set topic with `/set-topic ORCHESTRATE: <name>`)", err)
	}
	return name, nil
}

// readBody returns body verbatim if not equal to "-", otherwise reads stdin.
func readBody(body string) (string, error) {
	if body != "-" {
		return body, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ----- subcommands -----

func runOrchInit(args []string) error {
	fs := flag.NewFlagSet("orchestrator init", flag.ContinueOnError)
	project := fs.String("project", "", "Project path (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	if err := orchestrator.Init(name, *project); err != nil {
		return err
	}
	fmt.Printf("Orchestrator %q ready at %s\n", name, orchestrator.DirFor(name))
	return nil
}

func runOrchStatus(args []string) error {
	fs := flag.NewFlagSet("orchestrator status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Emit JSON instead of markdown")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	o, err := orchestrator.Load(name)
	if err != nil {
		return err
	}
	if *asJSON {
		return printOrchestratorJSON(o)
	}
	data, err := os.ReadFile(orchestrator.StateMDPath(name))
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func printOrchestratorJSON(o *orchestrator.Orchestrator) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Name        string                    `json:"name"`
		Status      string                    `json:"status"`
		Project     string                    `json:"project"`
		StartedAt   time.Time                 `json:"started_at"`
		CompletedAt *time.Time                `json:"completed_at,omitempty"`
		Threads     []orchestrator.Thread     `json:"threads"`
		Decisions   []orchestrator.Decision   `json:"decisions"`
		Questions   []orchestrator.Question   `json:"questions"`
		Notes       string                    `json:"notes"`
	}{
		Name:        o.Name,
		Status:      o.Status,
		Project:     o.Project,
		StartedAt:   o.StartedAt,
		CompletedAt: o.CompletedAt,
		Threads:     o.Threads,
		Decisions:   o.Decisions,
		Questions:   o.Questions,
		Notes:       o.Notes,
	})
}

func runOrchThread(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("orchestrator thread: subcommand required (add | set-status | complete)")
	}
	switch args[0] {
	case "add":
		return runOrchThreadAdd(args[1:])
	case "set-status":
		return runOrchThreadSetStatus(args[1:])
	case "complete":
		return runOrchThreadComplete(args[1:])
	default:
		return fmt.Errorf("orchestrator thread: unknown subcommand %q", args[0])
	}
}

func runOrchThreadAdd(args []string) error {
	fs := flag.NewFlagSet("orchestrator thread add", flag.ContinueOnError)
	tName := fs.String("name", "", "Thread name (required)")
	project := fs.String("project", "", "Project path")
	branch := fs.String("branch", "", "Branch")
	worktree := fs.String("worktree", "", "Worktree path")
	sessionID := fs.String("session-id", "", "CCC session ID")
	status := fs.String("status", "planning", "Initial status")
	summary := fs.String("summary", "", "Initial last-summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tName == "" {
		return fmt.Errorf("--name is required")
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	t := orchestrator.Thread{
		Name:        *tName,
		Status:      *status,
		Project:     *project,
		Branch:      *branch,
		Worktree:    *worktree,
		SessionID:   *sessionID,
		LastSummary: *summary,
	}
	if err := orchestrator.AddThread(name, t); err != nil {
		return err
	}
	fmt.Printf("Thread %q added (status=%s)\n", *tName, *status)
	return nil
}

func runOrchThreadSetStatus(args []string) error {
	fs := flag.NewFlagSet("orchestrator thread set-status", flag.ContinueOnError)
	tName := fs.String("name", "", "Thread name (required)")
	status := fs.String("status", "", "New status (required)")
	reason := fs.String("reason", "", "Reason (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tName == "" || *status == "" {
		return fmt.Errorf("--name and --status are required")
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	if err := orchestrator.SetThreadStatus(name, *tName, *status, *reason); err != nil {
		return err
	}
	fmt.Printf("Thread %q status=%s\n", *tName, *status)
	return nil
}

func runOrchThreadComplete(args []string) error {
	fs := flag.NewFlagSet("orchestrator thread complete", flag.ContinueOnError)
	tName := fs.String("name", "", "Thread name (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tName == "" {
		return fmt.Errorf("--name is required")
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	if err := orchestrator.CompleteThread(name, *tName); err != nil {
		return err
	}
	fmt.Printf("Thread %q complete\n", *tName)
	return nil
}

func runOrchDecision(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return fmt.Errorf("orchestrator decision: only 'add' is supported")
	}
	fs := flag.NewFlagSet("orchestrator decision add", flag.ContinueOnError)
	body := fs.String("body", "", "Decision body (required, or '-' for stdin)")
	thread := fs.String("thread", "", "Associated thread (optional)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	text, err := readBody(*body)
	if err != nil {
		return err
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	if err := orchestrator.AddDecision(name, text, *thread); err != nil {
		return err
	}
	fmt.Println("Decision recorded")
	return nil
}

func runOrchQuestion(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("orchestrator question: subcommand required (add | resolve)")
	}
	switch args[0] {
	case "add":
		return runOrchQuestionAdd(args[1:])
	case "resolve":
		return runOrchQuestionResolve(args[1:])
	default:
		return fmt.Errorf("orchestrator question: unknown subcommand %q", args[0])
	}
}

func runOrchQuestionAdd(args []string) error {
	fs := flag.NewFlagSet("orchestrator question add", flag.ContinueOnError)
	body := fs.String("body", "", "Question body (required, or '-' for stdin)")
	thread := fs.String("thread", "", "Associated thread (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	text, err := readBody(*body)
	if err != nil {
		return err
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	id, err := orchestrator.AddQuestion(name, text, *thread)
	if err != nil {
		return err
	}
	fmt.Printf("Question %s recorded\n", id)
	return nil
}

func runOrchQuestionResolve(args []string) error {
	fs := flag.NewFlagSet("orchestrator question resolve", flag.ContinueOnError)
	id := fs.String("id", "", "Question ID (required)")
	note := fs.String("note", "", "Resolution note (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	if err := orchestrator.ResolveQuestion(name, *id, *note); err != nil {
		return err
	}
	fmt.Printf("Question %s resolved\n", *id)
	return nil
}

func runOrchOverlapCheck(args []string) error {
	fs := flag.NewFlagSet("orchestrator overlap-check", flag.ContinueOnError)
	project := fs.String("project", "", "Project path to match")
	themes := fs.String("themes", "", "Comma-separated themes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var themeList []string
	if *themes != "" {
		themeList = strings.Split(*themes, ",")
	}
	matches, err := orchestrator.OverlapCheck(*project, themeList)
	if err != nil {
		return err
	}
	if matches == nil {
		matches = []orchestrator.OverlapMatch{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(matches)
}

func runOrchPasteHeader(args []string) error {
	fs := flag.NewFlagSet("orchestrator paste-header", flag.ContinueOnError)
	thread := fs.String("thread", "", "Thread name (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *thread == "" {
		return fmt.Errorf("--thread is required")
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	out, err := orchestrator.PasteHeader(name, *thread)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func runOrchComplete(args []string) error {
	fs := flag.NewFlagSet("orchestrator complete", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := resolveName()
	if err != nil {
		return err
	}
	changed, err := orchestrator.Complete(name)
	if err != nil {
		return err
	}
	if changed {
		fmt.Printf("Orchestrator %q complete\n", name)
	} else {
		fmt.Printf("Orchestrator %q was already complete\n", name)
	}
	return nil
}

func runOrchList(args []string) error {
	fs := flag.NewFlagSet("orchestrator list", flag.ContinueOnError)
	all := fs.Bool("all", false, "Include completed orchestrators")
	asJSON := fs.Bool("json", false, "Emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list, err := orchestrator.List(*all)
	if err != nil {
		return err
	}
	if *asJSON {
		type entry struct {
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Project     string     `json:"project"`
			StartedAt   time.Time  `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at,omitempty"`
			ThreadCount int        `json:"thread_count"`
		}
		out := make([]entry, 0, len(list))
		for _, o := range list {
			out = append(out, entry{
				Name:        o.Name,
				Status:      o.Status,
				Project:     o.Project,
				StartedAt:   o.StartedAt,
				CompletedAt: o.CompletedAt,
				ThreadCount: len(o.Threads),
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(list) == 0 {
		fmt.Println("No orchestrators.")
		return nil
	}
	for _, o := range list {
		started := ""
		if !o.StartedAt.IsZero() {
			started = o.StartedAt.Format("2006-01-02")
		}
		fmt.Printf("%-30s  %-10s  threads=%-2d  project=%s  started=%s\n",
			o.Name, o.Status, len(o.Threads), o.Project, started)
	}
	return nil
}
