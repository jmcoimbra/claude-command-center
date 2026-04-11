// Package agent provides a shared runner for managing headless Claude Code
// agent sessions. It handles process lifecycle, concurrency limiting, queue
// management, and stream-JSON parsing. It is generic — no plugin-specific or
// command-center-specific logic lives here.
package agent

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// CostCallback is invoked when token usage is extracted from an agent session's
// native log. It receives the input/output token counts and estimated USD cost.
type CostCallback func(inputTokens, outputTokens int, costUSD float64)

// Runner manages agent session lifecycle.
type Runner interface {
	// LaunchOrQueue either launches an agent immediately (if under the
	// concurrency limit) or queues it for later. Returns a tea.Cmd that
	// will emit SessionStartedMsg when the process starts, or nil if queued.
	LaunchOrQueue(req Request) (queued bool, cmd tea.Cmd)

	// Kill terminates a running session. Returns true if a session was found and killed.
	Kill(id string) bool

	// SendMessage writes a user message to a running session's stdin.
	SendMessage(id string, message string) error

	// Status returns the current state of a session, or nil if unknown.
	Status(id string) *SessionStatus

	// Active returns info about all currently running sessions.
	Active() []SessionInfo

	// QueueLen returns the number of sessions waiting in the queue.
	QueueLen() int

	// Session returns the raw session handle for a running agent, or nil.
	// This is needed for the session viewer to access the event channel.
	Session(id string) *Session

	// CheckProcesses polls active sessions for completion and status changes.
	// Called on tick to detect finished processes. Returns tea.Msgs via tea.Cmd.
	CheckProcesses() tea.Cmd

	// DrainQueue pops the next queued request (if any) when there is capacity.
	// Returns the request and true, or zero value and false if nothing to drain.
	DrainQueue() (Request, bool)

	// CleanupFinished removes a finished session from the active map and returns
	// it for summary extraction. Call this after receiving SessionFinishedMsg.
	CleanupFinished(id string) *Session

	// Watch returns a tea.Cmd that opens a live tail of the running session's
	// log file. Returns nil if the session is not found or has no log path.
	Watch(id string) tea.Cmd

	// Shutdown sends SIGINT to all active sessions and waits briefly for exit.
	Shutdown()
}

// Request describes an agent to spawn.
type Request struct {
	ID         string  // unique identifier for this request (e.g. todo ID)
	Prompt     string  // the prompt to send to the agent
	ProjectDir string  // working directory for the agent process
	Worktree   bool    // if true, pass --worktree flag
	Permission string  // permission mode (e.g. "default", "plan", "auto")
	Budget       float64      // max budget in USD (passed if >= 0.50)
	ResumeID     string       // if set, resume an existing session
	AutoStart    bool         // if true, auto-launch when dequeued
	Automation   string        // which automation triggered this agent (e.g. "pr-review")
	MaxRuntime   time.Duration // kill the agent after this duration (0 = no limit)
	CostCallback CostCallback  // optional callback for token usage updates
}

// Session is the handle for a running agent process.
type Session struct {
	ID        string
	SessionID string // Claude session UUID, captured from first stream-JSON event
	Cmd       *exec.Cmd
	Status    string // "processing", "blocked"
	Question  string // populated when blocked
	StartedAt time.Time
	LogPath   string

	// Pty is the PTY master file descriptor when the session is launched via PTY.
	Pty *os.File

	// StdinWriter is the writable end of the stdin pipe for interactive sessions.
	// Only set for resume sessions (where Pty is also set) or future interactive modes.
	// Nil for -p mode sessions, which are non-interactive.
	StdinWriter io.WriteCloser

	// Events tracks parsed session events for the live viewer.
	Events   []SessionEvent
	EventsCh chan SessionEvent

	// done is closed when the process exits. ExitCode is set before closing.
	done     chan struct{}
	exitCode int

	// output captures raw stream-JSON output for summary extraction.
	output stringBuilder

	// mu protects Status, Question, SessionID, Events, output, and exitCode.
	Mu sync.Mutex
}

// stringBuilder is a minimal interface satisfied by strings.Builder.
type stringBuilder interface {
	WriteString(s string) (int, error)
	String() string
}

// Done returns the channel that is closed when the session's process exits.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// ExitCode returns the process exit code. Only valid after Done() is closed.
func (s *Session) ExitCode() int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.exitCode
}

// Output returns the accumulated stream-JSON output.
func (s *Session) Output() string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.output.String()
}

// SessionStatus is the current state of a running/completed agent.
type SessionStatus struct {
	ID        string
	Status    string // "queued", "running", "completed", "failed", "blocked"
	SessionID string
	Question  string
	StartedAt time.Time
}

// SessionInfo is a lightweight view for listing active sessions.
type SessionInfo struct {
	ID        string
	Status    string
	SessionID string
	StartedAt time.Time
}

// SessionEvent represents a parsed event from the Claude CLI stream-json output.
type SessionEvent struct {
	Timestamp    string // raw timestamp from the event, if present
	Type         string // assistant_text, tool_use, tool_result, error, user, system
	Text         string // text content for assistant_text and error types
	ToolName     string // tool name for tool_use events
	ToolInput    string // truncated tool input for tool_use events
	ToolID       string // tool_use id for correlating with results
	ResultToolID string // tool_use_id from tool_result events
	ResultText   string // content from tool_result events
	IsError      bool   // true if tool_result is an error or event is an error type
}

// Tea messages emitted by the runner.

// SessionStartedMsg is sent when an agent process has been started.
type SessionStartedMsg struct {
	ID      string
	Session *Session
}

// SessionFinishedMsg is sent when an agent process exits.
type SessionFinishedMsg struct {
	ID       string
	ExitCode int
}

// SessionIDCapturedMsg is sent when the Claude session UUID is captured.
type SessionIDCapturedMsg struct {
	ID        string
	SessionID string
}

// SessionBlockedMsg is sent when an agent becomes blocked waiting for input.
type SessionBlockedMsg struct {
	ID       string
	Question string
}

// SessionEventMsg carries a single parsed event from the agent's stdout.
type SessionEventMsg struct {
	ID    string
	Event SessionEvent
}

// SessionEventsDoneMsg signals that the event channel for a session has closed.
type SessionEventsDoneMsg struct {
	ID string
}
