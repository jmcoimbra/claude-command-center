package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
)

// staleDays is the threshold for marking a PR as "stale".
const staleDays = 14

// RawPR is the intermediate type for JSON unmarshaling from `gh search prs`.
type RawPR struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	IsDraft   bool      `json:"isDraft"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
}

// PRDetail holds per-PR detail from `gh pr view`.
type PRDetail struct {
	HeadRefOid     string `json:"headRefOid"`
	ReviewDecision string `json:"reviewDecision"`
	Reviews        []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		State string `json:"state"`
	} `json:"reviews"`
	LatestReviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		State string `json:"state"`
	} `json:"latestReviews"`
	ReviewRequests []struct {
		Login string `json:"login"`
		Name  string `json:"name"` // team name (if team review request)
	} `json:"reviewRequests"`
	StatusCheckRollup []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
	Comments []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"comments"`
}

// ghSearchPRs is a variable for testability.
var ghSearchPRs = func(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args[:3], " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args[:3], " "), err)
	}
	return out, nil
}

// ghPRView is a variable for testability.
var ghPRView = func(ctx context.Context, repo string, number int) ([]byte, error) {
	args := []string{"pr", "view", fmt.Sprintf("%d", number), "-R", repo,
		"--json", "reviews,reviewRequests,statusCheckRollup,reviewDecision,comments,latestReviews,headRefOid"}
	cmd := ghCommand(ctx, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh pr view %d -R %s: %s", number, repo, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr view %d -R %s: %w", number, repo, err)
	}
	return out, nil
}

// fetchAuthoredPRs runs `gh search prs --author=@me --state=open`.
func fetchAuthoredPRs(ctx context.Context) ([]RawPR, error) {
	args := []string{"search", "prs", "--author=@me", "--state=open",
		"--json", "number,title,url,repository,isDraft,createdAt,updatedAt,author"}
	out, err := ghSearchPRs(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("fetch authored PRs: %w", err)
	}
	var prs []RawPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse authored PRs: %w", err)
	}
	return prs, nil
}

// fetchReviewRequestedPRs runs `gh search prs --review-requested=@me --state=open`.
func fetchReviewRequestedPRs(ctx context.Context) ([]RawPR, error) {
	args := []string{"search", "prs", "--review-requested=@me", "--state=open",
		"--json", "number,title,url,repository,isDraft,createdAt,updatedAt,author"}
	out, err := ghSearchPRs(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("fetch review-requested PRs: %w", err)
	}
	var prs []RawPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse review-requested PRs: %w", err)
	}
	return prs, nil
}

// fetchPRDetail runs `gh pr view` for a single PR to get review/CI detail.
func fetchPRDetail(ctx context.Context, repo string, number int) (*PRDetail, error) {
	out, err := ghPRView(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	var detail PRDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return nil, fmt.Errorf("parse pr detail %s#%d: %w", repo, number, err)
	}
	return &detail, nil
}

// prKey returns a dedup key like "owner/repo#123".
func prKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// buildPullRequests merges authored and review-requested PRs, deduplicates,
// and computes categories. The details map is keyed by prKey.
func buildPullRequests(authored, requested []RawPR, details map[string]*PRDetail, username string, now time.Time) []db.PullRequest {
	type merged struct {
		raw       RawPR
		isAuthor  bool
		isReviewRequested bool
	}

	seen := make(map[string]*merged)

	for _, pr := range authored {
		key := prKey(pr.Repository.NameWithOwner, pr.Number)
		seen[key] = &merged{raw: pr, isAuthor: true}
	}
	for _, pr := range requested {
		key := prKey(pr.Repository.NameWithOwner, pr.Number)
		if m, ok := seen[key]; ok {
			m.isReviewRequested = true
		} else {
			seen[key] = &merged{raw: pr, isReviewRequested: true}
		}
	}

	result := make([]db.PullRequest, 0, len(seen))
	for key, m := range seen {
		pr := m.raw
		role := computeRole(m.isAuthor, m.isReviewRequested)
		detail := details[key] // may be nil if detail fetch failed

		reviewDecision := ""
		var reviewerLogins, pendingReviewerLogins []string
		commentCount := 0
		ciStatus := ""
		lastActivity := pr.UpdatedAt

		headSHA := ""
		if detail != nil {
			headSHA = detail.HeadRefOid
			reviewDecision = detail.ReviewDecision
			reviewerLogins = extractReviewerLogins(detail)
			pendingReviewerLogins = extractPendingReviewerLogins(detail, username)
			commentCount = len(detail.Comments)
			ciStatus = computeCIStatus(detail.StatusCheckRollup)

			// Use latest comment time if newer than UpdatedAt.
			for _, c := range detail.Comments {
				if c.CreatedAt.After(lastActivity) {
					lastActivity = c.CreatedAt
				}
			}
		}

		dbPR := db.PullRequest{
			ID:                    key,
			Repo:                  pr.Repository.NameWithOwner,
			Number:                pr.Number,
			Title:                 pr.Title,
			URL:                   pr.URL,
			Author:                pr.Author.Login,
			Draft:                 pr.IsDraft,
			CreatedAt:             pr.CreatedAt,
			UpdatedAt:             pr.UpdatedAt,
			ReviewDecision:        reviewDecision,
			MyRole:                role,
			ReviewerLogins:        reviewerLogins,
			PendingReviewerLogins: pendingReviewerLogins,
			CommentCount:          commentCount,
			UnresolvedThreadCount: 0, // gh CLI doesn't expose this directly
			LastActivityAt:        lastActivity,
			CIStatus:              ciStatus,
			HeadSHA:               headSHA,
			FetchedAt:             now,
		}
		dbPR.Category = computeCategory(dbPR, username, now)
		result = append(result, dbPR)
	}

	return result
}

// computeRole returns "author", "reviewer", or "both".
func computeRole(isAuthor, isReviewRequested bool) string {
	if isAuthor && isReviewRequested {
		return "both"
	}
	if isAuthor {
		return "author"
	}
	return "reviewer"
}

// extractReviewerLogins returns all unique reviewer logins from reviews.
func extractReviewerLogins(detail *PRDetail) []string {
	seen := make(map[string]bool)
	var logins []string
	for _, r := range detail.Reviews {
		if r.Author.Login != "" && !seen[r.Author.Login] {
			seen[r.Author.Login] = true
			logins = append(logins, r.Author.Login)
		}
	}
	return logins
}

// extractPendingReviewerLogins returns logins that still have a pending review request.
// This uses reviewRequests (explicit pending requests) from the PR.
func extractPendingReviewerLogins(detail *PRDetail, _ string) []string {
	var logins []string
	for _, rr := range detail.ReviewRequests {
		if rr.Login != "" {
			logins = append(logins, rr.Login)
		}
	}
	return logins
}

// computeCIStatus summarizes statusCheckRollup into "success", "failure", or "pending".
func computeCIStatus(checks []struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}) string {
	if len(checks) == 0 {
		return ""
	}
	hasFailure := false
	hasPending := false
	for _, c := range checks {
		// gh returns different shapes: statusContext uses State, checkRun uses Conclusion/Status.
		state := strings.ToUpper(c.State)
		conclusion := strings.ToUpper(c.Conclusion)

		switch {
		case state == "FAILURE" || state == "ERROR" || conclusion == "FAILURE" || conclusion == "TIMED_OUT" || conclusion == "CANCELLED":
			hasFailure = true
		case state == "PENDING" || c.Status == "IN_PROGRESS" || c.Status == "QUEUED" || state == "EXPECTED":
			hasPending = true
		}
	}
	if hasFailure {
		return "failure"
	}
	if hasPending {
		return "pending"
	}
	return "success"
}

// computeCategory assigns a PR category based on the plan's logic:
//   - review: my_role is "reviewer" or "both", AND current user is in PendingReviewerLogins
//   - respond: my_role is "author" or "both", AND review_decision = "CHANGES_REQUESTED"
//   - stale: last_activity_at older than 14 days
//   - waiting: default for authored PRs
func computeCategory(pr db.PullRequest, username string, now time.Time) string {
	// Check "review" first: am I requested to review this?
	if pr.MyRole == "reviewer" || pr.MyRole == "both" {
		for _, login := range pr.PendingReviewerLogins {
			if strings.EqualFold(login, username) {
				return "review"
			}
		}
	}

	// Check "respond": am I author and there are changes requested?
	if pr.MyRole == "author" || pr.MyRole == "both" {
		if pr.ReviewDecision == "CHANGES_REQUESTED" {
			return "respond"
		}
	}

	// Check "stale": no activity for 14 days.
	if now.Sub(pr.LastActivityAt) > time.Duration(staleDays)*24*time.Hour {
		return "stale"
	}

	return "waiting"
}
