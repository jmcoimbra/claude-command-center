package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/doctor"
	"github.com/anutron/claude-command-center/internal/external"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/refresh/sources/calendar"
	"github.com/anutron/claude-command-center/internal/refresh/sources/github"
	"github.com/anutron/claude-command-center/internal/refresh/sources/gmail"
	"github.com/anutron/claude-command-center/internal/refresh/sources/granola"
	"github.com/anutron/claude-command-center/internal/tui"
)

func main() {
	forceSetup := false

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--daemon-internal":
			if err := runDaemonInternal(); err != nil {
				fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
				os.Exit(1)
			}
			return
		case "daemon":
			if err := runDaemon(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "register":
			if err := runRegister(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-session":
			if err := runUpdateSession(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "stop-all":
			if err := runStopAll(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "refresh":
			if err := runRefreshCmd(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "doctor":
			fmt.Println("Claude Command Center — Doctor")
			fmt.Println()
			live := false
			for _, a := range os.Args[2:] {
				if a == "--live" {
					live = true
				}
			}
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: could not load config: %v\n", err)
				os.Exit(1)
			}
			pal := config.GetPalette(cfg.Palette, cfg.Colors)
			providers := []plugin.DoctorProvider{
				calendar.NewSettings(cfg, pal, nil),
				gmail.NewDoctor(cfg.Gmail),
				github.NewSettings(cfg, pal, nil),
				granola.NewSettings(cfg, pal),
			}
			if err := doctor.RunDoctor(providers, live); err != nil {
				os.Exit(1)
			}
			return
		case "install-schedule":
			if err := config.InstallSchedule(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "uninstall-schedule":
			if err := config.UninstallSchedule(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "notify":
			event := "reload"
			if len(os.Args) > 2 {
				event = os.Args[2]
			}
			if err := tui.SendNotify(event); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "add-todo":
			if err := runAddTodo(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-todo":
			if err := runUpdateTodo(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "todo":
			if err := runTodo(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "add-bookmark":
			if err := runAddBookmark(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "paths":
			if err := runPaths(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "worktrees":
			if err := runWorktrees(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "console":
			if err := runConsole(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "orchestrator":
			if err := runOrchestrator(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			printUsage()
			return
		case "setup":
			forceSetup = true
			// Fall through to normal TUI launch with onboarding.
		case "sessions":
			// same as default, fall through
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}

	// Detect first run: config file doesn't exist yet.
	_, statErr := os.Stat(config.ConfigPath())
	isFirstRun := os.IsNotExist(statErr)

	// Load config — exit on error to prevent defaults from overwriting the user's file.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not load config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Fix the config file at %s, or remove it to start fresh.\n", config.ConfigPath())
		if bakPath := config.ConfigPath() + ".bak"; fileExists(bakPath) {
			fmt.Fprintf(os.Stderr, "A backup exists at %s\n", bakPath)
		}
		os.Exit(1)
	}

	// Open database (required — TUI is useless without it)
	dbPath := config.DBPath()
	database, err := db.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not open database at %s: %v\n", dbPath, err)
		fmt.Fprintf(os.Stderr, "Run 'ccc setup' to initialize, or check permissions.\n")
		os.Exit(1)
	}
	defer database.Close()

	// Load external plugins (persist across TUI loop iterations).
	bus := plugin.NewBus()
	logPath := filepath.Join(config.DataDir(), "ccc.log")
	logger, err := plugin.NewFileLogger(logPath)
	if err != nil {
		logger = plugin.NewMemoryLogger()
	}
	defer logger.Close()

	// Construct LLM implementation — sandboxed for TUI inline calls
	var l llm.LLM
	if llm.Available() {
		noTools := ""
		l = llm.ClaudeCLI{
			Timeout:              90 * time.Second,
			DisableSlashCommands: true,
			Tools:                &noTools,
		}
	} else {
		l = llm.NoopLLM{}
	}

	extCtx := plugin.Context{
		DB:     database,
		Config: cfg,
		Bus:    bus,
		Logger: logger,
		DBPath: config.DBPath(),
		LLM:    l,
	}
	extPlugins, err := external.LoadExternalPlugins(cfg, extCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load external plugins: %v\n", err)
	}
	var pluginInterfaces []plugin.Plugin
	for _, ep := range extPlugins {
		pluginInterfaces = append(pluginInterfaces, ep)
	}
	// Graceful shutdown on SIGINT/SIGTERM to clean up plugin subprocesses
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		for _, ep := range pluginInterfaces {
			ep.Shutdown()
		}
		os.Exit(0)
	}()
	defer func() {
		signal.Stop(sigCh)
		for _, ep := range pluginInterfaces {
			ep.Shutdown()
		}
	}()

	// TUI loop: launch TUI, optionally exec claude, return to TUI
	returnedFromLaunch := false
	var lastLaunch *tui.LaunchAction
	for {
		m := tui.NewModel(database, cfg, bus, logger, l, pluginInterfaces...)
		if returnedFromLaunch {
			m.SetReturnedFromLaunch()
			if lastLaunch != nil {
				m.SetReturnContext(lastLaunch.ReturnToTodoID, lastLaunch.WasResumeJoin)
			}
		}
		if (isFirstRun || forceSetup) && !returnedFromLaunch {
			m.SetOnboarding()
		}
		// Pre-allocate daemon connection so the pointer is shared with
		// the bubbletea model copy (value-type Model is copied by NewProgram).
		daemonConn := tui.NewDaemonConn(logger, bus)
		m.SetDaemonConn(daemonConn)

		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus(), tea.WithMouseCellMotion())

		// Start unix socket listener for cross-instance notifications
		cleanupNotify := tui.StartNotifyListener(p)

		// Connect to daemon (auto-starts if needed) and subscribe to events.
		daemonConn.Connect(p)

		finalModel, err := p.Run()
		cleanupNotify()
		if err != nil {
			daemonConn.Close()
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fm := finalModel.(tui.Model)
		if fm.Launch == nil {
			// User pressed Esc — exit
			daemonConn.Close()
			break
		}

		lastLaunch = fm.Launch

		// Register the session with the daemon around the claude launch.
		// Generate a placeholder session ID (claude's real session ID is
		// unknown until the hook fires, but this lets us track the process).
		launchSessionID := fm.Launch.SessionID
		if launchSessionID == "" {
			launchSessionID = uuid.New().String()
			fm.Launch.SessionID = launchSessionID
		}
		resolvedDir, err := tui.RunClaude(*fm.Launch, func(pid int) {
			if client := daemonConn.Client(); client != nil {
				regErr := client.RegisterSession(daemon.RegisterSessionParams{
					SessionID: launchSessionID,
					PID:       pid,
					Project:   fm.Launch.Dir,
				})
				if regErr != nil {
					logger.Info("launch", fmt.Sprintf("session register failed: %v", regErr))
				}
			}
		})

		// Mark the session as ended now that claude has exited.
		if client := daemonConn.Client(); client != nil {
			if endErr := client.EndSession(daemon.EndSessionParams{
				SessionID: launchSessionID,
			}); endErr != nil {
				logger.Info("launch", fmt.Sprintf("EndSession failed for %s: %v", launchSessionID[:min(8, len(launchSessionID))], endErr))
			}
		} else {
			logger.Info("launch", "EndSession skipped: daemon connection lost")
		}
		daemonConn.Close()

		// Write the resolved launch directory (may be worktree) so the shell hook can cd to it after exit.
		_ = os.WriteFile(filepath.Join(config.DataDir(), "last-dir"), []byte(resolvedDir), 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Claude error: %v\n", err)
		}
		// Claude exited — loop back to TUI with returnedFromLaunch flag
		returnedFromLaunch = true
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Claude Command Center — Session Launcher & Dashboard")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage: ccc [command]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  (default)            Launch session picker")
	fmt.Fprintln(os.Stderr, "  setup                Run interactive setup wizard")
	fmt.Fprintln(os.Stderr, "  doctor [--live]       Check system health (--live hits network endpoints)")
	fmt.Fprintln(os.Stderr, "  install-schedule     Install launchd plist for background refresh")
	fmt.Fprintln(os.Stderr, "  uninstall-schedule   Remove background refresh schedule")
	fmt.Fprintln(os.Stderr, "  notify [event]       Notify running instances to reload (default: reload)")
	fmt.Fprintln(os.Stderr, "  add-todo             Add a todo to the Command Center")
	fmt.Fprintln(os.Stderr, "  update-todo          Update a todo's session summary or status")
	fmt.Fprintln(os.Stderr, "  todo --get <id>      Get a todo by display ID (JSON output)")
	fmt.Fprintln(os.Stderr, "  todo --fetch-context <id>  Fetch source context for a todo")
	fmt.Fprintln(os.Stderr, "  add-bookmark         Save a session bookmark")
	fmt.Fprintln(os.Stderr, "  paths                List learned project paths (--json, --auto-describe, --add-rule)")
	fmt.Fprintln(os.Stderr, "  worktrees            List CCC-managed git worktrees")
	fmt.Fprintln(os.Stderr, "  worktrees prune      Remove all CCC worktrees (or prune [path] for one repo)")
	fmt.Fprintln(os.Stderr, "  daemon start         Start the background daemon")
	fmt.Fprintln(os.Stderr, "  daemon stop          Stop the background daemon")
	fmt.Fprintln(os.Stderr, "  daemon status        Check daemon status")
	fmt.Fprintln(os.Stderr, "  register             Register a session with the daemon")
	fmt.Fprintln(os.Stderr, "  update-session       Update a session's topic")
	fmt.Fprintln(os.Stderr, "  refresh              Trigger a data refresh via daemon")
	fmt.Fprintln(os.Stderr, "  stop-all             Emergency stop: kill all running agents")
	fmt.Fprintln(os.Stderr, "  console              Live agent streaming dashboard")
	fmt.Fprintln(os.Stderr, "  orchestrator <verb>  Manage orchestrators (run `ccc orchestrator help` for details)")
	fmt.Fprintln(os.Stderr, "  sessions             Same as default")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -h, --help           Show this help")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
