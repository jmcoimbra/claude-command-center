package orchestrator

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AddThread appends a thread to the orchestrator. Fails if a thread with the
// same name already exists.
func AddThread(name string, t Thread) error {
	if t.Name == "" {
		return fmt.Errorf("thread name is required")
	}
	o, err := Load(name)
	if err != nil {
		return err
	}
	for _, existing := range o.Threads {
		if existing.Name == t.Name {
			return fmt.Errorf("thread %q already exists", t.Name)
		}
	}
	if t.Status == "" {
		t.Status = "planning"
	}
	if t.LastUpdate.IsZero() {
		t.LastUpdate = time.Now().UTC()
	}
	o.Threads = append(o.Threads, t)
	if err := Save(o); err != nil {
		return err
	}
	return AppendStateLog(name, fmt.Sprintf("thread add %s status=%s", t.Name, t.Status))
}

// SetThreadStatus updates a thread's status and last-update timestamp. The
// reason (if non-empty) is appended to the state-log entry.
func SetThreadStatus(name, threadName, status, reason string) error {
	if status == "" {
		return fmt.Errorf("status is required")
	}
	o, err := Load(name)
	if err != nil {
		return err
	}
	idx := findThread(o, threadName)
	if idx < 0 {
		return fmt.Errorf("thread %q not found", threadName)
	}
	o.Threads[idx].Status = status
	o.Threads[idx].LastUpdate = time.Now().UTC()
	if err := Save(o); err != nil {
		return err
	}
	entry := fmt.Sprintf("thread set-status %s status=%s", threadName, status)
	if reason != "" {
		entry += " reason=" + reason
	}
	return AppendStateLog(name, entry)
}

// CompleteThread is shorthand for SetThreadStatus(..., "complete", "").
func CompleteThread(name, threadName string) error {
	return SetThreadStatus(name, threadName, "complete", "")
}

// AddDecision appends a decision to the orchestrator. If thread is non-empty
// the decision is tagged with that thread.
func AddDecision(name, body, thread string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("decision body is required")
	}
	o, err := Load(name)
	if err != nil {
		return err
	}
	if thread != "" && findThread(o, thread) < 0 {
		return fmt.Errorf("thread %q not found", thread)
	}
	d := Decision{Time: time.Now().UTC(), Body: body, Thread: thread}
	o.Decisions = append(o.Decisions, d)
	if err := Save(o); err != nil {
		return err
	}
	logEntry := "decision add"
	if thread != "" {
		logEntry += " thread=" + thread
	}
	return AppendStateLog(name, logEntry)
}

// AddQuestion appends a question with a freshly assigned Q<n> ID. Returns
// the assigned ID.
func AddQuestion(name, body, thread string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("question body is required")
	}
	o, err := Load(name)
	if err != nil {
		return "", err
	}
	if thread != "" && findThread(o, thread) < 0 {
		return "", fmt.Errorf("thread %q not found", thread)
	}
	id := nextQuestionID(o)
	q := Question{
		ID:     id,
		Time:   time.Now().UTC(),
		Status: QuestionOpen,
		Body:   body,
		Thread: thread,
	}
	o.Questions = append(o.Questions, q)
	if err := Save(o); err != nil {
		return "", err
	}
	logEntry := "question add " + id
	if thread != "" {
		logEntry += " thread=" + thread
	}
	if err := AppendStateLog(name, logEntry); err != nil {
		return id, err
	}
	return id, nil
}

// ResolveQuestion flips a question's status to resolved. Fails if no question
// with the given ID exists or it is already resolved.
func ResolveQuestion(name, qid, note string) error {
	o, err := Load(name)
	if err != nil {
		return err
	}
	for i := range o.Questions {
		if o.Questions[i].ID != qid {
			continue
		}
		if o.Questions[i].Status == QuestionResolved {
			return fmt.Errorf("question %s is already resolved", qid)
		}
		now := time.Now().UTC()
		o.Questions[i].Status = QuestionResolved
		o.Questions[i].ResolvedAt = &now
		o.Questions[i].Note = note
		if err := Save(o); err != nil {
			return err
		}
		return AppendStateLog(name, "question resolve "+qid)
	}
	return fmt.Errorf("question %s not found", qid)
}

func findThread(o *Orchestrator, name string) int {
	for i, t := range o.Threads {
		if t.Name == name {
			return i
		}
	}
	return -1
}

func nextQuestionID(o *Orchestrator) string {
	max := 0
	for _, q := range o.Questions {
		if !strings.HasPrefix(q.ID, "Q") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(q.ID, "Q")); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("Q%d", max+1)
}

// PasteHeader returns the standardized "PASTE INTO" block for a thread.
func PasteHeader(name, threadName string) (string, error) {
	o, err := Load(name)
	if err != nil {
		return "", err
	}
	idx := findThread(o, threadName)
	if idx < 0 {
		return "", fmt.Errorf("thread %q not found", threadName)
	}
	t := o.Threads[idx]
	worktree := t.Worktree
	if worktree == "" {
		worktree = "(none)"
	}
	expectedTopic := t.Name
	var b strings.Builder
	fmt.Fprintf(&b, "─── PASTE INTO: %s ───\n", t.Name)
	fmt.Fprintf(&b, "  Project:  %s\n", t.Project)
	fmt.Fprintf(&b, "  Worktree: %s\n", worktree)
	fmt.Fprintf(&b, "  ccc topic: %q\n", expectedTopic)
	fmt.Fprintf(&b, "  Verify:   terminal prompt shows that branch before pasting\n")
	return b.String(), nil
}
