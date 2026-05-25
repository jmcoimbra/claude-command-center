package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Name            string        `yaml:"name"`
	HomeDir         string        `yaml:"home_dir,omitempty"`
	Subtitle        string        `yaml:"subtitle,omitempty"`
	ShowBanner      *bool         `yaml:"show_banner,omitempty"`
	BannerTopPadding *int        `yaml:"banner_top_padding,omitempty"`
	Palette         string        `yaml:"palette"`
	Colors          *CustomColors `yaml:"colors,omitempty"`
	RefreshInterval string        `yaml:"refresh_interval,omitempty"`

	Calendar        CalendarConfig         `yaml:"calendar"`
	GitHub          GitHubConfig           `yaml:"github"`
	Todos           TodosConfig            `yaml:"todos"`
	Granola         GranolaConfig          `yaml:"granola"`
	Slack           SlackConfig            `yaml:"slack"`
	Gmail           GmailConfig            `yaml:"gmail"`
	ExternalPlugins []ExternalPluginConfig `yaml:"external_plugins"`
	Agent           AgentConfig            `yaml:"agent"`
	Automations     []AutomationConfig     `yaml:"automations,omitempty"`
	Refresh         RefreshConfig          `yaml:"refresh"`
	Daemon          DaemonConfig           `yaml:"daemon"`

	// DisabledPlugins lists slugs of built-in plugins the user has turned off.
	// e.g. ["sessions", "commandcenter"]
	DisabledPlugins []string `yaml:"disabled_plugins,omitempty"`

	// loadedFromFile is true when the config was successfully loaded from disk.
	// When false (i.e. defaults), Save will refuse to overwrite an existing file.
	loadedFromFile bool `yaml:"-"`

	// originalContent stores the raw YAML bytes that were loaded from disk.
	// Used by Save() to detect regressions: if the new content would lose data
	// compared to the original file, Save refuses to write.
	originalContent []byte `yaml:"-"`
}

// PluginEnabled returns whether a plugin is enabled.
// It checks both the DisabledPlugins list (built-in plugins) and the
// ExternalPlugins entries (external plugins matched by name).
func (c *Config) PluginEnabled(slug string) bool {
	for _, s := range c.DisabledPlugins {
		if s == slug {
			return false
		}
	}
	for _, ep := range c.ExternalPlugins {
		if ep.Name == slug && !ep.Enabled {
			return false
		}
	}
	return true
}

// SetPluginEnabled adds or removes a slug from DisabledPlugins.
func (c *Config) SetPluginEnabled(slug string, enabled bool) {
	if enabled {
		// Remove from disabled list
		out := c.DisabledPlugins[:0]
		for _, s := range c.DisabledPlugins {
			if s != slug {
				out = append(out, s)
			}
		}
		c.DisabledPlugins = out
	} else {
		// Add to disabled list if not already there
		if c.PluginEnabled(slug) {
			c.DisabledPlugins = append(c.DisabledPlugins, slug)
		}
	}
}

const DefaultRefreshInterval = 5 * time.Minute

// ParseRefreshInterval returns the configured refresh interval, or the default.
func (c *Config) ParseRefreshInterval() time.Duration {
	if c.RefreshInterval == "" {
		return DefaultRefreshInterval
	}
	d, err := time.ParseDuration(c.RefreshInterval)
	if err != nil || d < 1*time.Minute {
		return DefaultRefreshInterval
	}
	return d
}

type CustomColors struct {
	Primary   string `yaml:"primary"`
	Secondary string `yaml:"secondary"`
	Accent    string `yaml:"accent"`
}

type CalendarConfig struct {
	Enabled   bool            `yaml:"enabled"`
	Calendars []CalendarEntry `yaml:"calendars"`
}

type CalendarEntry struct {
	ID      string `yaml:"id"`
	Label   string `yaml:"label"`
	Color   string `yaml:"color,omitempty"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

// IsEnabled returns whether this calendar entry is enabled.
// Defaults to true if the Enabled field is nil (not set).
func (e CalendarEntry) IsEnabled() bool {
	if e.Enabled == nil {
		return true
	}
	return *e.Enabled
}

// SetEnabled sets the enabled state of a calendar entry.
func (e *CalendarEntry) SetEnabled(v bool) {
	e.Enabled = &v
}

type GitHubConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Repos       []string `yaml:"repos"`
	Username    string   `yaml:"username"`
	TrackMyPRs  *bool    `yaml:"track_my_prs,omitempty"`
}

// IsTrackMyPRs returns whether "all my PRs" mode is enabled.
// Defaults to true if not explicitly set.
func (g GitHubConfig) IsTrackMyPRs() bool {
	if g.TrackMyPRs == nil {
		return true
	}
	return *g.TrackMyPRs
}

// SetTrackMyPRs sets the TrackMyPRs field.
func (g *GitHubConfig) SetTrackMyPRs(v bool) {
	g.TrackMyPRs = &v
}

type TodosConfig struct {
	Enabled bool `yaml:"enabled"`
}

type GranolaConfig struct {
	Enabled bool `yaml:"enabled"`
}

type SlackConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Token    string `yaml:"token,omitempty"`
	BotToken string `yaml:"bot_token,omitempty"` // deprecated: use Token; kept for backwards compat
}

// EffectiveToken returns the active Slack token. Resolution order:
//  1. config Token field (set via Settings UI or written to config.yaml)
//  2. deprecated BotToken field (back-compat with older configs)
//  3. SLACK_TOKEN env var (matches onboarding validate.go order)
//  4. SLACK_BOT_TOKEN env var
//
// The env fallback lets users keep the token out of config.yaml entirely and
// source it from Keychain, 1Password CLI, direnv, etc. via their shell rc.
func (c SlackConfig) EffectiveToken() string {
	if tok := strings.TrimSpace(c.Token); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(c.BotToken); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(os.Getenv("SLACK_TOKEN")); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
}

type GmailConfig struct {
	Enabled   bool   `yaml:"enabled"`
	TodoLabel string `yaml:"todo_label,omitempty"` // Gmail label name to sync as todos (empty = disabled)
	Advanced  bool   `yaml:"advanced,omitempty"`   // opt-in for modify+compose scopes (label mgmt, drafts)
}

type ExternalPluginConfig struct {
	Name        string `yaml:"name"`
	Command     string `yaml:"command"`
	Description string `yaml:"description,omitempty"`
	Enabled     bool   `yaml:"enabled"`
}

type AutomationConfig struct {
	Name         string                 `yaml:"name"`
	Command      string                 `yaml:"command"`
	Enabled      bool                   `yaml:"enabled"`
	Schedule     string                 `yaml:"schedule"`
	ConfigScopes []string               `yaml:"config_scopes"`
	Settings     map[string]interface{} `yaml:"settings,omitempty"`
}

type AgentConfig struct {
	DefaultBudget            float64  `yaml:"default_budget"`
	DefaultPermission        string   `yaml:"default_permission"`
	DefaultMode              string   `yaml:"default_mode"`
	MaxConcurrent            int      `yaml:"max_concurrent"`
	TodoWriteLearnedPaths    *bool    `yaml:"todo_write_learned_paths,omitempty"`
	TodoExtraWritePaths      []string `yaml:"todo_extra_write_paths,omitempty"`
	AutonomousAllowedDomains []string `yaml:"autonomous_allowed_domains,omitempty"`

	// Budget caps
	HourlyBudget     float64 `yaml:"hourly_budget"`      // max spend per rolling hour (default $25)
	DailyBudget      float64 `yaml:"daily_budget"`        // max spend per rolling 24h (default $100)
	BudgetWarningPct float64 `yaml:"budget_warning_pct"`  // warn at this fraction of budget (default 0.80)

	// Timeouts
	MaxRuntimeMinutes int `yaml:"max_runtime_minutes"` // kill agents after this many minutes (default 20, 0 = no limit)

	// Rate limiting & backoff
	MaxLaunchesPerAutomationPerHour int `yaml:"max_launches_per_automation_per_hour"` // default 20
	CooldownMinutes                 int `yaml:"cooldown_minutes"`                     // pause after budget hit (default 15)
	FailureBackoffBaseSec           int `yaml:"failure_backoff_base_seconds"`          // initial backoff on failure (default 60)
	FailureBackoffMaxSec            int `yaml:"failure_backoff_max_seconds"`           // max backoff cap (default 3600)
}

// TodoWriteLearnedPathsEnabled returns whether agents can write to learned paths.
// Defaults to true if not explicitly set.
func (a *AgentConfig) TodoWriteLearnedPathsEnabled() bool {
	if a.TodoWriteLearnedPaths == nil {
		return true
	}
	return *a.TodoWriteLearnedPaths
}

// DaemonConfig holds settings for the CCC daemon process.
type DaemonConfig struct {
	RefreshInterval  string `yaml:"refresh_interval"`  // default "5m"
	SessionRetention string `yaml:"session_retention"` // default "7d"
}

// RefreshConfig controls ai-cron behavior.
type RefreshConfig struct {
	// Enabled controls whether CCC spawns ai-cron on a timer.
	// Defaults to true when omitted for backwards compat.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Model selects which LLM model ai-cron uses for prompt generation.
	// Empty string means use the CLI default.
	Model string `yaml:"model,omitempty"`
}

// RefreshEnabled returns whether the periodic ai-cron refresh is enabled.
func (c *Config) RefreshEnabled() bool {
	if c.Refresh.Enabled == nil {
		return true
	}
	return *c.Refresh.Enabled
}

// BannerVisible returns whether the banner should be shown.
// Defaults to true if ShowBanner is nil (backwards compat).
func (c *Config) BannerVisible() bool {
	if c.ShowBanner == nil {
		return true
	}
	return *c.ShowBanner
}

// SetShowBanner sets the ShowBanner field.
func (c *Config) SetShowBanner(v bool) {
	c.ShowBanner = &v
}

// GetBannerTopPadding returns the number of blank lines above the banner.
// Defaults to 2 if not set.
func (c *Config) GetBannerTopPadding() int {
	if c.BannerTopPadding == nil {
		return 2
	}
	return *c.BannerTopPadding
}

// SetBannerTopPadding sets the BannerTopPadding field.
func (c *Config) SetBannerTopPadding(v int) {
	if v < 0 {
		v = 0
	}
	c.BannerTopPadding = &v
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Name:    "Claude Command",
		Palette: "aurora",
		Todos:   TodosConfig{Enabled: true},
		Agent: AgentConfig{
			DefaultBudget:            5.00,
			DefaultPermission:        "default",
			DefaultMode:              "normal",
			MaxConcurrent:            10,
			AutonomousAllowedDomains: []string{"github.com", "api.github.com"},

			HourlyBudget:                    25.00,
			DailyBudget:                     100.00,
			BudgetWarningPct:                0.80,
			MaxLaunchesPerAutomationPerHour: 20,
			CooldownMinutes:                 15,
			FailureBackoffBaseSec:           60,
			FailureBackoffMaxSec:            3600,
		},
		Daemon: DaemonConfig{
			RefreshInterval:  "5m",
			SessionRetention: "7d",
		},
	}
}

// ConfigDir returns the configuration directory path.
// Uses $CCC_CONFIG_DIR if set, otherwise ~/.config/ccc.
func ConfigDir() string {
	if dir := os.Getenv("CCC_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ccc")
}

// ConfigPath returns the path to config.yaml.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

// DataDir returns the data directory path.
// Uses $CCC_STATE_DIR if set, otherwise ConfigDir()/data.
func DataDir() string {
	if dir := os.Getenv("CCC_STATE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(ConfigDir(), "data")
}

// DBPath returns the path to the SQLite database.
func DBPath() string {
	return filepath.Join(DataDir(), "ccc.db")
}

// CredentialsDir returns the path to the credentials directory.
func CredentialsDir() string {
	return filepath.Join(ConfigDir(), "credentials")
}

// Load reads the config from ConfigPath(). If the file doesn't exist,
// it returns DefaultConfig() without error.
func Load() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ConfigPath(), err)
	}
	cfg.loadedFromFile = true
	cfg.originalContent = data
	return cfg, nil
}

// MarkLoadedFromFile marks a config as having been loaded from or saved to
// disk, so that subsequent Save calls are allowed to write it. This should
// only be called after a successful first Save (e.g. during onboarding).
func (c *Config) MarkLoadedFromFile() {
	c.loadedFromFile = true
}

// Save writes the config to ConfigPath(), creating directories as needed.
// If the config was not loaded from a file (i.e. it is a default config due to
// a load error), Save refuses to overwrite an existing config file to prevent
// data loss. It also creates a .bak backup before writing as defense-in-depth.
//
// Additional safety: Save re-reads the current file from disk and verifies that
// user-specific data (name, external plugins, data source settings) is not being
// regressed to defaults. This prevents scenarios where the in-memory config
// has been corrupted or reset (e.g. by process crash, binary rebuild, or
// accidental pointer sharing).
func Save(cfg *Config, force ...bool) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := ConfigPath()

	// Safety check 1: refuse to overwrite an existing config with defaults
	// when the config was never loaded from a file.
	if !cfg.loadedFromFile {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("refusing to overwrite %s: config was not loaded from file (possible data loss)", path)
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	// Safety check 2: if an existing config file is present, reload it and
	// verify we are not regressing user-specific data to defaults.
	// Skip this check when force is true (user-initiated saves from the settings UI).
	skipRegression := len(force) > 0 && force[0]
	if existing, readErr := os.ReadFile(path); readErr == nil && len(existing) > 0 {
		if !skipRegression {
			if err := detectRegression(existing, data); err != nil {
				return fmt.Errorf("refusing to save %s: %w", path, err)
			}
		}
		// Create a backup of the existing file before writing.
		_ = os.WriteFile(path+".bak", existing, 0o600)
	}

	// Write to a temp file and rename for atomicity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Rename failed — fall back to direct write.
		return os.WriteFile(path, data, 0o600)
	}

	cfg.loadedFromFile = true
	// Update the stored original content to match what we just wrote.
	cfg.originalContent = data
	return nil
}

// detectRegression checks whether newData would lose user-specific data that
// exists in existingData. It re-parses both as Config structs and compares
// key fields. Returns an error describing the regression, or nil if safe.
func detectRegression(existingData, newData []byte) error {
	// Fast path: if the new content is identical or larger, skip detailed check.
	if bytes.Equal(existingData, newData) {
		return nil
	}

	var existing, proposed Config
	if err := yaml.Unmarshal(existingData, &existing); err != nil {
		// Can't parse existing file — allow the save (it will improve things).
		return nil
	}
	if err := yaml.Unmarshal(newData, &proposed); err != nil {
		return fmt.Errorf("marshaled config is invalid YAML: %w", err)
	}

	defaults := DefaultConfig()

	// Check 1: name regression — if existing has a custom name and proposed
	// would revert it to the default.
	if existing.Name != defaults.Name && proposed.Name == defaults.Name {
		return fmt.Errorf("would reset name from %q to default %q", existing.Name, defaults.Name)
	}

	// Check 2: external plugins lost — if existing has plugins and proposed
	// has fewer or none.
	if len(existing.ExternalPlugins) > 0 && len(proposed.ExternalPlugins) == 0 {
		return fmt.Errorf("would lose %d external plugin(s)", len(existing.ExternalPlugins))
	}

	// Check 3: data source regression — if a data source was enabled and
	// would be disabled without the user intending it.
	if existing.Calendar.Enabled && !proposed.Calendar.Enabled && len(existing.Calendar.Calendars) > 0 {
		return fmt.Errorf("would disable calendar with %d configured calendars", len(existing.Calendar.Calendars))
	}

	// Check 4: automations lost — if existing has automations and proposed
	// has none.
	if len(existing.Automations) > 0 && len(proposed.Automations) == 0 {
		return fmt.Errorf("would lose %d automation(s)", len(existing.Automations))
	}

	return nil
}
