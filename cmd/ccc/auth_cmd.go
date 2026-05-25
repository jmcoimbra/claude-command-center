package main

import (
	"fmt"

	"github.com/anutron/claude-command-center/internal/refresh/sources/calendar"
)

// runAuthCmd dispatches `ccc auth <subcommand>` and runs the matching OAuth
// flow without requiring the user to enter the TUI. Calendar is the first
// supported subcommand; Gmail can be added later with the same pattern.
//
//	ccc auth calendar    Trigger the Google Calendar OAuth consent flow.
//	                     Opens the user's default browser. After consent,
//	                     writes tokens to ~/.config/google-calendar-mcp/credentials.json.
func runAuthCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ccc auth <calendar>\n\nAvailable subcommands:\n  calendar    Run Google Calendar OAuth flow")
	}
	switch args[0] {
	case "calendar":
		fmt.Println("Starting Google Calendar OAuth flow.")
		fmt.Println("Your default browser will open with the Google consent screen.")
		fmt.Println("Approve to write tokens to ~/.config/google-calendar-mcp/credentials.json.")
		fmt.Println()
		if err := calendar.RunCalendarAuth(); err != nil {
			return fmt.Errorf("calendar auth: %w", err)
		}
		fmt.Println("Calendar auth complete.")
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q. Available: calendar", args[0])
	}
}
