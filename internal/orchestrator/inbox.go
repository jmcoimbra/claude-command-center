package orchestrator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Message is one line in inbox.jsonl. Required fields: ID, TS, From, To, Kind, Body.
// Optional fields carry handoff/checkin metadata.
//
// JSON tags are stable; lowercase snake_case to match the on-disk format
// documented in specs/core/orchestrator.md.
type Message struct {
	ID        int64     `json:"id"`
	TS        time.Time `json:"ts"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Topic     string    `json:"topic,omitempty"`
	Project   string    `json:"project,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Worktree  string    `json:"worktree,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

// Recipient sentinels.
const (
	RecipientOrchestrator = "orchestrator"
	RecipientBroadcast    = "*"
)

// Inbox kinds. Freeform additions are allowed; readers must tolerate unknowns.
const (
	KindHandoff   = "handoff"
	KindCheckin   = "checkin"
	KindUpdate    = "update"
	KindQuestion  = "question"
	KindPasteBack = "paste-back"
)

// InboxPath returns the path to inbox.jsonl for an orchestrator.
func InboxPath(name string) string {
	return filepath.Join(DirFor(name), "inbox.jsonl")
}

// CursorsPath returns the path to cursors.json for an orchestrator.
func CursorsPath(name string) string {
	return filepath.Join(DirFor(name), "cursors.json")
}

// AppendMessage assigns an ID and timestamp to msg, appends it as a JSON line
// to inbox.jsonl, and returns the assigned ID. Callers should leave msg.ID and
// msg.TS zero — they are filled in here. To, From, Kind, and Body must be set.
func AppendMessage(name string, msg Message) (int64, error) {
	if name == "" {
		return 0, fmt.Errorf("orchestrator name is required")
	}
	if msg.To == "" {
		return 0, fmt.Errorf("message.to is required")
	}
	if msg.From == "" {
		return 0, fmt.Errorf("message.from is required")
	}
	if msg.Kind == "" {
		return 0, fmt.Errorf("message.kind is required")
	}
	if err := os.MkdirAll(DirFor(name), 0o755); err != nil {
		return 0, fmt.Errorf("create dir: %w", err)
	}

	// Determine next id by scanning existing messages. Two near-simultaneous
	// writers could collide on the same id; readers tolerate this by treating
	// the inbox as append-ordered. This matches the no-locks style used
	// elsewhere in the package.
	existing, err := ListMessages(name)
	if err != nil {
		return 0, err
	}
	var maxID int64
	for _, m := range existing {
		if m.ID > maxID {
			maxID = m.ID
		}
	}
	msg.ID = maxID + 1
	if msg.TS.IsZero() {
		msg.TS = time.Now().UTC()
	}

	line, err := json.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("marshal message: %w", err)
	}
	f, err := os.OpenFile(InboxPath(name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open inbox: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return 0, fmt.Errorf("write inbox: %w", err)
	}
	return msg.ID, nil
}

// ListMessages reads inbox.jsonl and returns every message in append order.
// Returns an empty slice (not an error) if the file does not yet exist.
// Malformed lines are skipped rather than failing the read — the inbox is
// human-tailable and a hand-edit shouldn't take down the CLI.
func ListMessages(name string) ([]Message, error) {
	f, err := os.Open(InboxPath(name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open inbox: %w", err)
	}
	defer f.Close()
	var out []Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return out, fmt.Errorf("scan inbox: %w", err)
	}
	return out, nil
}

// MessageFilter narrows a ListMessages result. Empty fields are ignored.
type MessageFilter struct {
	To     string
	From   string
	Kind   string
	Unread bool // requires To; pulls cursor from cursors.json
}

// FilterMessages applies a filter to a slice of messages.
// When Unread is true, the caller must supply the cursor (FilterMessages does
// not read cursors.json itself — that's UnreadFor's job).
func FilterMessages(msgs []Message, f MessageFilter, cursor int64) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if f.To != "" {
			if !(m.To == f.To || m.To == RecipientBroadcast) {
				continue
			}
		}
		if f.From != "" && m.From != f.From {
			continue
		}
		if f.Kind != "" && m.Kind != f.Kind {
			continue
		}
		if f.Unread && m.ID <= cursor {
			continue
		}
		out = append(out, m)
	}
	return out
}

// UnreadFor returns messages addressed to recipient (or broadcast) with id
// greater than the recipient's cursor. Convenience wrapper around ListMessages
// + ReadCursor + FilterMessages.
func UnreadFor(name, recipient string) ([]Message, error) {
	all, err := ListMessages(name)
	if err != nil {
		return nil, err
	}
	cursor, err := ReadCursor(name, recipient)
	if err != nil {
		return nil, err
	}
	return FilterMessages(all, MessageFilter{To: recipient, Unread: true}, cursor), nil
}

// ReadCursor returns the recipient's read cursor (highest id marked read).
// Missing recipient or missing file returns 0.
func ReadCursor(name, recipient string) (int64, error) {
	cursors, err := readCursors(name)
	if err != nil {
		return 0, err
	}
	return cursors[recipient], nil
}

// SetCursor sets the recipient's cursor to id, writing cursors.json atomically.
// If id is 0 and there are messages in the inbox, the cursor is set to the
// highest message id (mark all current messages read).
func SetCursor(name, recipient string, id int64) error {
	if recipient == "" {
		return fmt.Errorf("recipient is required")
	}
	cursors, err := readCursors(name)
	if err != nil {
		return err
	}
	if id == 0 {
		msgs, err := ListMessages(name)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if m.ID > id {
				id = m.ID
			}
		}
	}
	if cursors == nil {
		cursors = map[string]int64{}
	}
	cursors[recipient] = id
	return writeCursors(name, cursors)
}

func readCursors(name string) (map[string]int64, error) {
	data, err := os.ReadFile(CursorsPath(name))
	if os.IsNotExist(err) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cursors: %w", err)
	}
	cursors := map[string]int64{}
	if len(data) == 0 {
		return cursors, nil
	}
	if err := json.Unmarshal(data, &cursors); err != nil {
		return nil, fmt.Errorf("parse cursors: %w", err)
	}
	return cursors, nil
}

func writeCursors(name string, cursors map[string]int64) error {
	if err := os.MkdirAll(DirFor(name), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	data, err := json.MarshalIndent(cursors, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}
	tmp := CursorsPath(name) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write cursors tmp: %w", err)
	}
	if err := os.Rename(tmp, CursorsPath(name)); err != nil {
		return fmt.Errorf("rename cursors: %w", err)
	}
	return nil
}

// RoleMatch identifies a thread inside a specific orchestrator.
type RoleMatch struct {
	Orchestrator string `json:"orchestrator"`
	Role         string `json:"role"`
	Project      string `json:"project"`
	Worktree     string `json:"worktree"`
}

// ResolveRole searches all active orchestrators for threads whose worktree
// (or, as fallback, project) matches the supplied paths. Empty inputs are
// ignored. Threads whose status is "complete" are excluded unless
// includeCompleted is true. Returns all matches; callers decide how to
// disambiguate.
func ResolveRole(worktree, project string, includeCompleted bool) ([]RoleMatch, error) {
	orchs, err := List(false)
	if err != nil {
		return nil, err
	}
	var matches []RoleMatch
	for _, o := range orchs {
		for _, t := range o.Threads {
			if !includeCompleted && t.Status == StatusComplete {
				continue
			}
			role := t.Role
			if role == "" {
				role = t.Name
			}
			if matchesPath(t.Worktree, worktree) {
				matches = append(matches, RoleMatch{
					Orchestrator: o.Name,
					Role:         role,
					Project:      t.Project,
					Worktree:     t.Worktree,
				})
				continue
			}
			if t.Worktree == "" && matchesPath(t.Project, project) {
				matches = append(matches, RoleMatch{
					Orchestrator: o.Name,
					Role:         role,
					Project:      t.Project,
					Worktree:     t.Worktree,
				})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Orchestrator != matches[j].Orchestrator {
			return matches[i].Orchestrator < matches[j].Orchestrator
		}
		return matches[i].Role < matches[j].Role
	})
	return matches, nil
}

func matchesPath(threadPath, queryPath string) bool {
	if threadPath == "" || queryPath == "" {
		return false
	}
	return filepath.Clean(threadPath) == filepath.Clean(queryPath)
}
