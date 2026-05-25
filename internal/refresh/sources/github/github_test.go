package github

import (
	"context"
	"testing"

	"github.com/anutron/claude-command-center/internal/config"
)

func TestNew(t *testing.T) {
	repos := []string{"owner/repo1", "owner/repo2"}
	src := New(true, repos, "testuser", true)

	if src == nil {
		t.Fatal("New() returned nil")
	}
	if src.Username != "testuser" {
		t.Errorf("Username = %q, want %q", src.Username, "testuser")
	}
	if len(src.Repos) != 2 {
		t.Errorf("Repos length = %d, want 2", len(src.Repos))
	}
	if src.Repos[0] != "owner/repo1" {
		t.Errorf("Repos[0] = %q, want %q", src.Repos[0], "owner/repo1")
	}
	if !src.TrackMyPRs {
		t.Error("TrackMyPRs = false, want true")
	}
}

func TestNewDefaults(t *testing.T) {
	src := New(false, nil, "", false)

	if src.Username != "" {
		t.Errorf("Username = %q, want empty", src.Username)
	}
	if src.Repos != nil {
		t.Errorf("Repos = %v, want nil", src.Repos)
	}
	if src.TrackMyPRs {
		t.Error("TrackMyPRs = true, want false")
	}
}

func TestName(t *testing.T) {
	src := New(true, nil, "", false)
	if got := src.Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := New(tt.enabled, nil, "", false)
			if got := src.Enabled(); got != tt.enabled {
				t.Errorf("Enabled() = %v, want %v", got, tt.enabled)
			}
		})
	}
}

func TestFetchReturnsEmptyResult(t *testing.T) {
	// Mock the gh-backed helpers so the test does not depend on the local
	// gh CLI being authenticated or on the live network.
	origSearch := ghSearchPRs
	ghSearchPRs = func(ctx context.Context, args []string) ([]byte, error) {
		return []byte("[]"), nil
	}
	defer func() { ghSearchPRs = origSearch }()

	origPRView := ghPRView
	ghPRView = func(ctx context.Context, repo string, number int) ([]byte, error) {
		return []byte("{}"), nil
	}
	defer func() { ghPRView = origPRView }()

	src := New(true, []string{"owner/repo"}, "user", true)
	result, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Fetch() returned nil result")
	}
	if len(result.Todos) != 0 {
		t.Errorf("expected 0 todos, got %d", len(result.Todos))
	}
}

func TestTrackMyPRsConfigDefault(t *testing.T) {
	cfg := config.GitHubConfig{}
	if !cfg.IsTrackMyPRs() {
		t.Error("IsTrackMyPRs() should default to true when not set")
	}
}

func TestTrackMyPRsConfigExplicit(t *testing.T) {
	cfg := config.GitHubConfig{}
	cfg.SetTrackMyPRs(false)
	if cfg.IsTrackMyPRs() {
		t.Error("IsTrackMyPRs() should be false after SetTrackMyPRs(false)")
	}
	cfg.SetTrackMyPRs(true)
	if !cfg.IsTrackMyPRs() {
		t.Error("IsTrackMyPRs() should be true after SetTrackMyPRs(true)")
	}
}
