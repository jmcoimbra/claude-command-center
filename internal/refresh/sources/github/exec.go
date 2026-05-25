package github

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// ghCommand builds an exec.Cmd for the gh CLI with GH_TOKEN and GITHUB_TOKEN
// stripped from the environment.
//
// Why: parent processes (Conductor, shell sessions, launchd) often inject a
// stale GH_TOKEN that overrides gh's keyring-stored credential. With a stale
// env token, gh hits 401 on every call; without it, gh falls back to the
// working keyring credential. This mirrors the documented Thanx workaround
// (`GH_TOKEN="" gh ...`) so CCC keeps working regardless of who set the env.
//
// Always pass a non-nil context so the parent can cancel the gh process.
func ghCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = filterGHTokenEnv(os.Environ())
	return cmd
}

// filterGHTokenEnv returns env entries with GH_TOKEN and GITHUB_TOKEN removed.
// Exposed for testing.
func filterGHTokenEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
