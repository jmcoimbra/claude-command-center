package orchestrator

import (
	"fmt"
	"strings"
	"time"
)

// renderStateMD produces the canonical state.md serialization.
func renderStateMD(o *Orchestrator) string {
	var b strings.Builder

	// Frontmatter
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", o.Name)
	fmt.Fprintf(&b, "status: %s\n", o.Status)
	fmt.Fprintf(&b, "project: %s\n", o.Project)
	fmt.Fprintf(&b, "started_at: %s\n", formatTimeOrEmpty(&o.StartedAt))
	fmt.Fprintf(&b, "completed_at: %s\n", formatTimeOrEmpty(o.CompletedAt))
	b.WriteString("---\n\n")

	// Threads
	b.WriteString("# Threads\n\n")
	for _, t := range o.Threads {
		fmt.Fprintf(&b, "## %s\n", t.Name)
		if t.Role != "" {
			fmt.Fprintf(&b, "- role: %s\n", t.Role)
		}
		fmt.Fprintf(&b, "- status: %s\n", t.Status)
		fmt.Fprintf(&b, "- project: %s\n", t.Project)
		fmt.Fprintf(&b, "- branch: %s\n", t.Branch)
		fmt.Fprintf(&b, "- worktree: %s\n", t.Worktree)
		fmt.Fprintf(&b, "- session-id: %s\n", t.SessionID)
		fmt.Fprintf(&b, "- last-update: %s\n", formatTimeOrEmpty(&t.LastUpdate))
		fmt.Fprintf(&b, "- last-summary: %s\n", t.LastSummary)
		b.WriteString("\n")
	}

	// Decisions
	b.WriteString("# Decisions\n\n")
	for _, d := range o.Decisions {
		stamp := d.Time.UTC().Format(time.RFC3339)
		if d.Thread != "" {
			fmt.Fprintf(&b, "- %s [thread:%s]: %s\n", stamp, d.Thread, d.Body)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", stamp, d.Body)
		}
	}
	b.WriteString("\n")

	// Questions
	b.WriteString("# Questions\n\n")
	for _, q := range o.Questions {
		stamp := q.Time.UTC().Format(time.RFC3339)
		threadTag := ""
		if q.Thread != "" {
			threadTag = fmt.Sprintf(" [thread:%s]", q.Thread)
		}
		fmt.Fprintf(&b, "- (%s) %s [%s]%s: %s", q.Status, stamp, q.ID, threadTag, q.Body)
		if q.Status == QuestionResolved && q.ResolvedAt != nil {
			fmt.Fprintf(&b, " (resolved %s", q.ResolvedAt.UTC().Format(time.RFC3339))
			if q.Note != "" {
				fmt.Fprintf(&b, ": %s", q.Note)
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Notes
	b.WriteString("# Notes\n\n")
	if strings.TrimSpace(o.Notes) != "" {
		b.WriteString(strings.TrimRight(o.Notes, "\n"))
		b.WriteString("\n")
	}

	return b.String()
}

func formatTimeOrEmpty(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseStateMD parses a state.md document into an Orchestrator.
func parseStateMD(s string) (*Orchestrator, error) {
	o := &Orchestrator{}
	lines := strings.Split(s, "\n")
	if len(lines) < 1 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("missing opening --- frontmatter delimiter")
	}
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return nil, fmt.Errorf("unclosed frontmatter")
	}
	for _, line := range lines[1:endIdx] {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			o.Name = val
		case "status":
			if val == "" {
				val = StatusActive
			}
			o.Status = val
		case "project":
			o.Project = val
		case "started_at":
			if t := parseTimeOrZero(val); !t.IsZero() {
				o.StartedAt = t
			}
		case "completed_at":
			if t := parseTimeOrZero(val); !t.IsZero() {
				tt := t
				o.CompletedAt = &tt
			}
		}
	}

	// Walk sections after frontmatter.
	current := ""
	var threadBuf *Thread
	notesBuf := strings.Builder{}
	flushThread := func() {
		if threadBuf != nil {
			o.Threads = append(o.Threads, *threadBuf)
			threadBuf = nil
		}
	}

	for i := endIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Section header
		if strings.HasPrefix(trimmed, "# ") {
			flushThread()
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}

		switch current {
		case "Threads":
			if strings.HasPrefix(trimmed, "## ") {
				flushThread()
				threadBuf = &Thread{Name: strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))}
				continue
			}
			if threadBuf == nil {
				continue
			}
			if strings.HasPrefix(trimmed, "- ") {
				kv := strings.TrimPrefix(trimmed, "- ")
				k, v, ok := strings.Cut(kv, ":")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				switch k {
				case "role":
					threadBuf.Role = v
				case "status":
					threadBuf.Status = v
				case "project":
					threadBuf.Project = v
				case "branch":
					threadBuf.Branch = v
				case "worktree":
					threadBuf.Worktree = v
				case "session-id":
					threadBuf.SessionID = v
				case "last-update":
					threadBuf.LastUpdate = parseTimeOrZero(v)
				case "last-summary":
					threadBuf.LastSummary = v
				}
			}
		case "Decisions":
			if strings.HasPrefix(trimmed, "- ") {
				d, ok := parseDecisionLine(strings.TrimPrefix(trimmed, "- "))
				if ok {
					o.Decisions = append(o.Decisions, d)
				}
			}
		case "Questions":
			if strings.HasPrefix(trimmed, "- ") {
				q, ok := parseQuestionLine(strings.TrimPrefix(trimmed, "- "))
				if ok {
					o.Questions = append(o.Questions, q)
				}
			}
		case "Notes":
			notesBuf.WriteString(line)
			notesBuf.WriteString("\n")
		}
	}
	flushThread()
	o.Notes = strings.TrimSpace(notesBuf.String())
	return o, nil
}

func parseTimeOrZero(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// parseDecisionLine parses a line like:
//
//	2026-05-09T15:30:00Z: chose to defer
//	2026-05-09T15:30:00Z [thread:postgres]: chose to defer
func parseDecisionLine(s string) (Decision, bool) {
	// First space splits timestamp (possibly with trailing colon) from rest.
	stamp, rest, ok := strings.Cut(s, " ")
	if !ok {
		return Decision{}, false
	}
	// When there is no thread tag, render writes "<stamp>: body" — that
	// trailing colon ends up attached to the stamp via Cut(" "). Strip it.
	stamp = strings.TrimSuffix(stamp, ":")
	t := parseTimeOrZero(stamp)
	if t.IsZero() {
		return Decision{}, false
	}
	thread := ""
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "[thread:") {
		end := strings.Index(rest, "]")
		if end > 0 {
			thread = strings.TrimPrefix(rest[:end], "[thread:")
			rest = strings.TrimSpace(rest[end+1:])
		}
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	return Decision{Time: t, Body: rest, Thread: thread}, true
}

// parseQuestionLine parses a line like:
//
//	(open) 2026-05-09T15:35:00Z [Q1]: should we migrate the indexes
//	(resolved) 2026-05-09T15:35:00Z [Q1] [thread:postgres]: ... (resolved 2026-05-09T16:00:00Z: yes)
func parseQuestionLine(s string) (Question, bool) {
	q := Question{}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") {
		return q, false
	}
	end := strings.Index(s, ")")
	if end < 0 {
		return q, false
	}
	q.Status = s[1:end]
	rest := strings.TrimSpace(s[end+1:])

	// Timestamp
	stamp, after, ok := strings.Cut(rest, " ")
	if !ok {
		return q, false
	}
	q.Time = parseTimeOrZero(stamp)
	if q.Time.IsZero() {
		return q, false
	}
	rest = strings.TrimSpace(after)

	// [Q<n>]
	if !strings.HasPrefix(rest, "[") {
		return q, false
	}
	idEnd := strings.Index(rest, "]")
	if idEnd < 0 {
		return q, false
	}
	q.ID = strings.TrimPrefix(rest[:idEnd], "[")
	rest = strings.TrimSpace(rest[idEnd+1:])

	// optional [thread:<name>]
	if strings.HasPrefix(rest, "[thread:") {
		threadEnd := strings.Index(rest, "]")
		if threadEnd > 0 {
			q.Thread = strings.TrimPrefix(rest[:threadEnd], "[thread:")
			rest = strings.TrimSpace(rest[threadEnd+1:])
		}
	}

	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))

	// Optional resolution annotation: "... (resolved <stamp>[: note])"
	// Split annotation on ": " (colon-space) — timestamp itself contains
	// colons, so a bare ":" cut would land in the middle of the timestamp.
	if idx := strings.LastIndex(rest, "(resolved "); idx >= 0 && strings.HasSuffix(rest, ")") {
		body := strings.TrimSpace(rest[:idx])
		annot := strings.TrimSpace(rest[idx+len("(resolved ") : len(rest)-1])
		annStamp, annNote, hasNote := strings.Cut(annot, ": ")
		if rt := parseTimeOrZero(strings.TrimSpace(annStamp)); !rt.IsZero() {
			q.ResolvedAt = &rt
		}
		if hasNote {
			q.Note = strings.TrimSpace(annNote)
		}
		q.Body = body
	} else {
		q.Body = rest
	}
	return q, true
}
