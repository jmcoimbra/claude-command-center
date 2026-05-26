package slack

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/refresh"
)

// SlackSource fetches Slack messages with commitment language and uses LLM to extract todos.
type SlackSource struct {
	enabled       bool
	botToken      string
	userFirstName string // lowercase; empty means third-person scanning is skipped
	LLM           llm.LLM
	DB            *sql.DB
}

// New creates a SlackSource with the given token, user first name, and LLM.
// userFirstName is normalized (lowercased + trimmed). Empty value disables
// third-person commitment detection ("<name> will...", "<name> is going to...").
func New(enabled bool, botToken string, userFirstName string, l llm.LLM, database *sql.DB) *SlackSource {
	return &SlackSource{
		enabled:       enabled,
		botToken:      botToken,
		userFirstName: strings.ToLower(strings.TrimSpace(userFirstName)),
		LLM:           l,
		DB:            database,
	}
}

func (s *SlackSource) Name() string  { return "slack" }
func (s *SlackSource) Enabled() bool { return s.enabled }

func (s *SlackSource) Fetch(ctx context.Context) (*refresh.SourceResult, error) {
	token := strings.TrimSpace(s.botToken)
	if token == "" {
		return nil, fmt.Errorf("slack auth: bot token not configured")
	}

	phrases := s.commitmentPhrases()
	queries := s.searchQueries()
	candidates, err := fetchSlackCandidates(ctx, token, queries, phrases)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	log.Printf("slack: %d candidates found", len(candidates))

	// Only send messages newer than our last successful sync to the LLM.
	// Subtract a 2-minute overlap to avoid losing messages sent during a sync
	// cycle (lastSuccess is recorded at sync completion, not fetch start).
	var lastSuccess time.Time
	if s.DB != nil {
		if ss, err := db.DBLoadSourceSync(s.DB, "slack"); err == nil && ss != nil && ss.LastSuccess != nil {
			lastSuccess = ss.LastSuccess.Add(-2 * time.Minute)
			log.Printf("slack: last successful sync: %s (with 2min overlap: %s)",
				ss.LastSuccess.Format(time.RFC3339), lastSuccess.Format(time.RFC3339))
		} else {
			log.Printf("slack: no previous successful sync found — processing all candidates")
		}
	}

	var newCandidates []slackCandidate
	for _, c := range candidates {
		if !lastSuccess.IsZero() && c.Timestamp != "" {
			// Slack timestamps are Unix epoch with decimal (e.g. "1710000000.000100")
			if ts, err := strconv.ParseFloat(c.Timestamp, 64); err == nil {
				msgTime := time.Unix(int64(ts), 0)
				if !msgTime.After(lastSuccess) {
					continue
				}
			}
		}
		newCandidates = append(newCandidates, c)
	}

	log.Printf("slack: %d new candidates to process (skipped %d)", len(newCandidates), len(candidates)-len(newCandidates))

	// Extract commitments via LLM only for new candidates
	var todos []db.Todo
	if len(newCandidates) > 0 && s.LLM != nil {
		todos, err = extractSlackCommitments(ctx, s.LLM, newCandidates, s.userFirstName)
		if err != nil {
			log.Printf("slack: LLM extraction failed: %v", err)
			return &refresh.SourceResult{
				Warnings: []db.Warning{{Source: "slack", Message: fmt.Sprintf("LLM extraction failed: %v", err)}},
			}, nil
		}
	} else if len(newCandidates) > 0 && s.LLM == nil {
		log.Printf("slack: WARNING — %d candidates found but LLM is nil, cannot extract commitments", len(newCandidates))
	}

	log.Printf("slack: returning %d todos", len(todos))
	return &refresh.SourceResult{Todos: todos}, nil
}

// slackCandidate is a Slack message that may contain a commitment.
type slackCandidate struct {
	Message             string
	Author              string // display name of the message author
	Permalink           string
	Channel             string
	ChannelID           string
	Timestamp           string
	ThreadContext       string
	ConversationContext string // preceding messages in the same channel for pronoun resolution
}

// firstPersonCommitmentPhrases are name-independent phrases. Apply to every operator.
var firstPersonCommitmentPhrases = []string{
	"i'll", "i will", "i need to", "let me", "i'm going to",
	"action item", "i committed", "i promise", "follow up",
	"send you", "set up", "schedule", "i can do", "i'll take",
	"i'll handle", "i'll get", "i'll send", "i'll look",
	"i'll check", "i'll follow", "i'll set", "i'll make",
	"i'll write", "i'll review", "i'll update", "i'll fix",
	"i'll create", "i'll put", "i'll share", "i'll reach out",
}

// thirdPersonCommitmentTemplates produce phrases when joined with the user's
// first name. e.g. "<name> will", "<name> is going to". Empty userFirstName
// means we skip these (only first-person scanning).
var thirdPersonCommitmentTemplates = []string{
	"%s will", "%s is going to", "%s to follow",
	"%s to handle", "%s can", "%s should",
	"and %s will", "%s needs to", "%s to send",
	"%s to review", "%s to set up", "%s to schedule",
}

// commitmentPhrases returns the full phrase set for this source (first-person
// plus third-person if userFirstName is set).
func (s *SlackSource) commitmentPhrases() []string {
	if s.userFirstName == "" {
		return firstPersonCommitmentPhrases
	}
	out := make([]string, 0, len(firstPersonCommitmentPhrases)+len(thirdPersonCommitmentTemplates))
	out = append(out, firstPersonCommitmentPhrases...)
	for _, tmpl := range thirdPersonCommitmentTemplates {
		out = append(out, fmt.Sprintf(tmpl, s.userFirstName))
	}
	return out
}

// searchQueries returns the queries used by the search.messages fallback path.
// Drops the third-person queries when userFirstName is empty.
func (s *SlackSource) searchQueries() []string {
	base := []string{
		"i'll", "i will", "i promise", "action item", "follow up", "let me",
	}
	if s.userFirstName == "" {
		return base
	}
	n := s.userFirstName
	return append(base,
		n+" will",
		n+" is going to",
		n+" to follow",
	)
}

// API response types for bot-compatible endpoints.

type slackConversationsListResponse struct {
	OK       bool           `json:"ok"`
	Channels []slackChannel `json:"channels"`
	Error    string         `json:"error,omitempty"`
	Meta     struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type slackChannel struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	IsIM    bool    `json:"is_im"`
	IsMpim  bool    `json:"is_mpim"`
	User    string  `json:"user"`    // For IM channels: the other user's ID (or self for self-DMs)
	Updated float64 `json:"updated"` // Unix timestamp of last activity (0 if never)
}

type slackHistoryResponse struct {
	OK       bool                `json:"ok"`
	Messages []slackHistoryEntry `json:"messages"`
	Error    string              `json:"error,omitempty"`
}

type slackHistoryEntry struct {
	Type string `json:"type"`
	User string `json:"user"`
	Text string `json:"text"`
	TS   string `json:"ts"`
}

type slackReply struct {
	User string `json:"user"`
	Text string `json:"text"`
	TS   string `json:"ts"`
}

type slackRepliesResponse struct {
	OK       bool         `json:"ok"`
	Messages []slackReply `json:"messages"`
	Error    string       `json:"error,omitempty"`
}

// slackAuthTestResponse is the response from auth.test API.
type slackAuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	UserID string `json:"user_id"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
}

// slackUserInfoResponse is the response from users.info API.
type slackUserInfoResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	User  struct {
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	} `json:"user"`
}

// fetchAuthIdentity calls auth.test to get the authenticated user's ID.
func fetchAuthIdentity(ctx context.Context, token string) (userID string, userName string, err error) {
	var result slackAuthTestResponse
	if err := slackAPIGet(ctx, token, "auth.test", url.Values{}, &result); err != nil {
		return "", "", err
	}
	if !result.OK {
		return "", "", fmt.Errorf("auth.test error: %s", result.Error)
	}
	return result.UserID, result.User, nil
}

// fetchUserName looks up a user's display name by ID. Returns "" on error.
func fetchUserName(ctx context.Context, token, userID string) string {
	var result slackUserInfoResponse
	if err := slackAPIGet(ctx, token, "users.info", url.Values{"user": {userID}}, &result); err != nil {
		return ""
	}
	if !result.OK {
		return ""
	}
	if result.User.RealName != "" {
		return result.User.RealName
	}
	if result.User.DisplayName != "" {
		return result.User.DisplayName
	}
	return result.User.Name
}

// channelDisplayName returns a human-readable channel name, handling IM channels
// which have no name field in the Slack API response.
func channelDisplayName(ctx context.Context, token string, ch slackChannel, selfUserID string) string {
	if ch.Name != "" {
		return ch.Name
	}
	if ch.IsIM {
		if ch.User == selfUserID {
			return "self-DM"
		}
		name := fetchUserName(ctx, token, ch.User)
		if name != "" {
			return "DM:" + name
		}
		return "DM:" + ch.User
	}
	if ch.IsMpim {
		return "group-DM:" + ch.ID
	}
	return ch.ID
}

// slackAPIGet performs a GET request to the Slack API and decodes the response.
// Retries once on rate limit (429) using the Retry-After header.
func slackAPIGet(ctx context.Context, token, endpoint string, params url.Values, dest interface{}) error {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET",
			"https://slack.com/api/"+endpoint+"?"+params.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == 429 {
			resp.Body.Close()
			retryAfter := 5 // default 5s
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if v, err := strconv.Atoi(ra); err == nil {
					retryAfter = v
				}
			}
			log.Printf("slack: rate limited on %s, retrying in %ds", endpoint, retryAfter)
			select {
			case <-time.After(time.Duration(retryAfter) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		const maxResponseSize = 10 * 1024 * 1024 // 10MB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			return err
		}

		return json.Unmarshal(body, dest)
	}
	return fmt.Errorf("%s: rate limited after retries", endpoint)
}

// fetchChannels retrieves channels the user is a member of, including DMs and group DMs.
// The activity filter in fetchSlackCandidates ensures only recently-active conversations
// have their history fetched.
func fetchChannels(ctx context.Context, token string) ([]slackChannel, error) {
	var allChannels []slackChannel
	cursor := ""

	for {
		params := url.Values{
			"types":            {"public_channel,private_channel,im,mpim"},
			"exclude_archived": {"true"},
			"limit":            {"999"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var result slackConversationsListResponse
		if err := slackAPIGet(ctx, token, "users.conversations", params, &result); err != nil {
			return nil, fmt.Errorf("listing channels: %w", err)
		}
		if !result.OK {
			if result.Error == "ratelimited" {
				log.Printf("slack: users.conversations rate limited, waiting 5s")
				select {
				case <-time.After(5 * time.Second):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("users.conversations error: %s", result.Error)
		}

		allChannels = append(allChannels, result.Channels...)

		// Paginate if there are more results
		if result.Meta.NextCursor == "" {
			break
		}
		cursor = result.Meta.NextCursor
		log.Printf("slack: users.conversations paginating (have %d channels so far)", len(allChannels))
	}

	return allChannels, nil
}

// fetchChannelHistory retrieves recent messages from a channel since the given timestamp.
func fetchChannelHistory(ctx context.Context, token, channelID string, oldest string) ([]slackHistoryEntry, error) {
	params := url.Values{
		"channel": {channelID},
		"oldest":  {oldest},
		"limit":   {"100"},
	}

	var result slackHistoryResponse
	if err := slackAPIGet(ctx, token, "conversations.history", params, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("conversations.history error: %s", result.Error)
	}

	return result.Messages, nil
}

// buildPermalink constructs a Slack message permalink from channel ID and timestamp.
// Format: https://slack.com/archives/{channelID}/p{ts_without_dot}
func buildPermalink(channelID, ts string) string {
	// Slack permalinks use the timestamp without the dot
	tsNoDot := strings.Replace(ts, ".", "", 1)
	return fmt.Sprintf("https://app.slack.com/archives/%s/p%s", channelID, tsNoDot)
}

// isMissingScopeError checks if a Slack API error indicates a missing OAuth scope.
func isMissingScopeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "missing_scope") || strings.Contains(err.Error(), "not_allowed_token_type")
}

// userNameResolver caches user ID → display name lookups to avoid repeated API calls.
type userNameResolver struct {
	cache map[string]string
	token string
	ctx   context.Context
}

func newUserNameResolver(ctx context.Context, token, selfUserID, selfUserName string) *userNameResolver {
	r := &userNameResolver{
		cache: make(map[string]string),
		token: token,
		ctx:   ctx,
	}
	if selfUserID != "" && selfUserName != "" {
		r.cache[selfUserID] = selfUserName
	}
	return r
}

func (r *userNameResolver) resolve(userID string) string {
	if name, ok := r.cache[userID]; ok {
		return name
	}
	name := fetchUserName(r.ctx, r.token, userID)
	if name == "" {
		name = userID
	}
	r.cache[userID] = name
	return name
}

func fetchSlackCandidates(ctx context.Context, token string, searchQueries []string, phrases []string) ([]slackCandidate, error) {
	// Get the authenticated user's identity for labeling self-DMs
	selfUserID, selfUserName, authErr := fetchAuthIdentity(ctx, token)
	if authErr != nil {
		log.Printf("slack: auth.test failed (non-fatal): %v", authErr)
	} else {
		log.Printf("slack: authenticated as %s (ID: %s)", selfUserName, selfUserID)
	}

	names := newUserNameResolver(ctx, token, selfUserID, selfUserName)

	// Try conversations-based approach first (requires channels:read scope).
	// If it fails due to missing scope, fall back to search.messages (requires search:read only).
	channels, err := fetchChannels(ctx, token)
	if err != nil {
		if isMissingScopeError(err) {
			log.Printf("slack: conversations.list missing scope, falling back to search.messages")
			return fetchSlackCandidatesViaSearch(ctx, token, searchQueries, phrases)
		}
		return nil, fmt.Errorf("listing channels: %w", err)
	}

	// Log channel type breakdown for diagnostics
	var nPublic, nPrivate, nIM, nMpim int
	for _, ch := range channels {
		switch {
		case ch.IsIM:
			nIM++
		case ch.IsMpim:
			nMpim++
		case ch.Name != "":
			// Heuristic: named channels are public or private
			nPublic++
		default:
			nPrivate++
		}
	}
	log.Printf("slack: %d channels found (public/private=%d, im=%d, mpim=%d, other=%d)",
		len(channels), nPublic, nIM, nMpim, nPrivate)

	// Look back 15 hours for recent activity
	cutoff := time.Now().Add(-15 * time.Hour)
	oldest := strconv.FormatInt(cutoff.Unix(), 10)

	// Filter to channels with recent activity to avoid hammering the API.
	// Slack's updated field is in milliseconds, not seconds.
	// Filter to channels with recent activity. Slack doesn't reliably populate
	// the updated field for IM channels — it often reflects when the channel was
	// created rather than last message time. Always include all IMs; the oldest
	// parameter on history calls ensures we only get recent messages back.
	cutoffMs := float64(cutoff.UnixMilli())
	var recentChannels []slackChannel
	for _, ch := range channels {
		if ch.IsIM || ch.Updated >= cutoffMs {
			recentChannels = append(recentChannels, ch)
		}
	}
	log.Printf("slack: %d/%d channels pass activity filter (last 15h + all %d IMs)", len(recentChannels), len(channels), nIM)

	var candidates []slackCandidate
	for _, ch := range recentChannels {
		displayName := channelDisplayName(ctx, token, ch, selfUserID)

		messages, err := fetchChannelHistory(ctx, token, ch.ID, oldest)
		if err != nil {
			if isMissingScopeError(err) {
				log.Printf("slack: conversations.history missing scope for %s (%s), falling back to search.messages",
					displayName, ch.ID)
				return fetchSlackCandidatesViaSearch(ctx, token, searchQueries, phrases)
			}
			// Log individual channel errors instead of silently skipping
			log.Printf("slack: error fetching history for %s (%s): %v", displayName, ch.ID, err)
			continue
		}

		var commitmentCount int
		for i, msg := range messages {
			if msg.Type != "message" || msg.Text == "" {
				continue
			}
			if !hasCommitmentLanguage(msg.Text, phrases) {
				continue
			}
			commitmentCount++

			c := slackCandidate{
				Message:   msg.Text,
				Author:    names.resolve(msg.User),
				Permalink: buildPermalink(ch.ID, msg.TS),
				Channel:   displayName,
				ChannelID: ch.ID,
				Timestamp: msg.TS,
			}

			// Include preceding messages for context (helps LLM resolve "this", "it", etc.).
			// Messages are newest-first, so indices > i are older/preceding.
			c.ConversationContext = buildConversationContext(messages, i, 15, names)

			// Fetch thread context if this message is part of a thread — non-fatal if scope missing
			thread, threadErr := fetchThreadContext(ctx, token, ch.ID, msg.TS)
			if threadErr == nil && len(thread) > 1 {
				var sb strings.Builder
				for _, reply := range thread {
					sb.WriteString(fmt.Sprintf("[%s]: %s\n", names.resolve(reply.User), reply.Text))
				}
				c.ThreadContext = sb.String()
			}

			candidates = append(candidates, c)
		}

		if len(messages) > 0 || ch.IsIM || ch.IsMpim {
			log.Printf("slack: %s (%s): %d messages, %d with commitment language",
				displayName, ch.ID, len(messages), commitmentCount)
		}
	}

	// Also run search.messages to pick up DM/group-DM candidates that aren't
	// covered by the channel-only fetch above.
	searchCandidates, searchErr := fetchSlackCandidatesViaSearch(ctx, token, searchQueries, phrases)
	if searchErr != nil {
		log.Printf("slack: search.messages fallback failed (non-fatal): %v", searchErr)
	} else if len(searchCandidates) > 0 {
		// Deduplicate by permalink — channel candidates take precedence.
		seen := make(map[string]bool, len(candidates))
		for _, c := range candidates {
			seen[c.Permalink] = true
		}
		var added int
		for _, c := range searchCandidates {
			if !seen[c.Permalink] {
				candidates = append(candidates, c)
				seen[c.Permalink] = true
				added++
			}
		}
		if added > 0 {
			log.Printf("slack: search.messages added %d additional candidates (DMs/group DMs)", added)
		}
	}

	return candidates, nil
}

// slackSearchResponse is the response from search.messages API.
type slackSearchResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages struct {
		Matches []slackSearchMatch `json:"matches"`
	} `json:"messages"`
}

type slackSearchMatch struct {
	Text      string `json:"text"`
	Username  string `json:"username"`
	Timestamp string `json:"ts"`
	Permalink string `json:"permalink"`
	Channel   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
}

// fetchSlackCandidatesViaSearch uses the search.messages API (requires only search:read scope)
// to find commitment-language messages. This is the fallback when conversations.list is unavailable.
// searchQueries drives the API calls (one per query); phrases is the filter applied to results.
func fetchSlackCandidatesViaSearch(ctx context.Context, token string, searchQueries, phrases []string) ([]slackCandidate, error) {
	log.Printf("slack: using search.messages fallback path")

	var allCandidates []slackCandidate

	seen := make(map[string]bool)
	for _, query := range searchQueries {
		params := url.Values{
			"query": {fmt.Sprintf("%s after:3d", query)},
			"count": {"50"},
			"sort":  {"timestamp"},
		}

		var result slackSearchResponse
		if err := slackAPIGet(ctx, token, "search.messages", params, &result); err != nil {
			return nil, fmt.Errorf("search.messages: %w", err)
		}
		if !result.OK {
			return nil, fmt.Errorf("search.messages error: %s", result.Error)
		}

		var matchCount int
		for _, match := range result.Messages.Matches {
			if match.Text == "" {
				continue
			}
			// Deduplicate by timestamp+channel
			key := match.Channel.ID + ":" + match.Timestamp
			if seen[key] {
				continue
			}
			seen[key] = true

			if !hasCommitmentLanguage(match.Text, phrases) {
				continue
			}
			matchCount++

			permalink := match.Permalink
			if permalink == "" {
				permalink = buildPermalink(match.Channel.ID, match.Timestamp)
			}

			channelName := match.Channel.Name
			if channelName == "" {
				channelName = "DM:" + match.Channel.ID
			}

			allCandidates = append(allCandidates, slackCandidate{
				Message:   match.Text,
				Author:    match.Username,
				Permalink: permalink,
				Channel:   channelName,
				ChannelID: match.Channel.ID,
				Timestamp: match.Timestamp,
			})
		}
		log.Printf("slack: search query %q: %d matches, %d with commitment language",
			query, len(result.Messages.Matches), matchCount)
	}

	log.Printf("slack: search fallback found %d total candidates", len(allCandidates))
	return allCandidates, nil
}

func fetchThreadContext(ctx context.Context, token, channelID, ts string) ([]slackReply, error) {
	params := url.Values{
		"channel": {channelID},
		"ts":      {ts},
		"limit":   {"20"},
	}

	var result slackRepliesResponse
	if err := slackAPIGet(ctx, token, "conversations.replies", params, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("slack replies error: %s", result.Error)
	}

	return result.Messages, nil
}

// buildConversationContext extracts up to n preceding messages from the same
// calendar day as the candidate. Messages are newest-first in the slice, so
// preceding messages are at higher indices. Returns them in chronological order
// (oldest first) with speaker labels.
//
// names may be nil (e.g. in tests or the search fallback), in which case
// messages are emitted without speaker labels.
func buildConversationContext(messages []slackHistoryEntry, candidateIdx, n int, names *userNameResolver) string {
	if candidateIdx+1 >= len(messages) {
		return ""
	}

	// Determine the candidate's calendar day.
	candidateDay := slackTSDay(messages[candidateIdx].TS)

	// Collect up to n preceding messages that fall on the same day.
	var preceding []slackHistoryEntry
	for i := candidateIdx + 1; i < len(messages) && len(preceding) < n; i++ {
		msg := messages[i]
		if msg.Type != "message" || msg.Text == "" {
			continue
		}
		if !candidateDay.IsZero() && !slackTSDay(msg.TS).IsZero() {
			if !slackTSDay(msg.TS).Equal(candidateDay) {
				break // crossed into previous day
			}
		}
		preceding = append(preceding, msg)
	}

	// Reverse so output is chronological (oldest first).
	var sb strings.Builder
	for i := len(preceding) - 1; i >= 0; i-- {
		msg := preceding[i]
		if names != nil && msg.User != "" {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n", names.resolve(msg.User), msg.Text))
		} else {
			sb.WriteString(msg.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// slackTSDay parses a Slack timestamp and returns the calendar day (midnight UTC-7/Pacific).
// Returns zero time on parse failure.
func slackTSDay(ts string) time.Time {
	f, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return time.Time{}
	}
	t := time.Unix(int64(f), 0).In(time.FixedZone("Pacific", -7*3600))
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func hasCommitmentLanguage(text string, phrases []string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
