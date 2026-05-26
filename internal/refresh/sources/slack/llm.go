package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/refresh"
)

func extractSlackCommitments(ctx context.Context, l llm.LLM, candidates []slackCandidate, userFirstName string) ([]db.Todo, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	for i, c := range candidates {
		sb.WriteString(fmt.Sprintf("## Message %d (from #%s)\n", i+1, c.Channel))
		if c.Author != "" {
			sb.WriteString(fmt.Sprintf("Author: %s\n", c.Author))
		}
		sb.WriteString(fmt.Sprintf("Permalink: %s\n", c.Permalink))
		if c.ConversationContext != "" {
			sb.WriteString(fmt.Sprintf("Preceding conversation:\n%s\n", c.ConversationContext))
		}
		sb.WriteString(fmt.Sprintf("Message: %s\n", c.Message))
		if c.ThreadContext != "" {
			sb.WriteString(fmt.Sprintf("Thread context:\n%s\n", c.ThreadContext))
		}
		sb.WriteString("\n---\n\n")
	}

	// userFirstName is the operator's name; capitalize for the prompt body so it
	// reads naturally. Empty name means we use a generic "the user" framing.
	nameTitle := strings.Title(userFirstName)
	if nameTitle == "" {
		nameTitle = "the user"
	}
	nameOrTheUser := nameTitle
	if nameTitle != "the user" {
		nameOrTheUser = nameTitle
	}

	prompt := fmt.Sprintf(`You are filtering Slack messages for real commitments involving the user (%s). The bar is VERY high.

A message is a todo if EITHER:
A) The user explicitly committed to a specific deliverable (not just participating in conversation)
B) Someone else assigned work to the user. e.g., "%s will...", "Bob and %s will follow-up on...",
   "%s is going to...", "%s to handle...", "[Name] and %s will..."
   These are commitments made ON BEHALF of the user that they need to be aware of.

In both cases:
1. There must be a concrete next action with a clear outcome
2. You can write an actionable title starting with a verb (Send, Review, Schedule, Build, Write, Follow up, etc.)

Each message has an Author field showing who wrote it. Use this to determine attribution:
- If the Author is NOT %s and the message uses first-person ("I will", "I'll"), that is the OTHER person's commitment, not %s's. REJECT it.
- If the Author is NOT %s but the message assigns work to %s ("%s will...", "%s to..."), that IS a todo for %s.

REJECT messages that are:
- First-person commitments by someone other than %s (check the Author field!)
- Conversational responses ("done", "good process!", "sounds good")
- Observations, tips, shared links, compliments
- Descriptions of past actions ("I just...", "I found that...")
- Vague intentions without a specific deliverable
- Assignments to OTHER people that don't include %s

Use the preceding conversation and thread context to understand WHAT was committed to. Messages often use pronouns like "this", "it", "that". Resolve them using the surrounding conversation. Build the todo title from the full context, not just the short message.

For each real commitment, return:
- title: Actionable title starting with a verb (20+ chars)
- source_ref: The permalink
- context: Channel name and what area this relates to
- detail: Full context: who was in the conversation, what was discussed, what's expected
- who_waiting: Person(s) waiting on this
- due: YYYY-MM-DD if mentioned, empty string if not

Return ONLY a JSON array. Return [] if no real commitments found. Expect 0-3 results from these %d candidates.

Messages:
%s`,
		nameOrTheUser,                                                                 // header
		nameTitle, nameTitle, nameTitle, nameTitle, nameTitle,                         // B examples (5)
		nameTitle, nameTitle, nameTitle, nameTitle, nameTitle, nameTitle, nameTitle,   // Author attribution block (7)
		nameTitle, nameTitle,                                                          // reject block (2)
		len(candidates), sb.String())

	log.Printf("slack: sending %d candidates to LLM for extraction", len(candidates))
	for i, c := range candidates {
		log.Printf("slack: candidate %d: channel=%s text=%q", i+1, c.Channel, truncate(c.Message, 80))
	}

	text, err := l.Complete(llm.WithOperation(ctx, "slack-extract"), prompt)
	if err != nil {
		return nil, fmt.Errorf("slack commitment extraction: %w", err)
	}

	text = refresh.CleanJSON(text)
	log.Printf("slack: LLM response (first 500 chars): %s", truncate(text, 500))

	var items []struct {
		Title      string `json:"title"`
		SourceRef  string `json:"source_ref"`
		Context    string `json:"context"`
		Detail     string `json:"detail"`
		WhoWaiting string `json:"who_waiting"`
		Due        string `json:"due"`
	}
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("parsing slack commitment response: %w (raw: %s)", err, text[:min(200, len(text))])
	}

	log.Printf("slack: LLM extracted %d commitments from %d candidates", len(items), len(candidates))

	var todos []db.Todo
	for _, item := range items {
		log.Printf("slack: extracted todo: %q (source_ref=%s)", item.Title, truncate(item.SourceRef, 60))
		todos = append(todos, db.Todo{
			Title:      item.Title,
			Source:     "slack",
			SourceRef:  item.SourceRef,
			Context:    item.Context,
			Detail:     item.Detail,
			WhoWaiting: item.WhoWaiting,
			Due:        item.Due,
			Status:     "",
		})
	}

	return todos, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
