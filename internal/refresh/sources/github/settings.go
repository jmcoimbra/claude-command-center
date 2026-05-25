package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ghRepoInfo represents a fetched GitHub repo.
type ghRepoInfo struct {
	NameWithOwner string `json:"nameWithOwner"`
	Description   string `json:"description"`
}

// ghRepoFetchResult is a tea.Msg carrying the result of fetching available repos.
type ghRepoFetchResult struct {
	Repos []ghRepoInfo
	Err   error
}

// Settings implements plugin.SettingsProvider for the GitHub data source.
type Settings struct {
	cfg    *config.Config
	logger plugin.Logger
	styles settingsStyles

	cursor          int
	repoInput       textinput.Model
	repoEditing     bool
	usernameInput   textinput.Model
	usernameEditing bool

	// Browse mode: fetched repos with filter
	fetchedRepos []ghRepoInfo
	fetchLoading bool
	fetchError   string
	fetchMode    bool // browsing fetched repos
	fetchCursor       int
	fetchScrollOffset int
	filterInput       textinput.Model
	filtering         bool // whether filter input is focused
	lastHeight        int  // last known content height for scroll calc
}

type settingsStyles struct {
	header   lipgloss.Style
	muted    lipgloss.Style
	enabled  lipgloss.Style
	disabled lipgloss.Style
	itemName lipgloss.Style
	logError lipgloss.Style
	pointer  lipgloss.Style
}

func newSettingsStyles(pal config.Palette) settingsStyles {
	return settingsStyles{
		header:   lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Cyan)).Bold(true),
		muted:    lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Muted)),
		enabled:  lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Green)),
		disabled: lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Muted)),
		itemName: lipgloss.NewStyle().Foreground(lipgloss.Color(pal.White)),
		logError: lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")),
		pointer:  lipgloss.NewStyle().Foreground(lipgloss.Color(pal.Pointer)),
	}
}

// NewSettings creates a GitHub SettingsProvider.
func NewSettings(cfg *config.Config, pal config.Palette, logger plugin.Logger) *Settings {
	ri := textinput.New()
	ri.Placeholder = "owner/repo"
	ri.CharLimit = 100

	ui := textinput.New()
	ui.Placeholder = "GitHub username"
	ui.CharLimit = 50
	ui.SetValue(cfg.GitHub.Username)

	fi := textinput.New()
	fi.Placeholder = "Type to filter repos..."
	fi.CharLimit = 100
	fi.Width = 40

	return &Settings{
		cfg:           cfg,
		logger:        logger,
		styles:        newSettingsStyles(pal),
		repoInput:     ri,
		usernameInput: ui,
		filterInput:   fi,
	}
}

func (s *Settings) logInfo(msg string, fields ...interface{}) {
	if s.logger != nil {
		s.logger.Info("github", msg, fields...)
	}
}

func (s *Settings) logError(msg string, fields ...interface{}) {
	if s.logger != nil {
		s.logger.Error("github", msg, fields...)
	}
}

// ResetEditing resets editing state when the detail view is opened.
func (s *Settings) ResetEditing() {
	s.cursor = 0
	s.repoEditing = false
	s.usernameEditing = false
	s.usernameInput.SetValue(s.cfg.GitHub.Username)
	s.fetchMode = false
	s.filtering = false
	s.fetchScrollOffset = 0
	s.filterInput.SetValue("")
	s.filterInput.Blur()
}

func (s *Settings) SettingsView(width, height int) string {
	var lines []string

	statusText := "[off]"
	statusStyle := s.styles.disabled
	if s.cfg.GitHub.Enabled {
		statusText = "[on] "
		statusStyle = s.styles.enabled
	}

	lines = append(lines, fmt.Sprintf("  %s %s",
		s.styles.muted.Render("Enabled:"),
		statusStyle.Render(statusText+" (space to toggle)")))

	checks := s.DoctorChecks(plugin.DoctorOpts{})
	credStatus := s.styles.enabled.Render("Authenticated")
	if len(checks) > 0 && checks[0].Result.Status != "ok" {
		credStatus = s.styles.logError.Render("Not authenticated")
	}
	lines = append(lines, fmt.Sprintf("  %s %s",
		s.styles.muted.Render("gh CLI:"),
		credStatus))

	// Username
	username := s.cfg.GitHub.Username
	if username == "" {
		username = "(not set)"
	}
	lines = append(lines, fmt.Sprintf("  %s %s %s",
		s.styles.muted.Render("Username:"),
		s.styles.itemName.Render(username),
		s.styles.muted.Render("(u to edit)")))
	if s.usernameEditing {
		lines = append(lines, "  "+s.usernameInput.View())
	}

	lines = append(lines, "")

	// Track My PRs — prominent section
	lines = append(lines, s.styles.header.Render("  MODE"))
	trackText := "[off]"
	trackStyle := s.styles.disabled
	trackMyPRs := s.cfg.GitHub.IsTrackMyPRs()
	if trackMyPRs {
		trackText = "[on] "
		trackStyle = s.styles.enabled
	}
	lines = append(lines, fmt.Sprintf("  %s %s",
		s.styles.muted.Render("Track My PRs:"),
		trackStyle.Render(trackText+" (t to toggle)")))
	lines = append(lines, s.styles.muted.Render("    Show all PRs assigned, review-requested, or authored by you"))
	if trackMyPRs {
		lines = append(lines, s.styles.muted.Render("    No repo selection needed — all your PRs are tracked automatically"))
	}

	// When Track My PRs is on, hide the repo selection section entirely
	if !trackMyPRs {
		lines = append(lines, "")
		lines = append(lines, s.styles.header.Render("  REPOS"))

		// Browse mode: show fetched repos with filter
		if s.fetchMode {
			s.lastHeight = height
			remainingHeight := height - len(lines)
			if remainingHeight < 5 {
				remainingHeight = 5
			}
			lines = append(lines, s.viewFetchMode(remainingHeight)...)
			return strings.Join(lines, "\n")
		}

		if len(s.cfg.GitHub.Repos) == 0 {
			lines = append(lines, s.styles.muted.Render("  No repos configured"))
		} else {
			for i, repo := range s.cfg.GitHub.Repos {
				cursor := "  "
				if i == s.cursor {
					cursor = s.styles.pointer.Render("> ")
				}
				lines = append(lines, fmt.Sprintf("  %s%s", cursor, s.styles.itemName.Render(repo)))
			}
		}

		if s.repoEditing {
			lines = append(lines, "  "+s.repoInput.View())
		}

		lines = append(lines, "")

		// Show fetch status hint
		if s.fetchLoading {
			lines = append(lines, s.styles.muted.Render("  Fetching repos from GitHub..."))
			lines = append(lines, "")
		} else if s.fetchError != "" {
			lines = append(lines, s.styles.logError.Render("  Fetch error: "+s.fetchError))
			lines = append(lines, "")
		}
	}

	// Key hints
	lines = append(lines, "")
	if trackMyPRs {
		lines = append(lines, s.styles.muted.Render("  t track my PRs · u username"))
	} else {
		hintParts := "  t track my PRs · a add repo · x remove · u username"
		if len(s.fetchedRepos) > 0 {
			hintParts += " · f browse repos"
		} else if !s.fetchLoading && s.fetchError == "" {
			hintParts += " · f fetch from GitHub"
		}
		lines = append(lines, s.styles.muted.Render(hintParts))
	}

	return strings.Join(lines, "\n")
}

// filteredRepos returns the subset of fetchedRepos matching the current filter.
func (s *Settings) filteredRepos() []ghRepoInfo {
	query := strings.ToLower(strings.TrimSpace(s.filterInput.Value()))
	if query == "" {
		return s.fetchedRepos
	}
	var out []ghRepoInfo
	for _, r := range s.fetchedRepos {
		if strings.Contains(strings.ToLower(r.NameWithOwner), query) ||
			strings.Contains(strings.ToLower(r.Description), query) {
			out = append(out, r)
		}
	}
	return out
}

// isRepoConfigured returns true if the given repo name is already tracked.
func (s *Settings) isRepoConfigured(nameWithOwner string) bool {
	for _, r := range s.cfg.GitHub.Repos {
		if strings.EqualFold(r, nameWithOwner) {
			return true
		}
	}
	return false
}

func (s *Settings) viewFetchMode(availableHeight int) []string {
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  "+s.filterInput.View())
	lines = append(lines, "")

	filtered := s.filteredRepos()

	if len(filtered) == 0 {
		if s.filterInput.Value() != "" {
			lines = append(lines, s.styles.muted.Render("  No repos match filter"))
		} else {
			lines = append(lines, s.styles.muted.Render("  No repos found"))
		}
	} else {
		// Calculate how many repo lines fit in the available space.
		// We use: 3 lines above (blank + filter + blank), plus 3 lines below
		// (blank + count + hints). The rest is for repo items.
		chrome := 3 + 3 // filter area + footer area
		maxVisible := availableHeight - chrome
		if maxVisible < 3 {
			maxVisible = 3
		}
		// Hard cap to prevent the list from dominating the screen.
		const maxVisibleCap = 8
		if maxVisible > maxVisibleCap {
			maxVisible = maxVisibleCap
		}
		if maxVisible > len(filtered) {
			maxVisible = len(filtered)
		}

		// Ensure scroll offset keeps cursor visible.
		s.clampFetchScroll(len(filtered), maxVisible)

		visStart := s.fetchScrollOffset
		visEnd := s.fetchScrollOffset + maxVisible
		if visEnd > len(filtered) {
			visEnd = len(filtered)
		}

		if visStart > 0 {
			lines = append(lines, s.styles.muted.Render(fmt.Sprintf("  ▲ %d more above", visStart)))
		}

		for i := visStart; i < visEnd; i++ {
			repo := filtered[i]
			cursor := "  "
			if i == s.fetchCursor {
				cursor = s.styles.pointer.Render("> ")
			}

			configured := s.isRepoConfigured(repo.NameWithOwner)
			toggle := s.styles.disabled.Render("[ ] ")
			if configured {
				toggle = s.styles.enabled.Render("[+] ")
			}

			nameStyle := s.styles.itemName
			if configured {
				nameStyle = s.styles.enabled
			}

			desc := ""
			if repo.Description != "" {
				desc = " " + s.styles.muted.Render(repo.Description)
			}

			lines = append(lines, fmt.Sprintf("  %s%s%s", cursor, toggle, nameStyle.Render(repo.NameWithOwner)+desc))
		}

		if visEnd < len(filtered) {
			lines = append(lines, s.styles.muted.Render(fmt.Sprintf("  ▼ %d more below", len(filtered)-visEnd)))
		}
	}

	lines = append(lines, "")
	countInfo := fmt.Sprintf("  %d repos", len(filtered))
	if len(filtered) != len(s.fetchedRepos) {
		countInfo += fmt.Sprintf(" (of %d total)", len(s.fetchedRepos))
	}
	lines = append(lines, s.styles.muted.Render(countInfo))
	lines = append(lines, s.styles.muted.Render("  / filter · ↑↓ navigate · space toggle · esc back"))

	return lines
}

// clampFetchScroll adjusts fetchScrollOffset so fetchCursor is visible.
func (s *Settings) clampFetchScroll(total, maxVisible int) {
	if s.fetchCursor < s.fetchScrollOffset {
		s.fetchScrollOffset = s.fetchCursor
	}
	if s.fetchCursor >= s.fetchScrollOffset+maxVisible {
		s.fetchScrollOffset = s.fetchCursor - maxVisible + 1
	}
	if s.fetchScrollOffset < 0 {
		s.fetchScrollOffset = 0
	}
	if s.fetchScrollOffset > total-maxVisible {
		s.fetchScrollOffset = total - maxVisible
	}
	if s.fetchScrollOffset < 0 {
		s.fetchScrollOffset = 0
	}
}

// ghUserFetchResult is the message returned by the async username fetch.
type ghUserFetchResult struct {
	Login string
	Err   error
}

// fetchGHUsername is a variable for testability.
var fetchGHUsername = func() (string, error) {
	out, err := ghCommand(context.Background(), "api", "/user").Output()
	if err != nil {
		return "", err
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(out, &user); err != nil {
		return "", fmt.Errorf("parse /user response: %w", err)
	}
	if user.Login == "" {
		return "", fmt.Errorf("empty login returned")
	}
	return user.Login, nil
}

// fetchGHRepoList fetches repos for a given owner (empty string = authenticated user).
func fetchGHRepoList(owner string) ([]ghRepoInfo, error) {
	args := []string{"repo", "list"}
	if owner != "" {
		args = append(args, owner)
	}
	args = append(args, "--json", "nameWithOwner,description", "--limit", "200")
	cmd := ghCommand(context.Background(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh repo list %s: %s", owner, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh repo list %s: %w", owner, err)
	}
	var repos []ghRepoInfo
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("parse repo list %s: %w", owner, err)
	}
	return repos, nil
}

// fetchGHOrgs returns the login names of orgs the authenticated user belongs to.
func fetchGHOrgs() ([]string, error) {
	cmd := ghCommand(context.Background(), "api", "user/orgs", "--jq", ".[].login")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var orgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			orgs = append(orgs, line)
		}
	}
	return orgs, nil
}

// fetchGHRepos is a variable for testability.
var fetchGHRepos = func() ([]ghRepoInfo, error) {
	// Fetch personal repos.
	repos, err := fetchGHRepoList("")
	if err != nil {
		return nil, err
	}

	// Fetch org memberships and their repos.
	orgs, orgErr := fetchGHOrgs()
	if orgErr == nil {
		for _, org := range orgs {
			orgRepos, err := fetchGHRepoList(org)
			if err != nil {
				continue // Skip orgs that fail (permissions, etc.)
			}
			repos = append(repos, orgRepos...)
		}
	}

	// Deduplicate by nameWithOwner (case-insensitive).
	seen := make(map[string]bool, len(repos))
	deduped := make([]ghRepoInfo, 0, len(repos))
	for _, r := range repos {
		key := strings.ToLower(r.NameWithOwner)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, r)
		}
	}

	// Sort by nameWithOwner.
	sort.Slice(deduped, func(i, j int) bool {
		return strings.ToLower(deduped[i].NameWithOwner) < strings.ToLower(deduped[j].NameWithOwner)
	})

	return deduped, nil
}

func (s *Settings) SettingsOpenCmd() tea.Cmd {
	// Only fetch if authenticated and username is not already set.
	if s.cfg.GitHub.Username != "" {
		s.logInfo("settings opened, username already set", "username", s.cfg.GitHub.Username)
		return nil
	}
	// Quick check: is gh authenticated?
	if err := ghCommand(context.Background(), "auth", "token").Run(); err != nil {
		s.logError("gh CLI not authenticated, skipping username auto-fetch")
		return nil
	}
	s.logInfo("auto-fetching GitHub username via gh API")
	return func() tea.Msg {
		login, err := fetchGHUsername()
		return ghUserFetchResult{Login: login, Err: err}
	}
}

func (s *Settings) HandleSettingsMsg(msg tea.Msg) (bool, plugin.Action) {
	switch msg := msg.(type) {
	case ghUserFetchResult:
		if msg.Err != nil {
			s.logError("username auto-fetch failed", "error", msg.Err)
			return true, plugin.Action{Type: plugin.ActionFlash, Payload: "Could not auto-fetch GitHub username: " + msg.Err.Error()}
		}
		// Only set if still empty (user may have typed one in the meantime).
		if s.cfg.GitHub.Username == "" {
			s.cfg.GitHub.Username = msg.Login
			s.usernameInput.SetValue(msg.Login)
			config.Save(s.cfg)
			s.logInfo("username auto-detected", "login", msg.Login)
			return true, plugin.Action{Type: plugin.ActionFlash, Payload: "GitHub username auto-detected: " + msg.Login}
		}
		s.logInfo("username auto-fetch returned but username already set", "fetched", msg.Login, "current", s.cfg.GitHub.Username)
		return true, plugin.NoopAction()
	case ghRepoFetchResult:
		s.fetchLoading = false
		if msg.Err != nil {
			s.fetchError = msg.Err.Error()
			s.logError("repo fetch failed", "error", msg.Err)
		} else {
			s.fetchedRepos = msg.Repos
			s.fetchError = ""
			// Auto-enter browse mode when repos arrive
			s.fetchMode = true
			s.fetchCursor = 0
			s.fetchScrollOffset = 0
			s.filterInput.SetValue("")
			s.filterInput.Focus()
			s.filtering = true
			s.logInfo("repos fetched", "count", len(msg.Repos))
		}
		return true, plugin.NoopAction()
	}
	return false, plugin.NoopAction()
}

// DoctorChecks implements plugin.DoctorProvider for GitHub.
func (s *Settings) DoctorChecks(opts plugin.DoctorOpts) []plugin.DoctorCheck {
	check := plugin.DoctorCheck{Name: "GitHub CLI"}

	cmd := ghCommand(context.Background(), "auth", "token")
	if err := cmd.Run(); err != nil {
		check.Result = plugin.ValidationResult{
			Status:  "missing",
			Message: "GitHub CLI not authenticated",
			Hint:    "Run 'gh auth login' to authenticate",
		}
		s.logError("doctor check: gh CLI not authenticated")
	} else {
		check.Result = plugin.ValidationResult{
			Status:  "ok",
			Message: "GitHub CLI authenticated",
		}
		s.logInfo("doctor check: gh CLI authenticated")
	}

	return []plugin.DoctorCheck{check}
}

func (s *Settings) HandleSettingsKey(msg tea.KeyMsg) plugin.Action {
	// If editing a text input, route keys there
	if s.repoEditing {
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(s.repoInput.Value())
			if val != "" {
				s.cfg.GitHub.Repos = append(s.cfg.GitHub.Repos, val)
				config.Save(s.cfg)
				s.logInfo("repo added", "repo", val)
			}
			s.repoInput.SetValue("")
			s.repoEditing = false
			s.repoInput.Blur()
			if val != "" {
				return plugin.Action{Type: plugin.ActionFlash, Payload: "Added repo: " + val}
			}
			return plugin.NoopAction()
		case "esc":
			s.repoEditing = false
			s.repoInput.Blur()
			return plugin.NoopAction()
		}
		s.repoInput, _ = s.repoInput.Update(msg)
		return plugin.NoopAction()
	}

	if s.usernameEditing {
		switch msg.String() {
		case "enter":
			s.cfg.GitHub.Username = strings.TrimSpace(s.usernameInput.Value())
			config.Save(s.cfg)
			s.usernameEditing = false
			s.usernameInput.Blur()
			s.logInfo("username saved", "username", s.cfg.GitHub.Username)
			return plugin.Action{Type: plugin.ActionFlash, Payload: "Username saved"}
		case "esc":
			s.usernameEditing = false
			s.usernameInput.Blur()
			return plugin.NoopAction()
		}
		s.usernameInput, _ = s.usernameInput.Update(msg)
		return plugin.NoopAction()
	}

	// Browse/fetch mode
	if s.fetchMode {
		return s.handleFetchKey(msg)
	}

	switch msg.String() {
	case "t":
		newVal := !s.cfg.GitHub.IsTrackMyPRs()
		s.cfg.GitHub.SetTrackMyPRs(newVal)
		config.Save(s.cfg)
		label := "disabled"
		if newVal {
			label = "enabled"
		}
		s.logInfo("track my PRs toggled", "enabled", newVal)
		return plugin.Action{Type: plugin.ActionFlash, Payload: "Track My PRs " + label}
	case "a":
		// Ignore repo-add when Track My PRs is on (repos section hidden)
		if s.cfg.GitHub.IsTrackMyPRs() {
			return plugin.NoopAction()
		}
		s.repoEditing = true
		s.repoInput.Focus()
		return plugin.NoopAction()
	case "u":
		s.usernameEditing = true
		s.usernameInput.SetValue(s.cfg.GitHub.Username)
		s.usernameInput.Focus()
		return plugin.NoopAction()
	case "r":
		// Refresh: re-check credentials and re-fetch username if empty.
		s.logInfo("refresh triggered")
		var cmds []tea.Cmd
		// Re-fetch username if empty and gh is authenticated.
		if s.cfg.GitHub.Username == "" {
			if err := ghCommand(context.Background(), "auth", "token").Run(); err == nil {
				s.logInfo("refreshing: fetching username")
				cmds = append(cmds, func() tea.Msg {
					login, err := fetchGHUsername()
					return ghUserFetchResult{Login: login, Err: err}
				})
			}
		}
		if len(cmds) > 0 {
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
		}
		// If username is already set, just let the datasource-level r handler
		// do the credential recheck (return unhandled so it falls through).
		return plugin.Action{Type: plugin.ActionUnhandled}
	case "f":
		// Ignore fetch/browse when Track My PRs is on (repos section hidden)
		if s.cfg.GitHub.IsTrackMyPRs() {
			return plugin.NoopAction()
		}
		// Enter fetch/browse mode
		if len(s.fetchedRepos) > 0 {
			s.logInfo("entering repo browse mode")
			s.fetchMode = true
			s.fetchCursor = 0
			s.fetchScrollOffset = 0
			s.filterInput.SetValue("")
			s.filterInput.Focus()
			s.filtering = true
			return plugin.NoopAction()
		}
		// Trigger a fresh fetch
		if err := ghCommand(context.Background(), "auth", "token").Run(); err != nil {
			s.logError("repo fetch aborted: gh CLI not authenticated")
			return plugin.Action{Type: plugin.ActionFlash, Payload: "GitHub CLI not authenticated"}
		}
		s.fetchLoading = true
		s.fetchError = ""
		s.logInfo("fetching repos from GitHub")
		cmd := func() tea.Msg {
			repos, err := fetchGHRepos()
			return ghRepoFetchResult{Repos: repos, Err: err}
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "x", "d":
		// Ignore repo-remove when Track My PRs is on (repos section hidden)
		if s.cfg.GitHub.IsTrackMyPRs() {
			return plugin.NoopAction()
		}
		if s.cursor < len(s.cfg.GitHub.Repos) {
			removed := s.cfg.GitHub.Repos[s.cursor]
			s.cfg.GitHub.Repos = append(
				s.cfg.GitHub.Repos[:s.cursor],
				s.cfg.GitHub.Repos[s.cursor+1:]...,
			)
			config.Save(s.cfg)
			if s.cursor >= len(s.cfg.GitHub.Repos) && s.cursor > 0 {
				s.cursor--
			}
			s.logInfo("repo removed", "repo", removed)
			return plugin.Action{Type: plugin.ActionFlash, Payload: "Removed: " + removed}
		}
		return plugin.NoopAction()
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		return plugin.NoopAction()
	case "down", "j":
		if s.cursor < len(s.cfg.GitHub.Repos)-1 {
			s.cursor++
		}
		return plugin.NoopAction()
	}

	return plugin.Action{Type: plugin.ActionUnhandled}
}

func (s *Settings) handleFetchKey(msg tea.KeyMsg) plugin.Action {
	filtered := s.filteredRepos()

	// When filter input is focused, most keys go to the text input
	if s.filtering {
		switch msg.String() {
		case "esc":
			if s.filterInput.Value() != "" {
				// First esc clears filter
				s.filterInput.SetValue("")
				s.fetchCursor = 0
				s.fetchScrollOffset = 0
				return plugin.NoopAction()
			}
			// Second esc exits browse mode
			s.fetchMode = false
			s.filtering = false
			s.filterInput.Blur()
			return plugin.NoopAction()
		case "enter":
			// Switch from filter to navigation mode
			s.filtering = false
			s.filterInput.Blur()
			s.fetchCursor = 0
			s.fetchScrollOffset = 0
			return plugin.NoopAction()
		case "down":
			// Switch to navigation
			s.filtering = false
			s.filterInput.Blur()
			s.fetchCursor = 0
			s.fetchScrollOffset = 0
			return plugin.NoopAction()
		default:
			oldVal := s.filterInput.Value()
			s.filterInput, _ = s.filterInput.Update(msg)
			// Reset cursor and scroll if filter changed
			if s.filterInput.Value() != oldVal {
				s.fetchCursor = 0
				s.fetchScrollOffset = 0
			}
			return plugin.NoopAction()
		}
	}

	// Navigation mode (filter not focused)
	switch msg.String() {
	case "up", "k":
		if s.fetchCursor > 0 {
			s.fetchCursor--
		}
		return plugin.NoopAction()
	case "down", "j":
		if s.fetchCursor < len(filtered)-1 {
			s.fetchCursor++
		}
		return plugin.NoopAction()
	case " ", "enter":
		if s.fetchCursor >= len(filtered) {
			return plugin.NoopAction()
		}
		repo := filtered[s.fetchCursor]
		if s.isRepoConfigured(repo.NameWithOwner) {
			// Remove from config
			out := s.cfg.GitHub.Repos[:0]
			for _, r := range s.cfg.GitHub.Repos {
				if !strings.EqualFold(r, repo.NameWithOwner) {
					out = append(out, r)
				}
			}
			s.cfg.GitHub.Repos = out
			if s.cursor >= len(s.cfg.GitHub.Repos) && s.cursor > 0 {
				s.cursor = len(s.cfg.GitHub.Repos) - 1
			}
			config.Save(s.cfg)
			return plugin.Action{Type: plugin.ActionFlash, Payload: "Removed: " + repo.NameWithOwner}
		}
		// Add to config
		s.cfg.GitHub.Repos = append(s.cfg.GitHub.Repos, repo.NameWithOwner)
		config.Save(s.cfg)
		return plugin.Action{Type: plugin.ActionFlash, Payload: "Added: " + repo.NameWithOwner}
	case "/":
		// Focus the filter input
		s.filtering = true
		s.filterInput.Focus()
		return plugin.NoopAction()
	case "esc":
		s.fetchMode = false
		s.filtering = false
		s.filterInput.Blur()
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}
