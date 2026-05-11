// Package orchestrator implements the file-based state for ccc orchestrator
// subcommands. Storage lives at ~/.claude/orchestrators/<name>/ with state.md
// as the source of truth (YAML frontmatter + four sections).
//
// See specs/core/orchestrator.md for the data model.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Status values for an orchestrator.
const (
	StatusActive   = "active"
	StatusComplete = "complete"
)

// TopicPrefix is the required session-topic prefix that identifies a session
// as belonging to an orchestrator. The orchestrator name is whatever follows
// this prefix exactly.
const TopicPrefix = "ORCHESTRATE: "

// Orchestrator is the in-memory representation of an orchestrator's state.md.
type Orchestrator struct {
	Name        string
	Status      string
	Project     string
	StartedAt   time.Time
	CompletedAt *time.Time

	Threads   []Thread
	Decisions []Decision
	Questions []Question
	Notes     string
}

// Thread tracks one working session inside an orchestrator. Status is freeform
// text — typical values are planning, in-flight, blocked, awaiting-user, complete.
// Role is the short routing key used by inbox.jsonl; when empty, the thread
// name is used as the role for backwards compatibility.
type Thread struct {
	Name        string
	Role        string
	Status      string
	Project     string
	Branch      string
	Worktree    string
	SessionID   string
	LastUpdate  time.Time
	LastSummary string
}

// Decision is a freeform note recorded by the orchestrator at a point in time.
type Decision struct {
	Time   time.Time
	Body   string
	Thread string // optional
}

// Question is an open or resolved question the orchestrator is holding.
type Question struct {
	ID         string // Q1, Q2, ...
	Time       time.Time
	Status     string // open | resolved
	Body       string
	Thread     string // optional
	Note       string // resolution note
	ResolvedAt *time.Time
}

const (
	QuestionOpen     = "open"
	QuestionResolved = "resolved"
)

// RootDir returns the root directory holding all orchestrator subdirectories.
// Honors $CCC_ORCHESTRATOR_ROOT for testing; otherwise ~/.claude/orchestrators.
func RootDir() string {
	if v := os.Getenv("CCC_ORCHESTRATOR_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/orchestrators"
	}
	return filepath.Join(home, ".claude", "orchestrators")
}

// DirFor returns the directory for a single named orchestrator.
func DirFor(name string) string {
	return filepath.Join(RootDir(), name)
}

// StateMDPath returns the path to state.md for an orchestrator.
func StateMDPath(name string) string {
	return filepath.Join(DirFor(name), "state.md")
}

// TranscriptPath returns the path to transcript.md for an orchestrator.
func TranscriptPath(name string) string {
	return filepath.Join(DirFor(name), "transcript.md")
}

// StateLogPath returns the path to state.log for an orchestrator.
func StateLogPath(name string) string {
	return filepath.Join(DirFor(name), "state.log")
}

// LogShPath returns the path to log.sh for an orchestrator.
func LogShPath(name string) string {
	return filepath.Join(DirFor(name), "log.sh")
}

// Exists reports whether an orchestrator directory exists on disk.
func Exists(name string) bool {
	_, err := os.Stat(DirFor(name))
	return err == nil
}

// Init creates the orchestrator's directory and bootstrap files. Idempotent —
// if state.md already exists with status=active, returns nil without changes.
// If the directory is missing files (e.g. state.log was deleted) those are
// recreated, but an existing state.md is never overwritten.
func Init(name, project string) error {
	if name == "" {
		return fmt.Errorf("orchestrator name is required")
	}
	dir := DirFor(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	// Recreate ancillary files if missing. Never touch them if present.
	if _, err := os.Stat(LogShPath(name)); os.IsNotExist(err) {
		if err := os.WriteFile(LogShPath(name), []byte(logShTemplate), 0o755); err != nil {
			return fmt.Errorf("write log.sh: %w", err)
		}
	}
	if _, err := os.Stat(TranscriptPath(name)); os.IsNotExist(err) {
		if err := os.WriteFile(TranscriptPath(name), []byte{}, 0o644); err != nil {
			return fmt.Errorf("write transcript.md: %w", err)
		}
	}
	if _, err := os.Stat(StateLogPath(name)); os.IsNotExist(err) {
		if err := os.WriteFile(StateLogPath(name), []byte{}, 0o644); err != nil {
			return fmt.Errorf("write state.log: %w", err)
		}
	}

	// state.md is created only if missing. Existing content is preserved.
	if _, err := os.Stat(StateMDPath(name)); os.IsNotExist(err) {
		o := &Orchestrator{
			Name:      name,
			Status:    StatusActive,
			Project:   project,
			StartedAt: time.Now().UTC(),
		}
		if err := Save(o); err != nil {
			return fmt.Errorf("write state.md: %w", err)
		}
		if err := AppendStateLog(name, "init"); err != nil {
			return fmt.Errorf("append state.log: %w", err)
		}
	}
	return nil
}

const logShTemplate = `#!/bin/bash
# Append a turn to transcript.md.
# Usage: ./log.sh <speaker> "<message>"
LOGFILE="$(dirname "$0")/transcript.md"
echo "" >> "$LOGFILE"
echo "## $1 — $(date '+%Y-%m-%d %H:%M')" >> "$LOGFILE"
echo "" >> "$LOGFILE"
echo "$2" >> "$LOGFILE"
`

// Load reads and parses state.md for the named orchestrator.
func Load(name string) (*Orchestrator, error) {
	if name == "" {
		return nil, fmt.Errorf("orchestrator name is required")
	}
	data, err := os.ReadFile(StateMDPath(name))
	if err != nil {
		return nil, fmt.Errorf("read state.md: %w", err)
	}
	o, err := parseStateMD(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse state.md: %w", err)
	}
	if o.Name == "" {
		o.Name = name
	}
	return o, nil
}

// Save writes the orchestrator back to state.md in canonical form.
func Save(o *Orchestrator) error {
	if o.Name == "" {
		return fmt.Errorf("orchestrator name is required")
	}
	dir := DirFor(o.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(StateMDPath(o.Name), []byte(renderStateMD(o)), 0o644)
}

// AppendStateLog appends a one-line, timestamped entry to state.log.
func AppendStateLog(name, line string) error {
	f, err := os.OpenFile(StateLogPath(name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open state.log: %w", err)
	}
	defer f.Close()
	stamp := time.Now().UTC().Format(time.RFC3339)
	_, err = fmt.Fprintf(f, "%s %s\n", stamp, line)
	return err
}

// Complete marks an orchestrator as complete. No-op if already complete.
// Returns true if the orchestrator was newly completed (false if already complete).
func Complete(name string) (bool, error) {
	o, err := Load(name)
	if err != nil {
		return false, err
	}
	if o.Status == StatusComplete {
		return false, nil
	}
	now := time.Now().UTC()
	o.Status = StatusComplete
	o.CompletedAt = &now
	if err := Save(o); err != nil {
		return false, err
	}
	if err := AppendStateLog(name, "complete"); err != nil {
		return true, err
	}
	return true, nil
}

// List returns all orchestrators on disk. With includeCompleted=false, only
// orchestrators whose state.md has status=active are returned.
func List(includeCompleted bool) ([]*Orchestrator, error) {
	root := RootDir()
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read root: %w", err)
	}
	var out []*Orchestrator
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		o, err := Load(e.Name())
		if err != nil {
			continue // skip unreadable directories
		}
		if !includeCompleted && o.Status == StatusComplete {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}

// OverlapMatch describes an orchestrator that overlaps with a candidate context.
type OverlapMatch struct {
	Name        string    `json:"name"`
	Project     string    `json:"project"`
	StartedAt   time.Time `json:"started_at"`
	MatchReason string    `json:"match_reason"`
}

// OverlapCheck scans non-complete orchestrators and returns those whose project
// or themes overlap with the supplied context. Project overlap is substring
// match in either direction (orchestrator project contains candidate, or
// candidate contains orchestrator project). Theme overlap matches any theme
// substring against the orchestrator name or notes.
func OverlapCheck(project string, themes []string) ([]OverlapMatch, error) {
	all, err := List(false)
	if err != nil {
		return nil, err
	}
	var matches []OverlapMatch
	for _, o := range all {
		reason := matchReason(o, project, themes)
		if reason == "" {
			continue
		}
		matches = append(matches, OverlapMatch{
			Name:        o.Name,
			Project:     o.Project,
			StartedAt:   o.StartedAt,
			MatchReason: reason,
		})
	}
	return matches, nil
}

func matchReason(o *Orchestrator, project string, themes []string) string {
	if project != "" && o.Project != "" {
		if strings.Contains(o.Project, project) || strings.Contains(project, o.Project) {
			return "project: " + o.Project
		}
	}
	hay := strings.ToLower(o.Name + " " + o.Notes)
	for _, t := range themes {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" {
			continue
		}
		if strings.Contains(hay, t) {
			return "theme: " + t
		}
	}
	return ""
}

// ResolveFromTopic reads the current session's topic file and returns the
// orchestrator name encoded in it. The topic must have the form
// "ORCHESTRATE: <name>". Returns an error if no topic is set or the prefix is
// missing.
//
// Session ID is taken from $CCC_SESSION_ID first (set by ccc when launching a
// session); if that's empty, falls back to ~/.claude/session-topics/pid-<PPID>.map
// which the set-topic skill maintains.
func ResolveFromTopic() (string, error) {
	sessionID, err := currentSessionID()
	if err != nil {
		return "", err
	}
	topicFile := filepath.Join(sessionTopicsDir(), sessionID+".txt")
	data, err := os.ReadFile(topicFile)
	if err != nil {
		return "", fmt.Errorf("session has no topic set (%s): %w", topicFile, err)
	}
	topic := strings.TrimSpace(string(data))
	if !strings.HasPrefix(topic, TopicPrefix) {
		return "", fmt.Errorf("session topic %q does not have %q prefix", topic, TopicPrefix)
	}
	name := strings.TrimSpace(strings.TrimPrefix(topic, TopicPrefix))
	if name == "" {
		return "", fmt.Errorf("session topic has empty orchestrator name")
	}
	return name, nil
}

func sessionTopicsDir() string {
	if v := os.Getenv("CCC_SESSION_TOPICS_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/session-topics"
	}
	return filepath.Join(home, ".claude", "session-topics")
}

func currentSessionID() (string, error) {
	if v := os.Getenv("CCC_SESSION_ID"); v != "" {
		return v, nil
	}
	mapFile := filepath.Join(sessionTopicsDir(), fmt.Sprintf("pid-%d.map", os.Getppid()))
	data, err := os.ReadFile(mapFile)
	if err != nil {
		return "", fmt.Errorf("could not resolve current session id: set CCC_SESSION_ID or run from a Claude session (%w)", err)
	}
	return strings.TrimSpace(string(data)), nil
}
