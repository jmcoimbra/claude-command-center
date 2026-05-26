package slack

import (
	"testing"
)

func TestHasCommitmentLanguage(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"I'll send you the report", true},
		{"I will handle this", true},
		{"Let me check on that", true},
		{"No commitments here", false},
		{"Just a regular message", false},
		{"I'LL SEND IT", true}, // case insensitive
		{"action item: review PR", true},
		{"", false},
		{"I promise I'll finish that email to zach tomorrow", true},
		{"I promise to send the report", true},
		{"I promised to follow up", true},
	}
	// Use the source's default first-person phrases (no third-person, since no name).
	phrases := firstPersonCommitmentPhrases
	for _, tt := range tests {
		got := hasCommitmentLanguage(tt.text, phrases)
		if got != tt.want {
			t.Errorf("hasCommitmentLanguage(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestSlackSource_commitmentPhrases_thirdPersonInclusion(t *testing.T) {
	src := New(true, "", "juliano", nil, nil)
	phrases := src.commitmentPhrases()

	wantSubstrings := []string{"juliano will", "juliano is going to", "juliano to follow", "juliano needs to"}
	for _, want := range wantSubstrings {
		found := false
		for _, p := range phrases {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected phrase %q in commitmentPhrases, got %v", want, phrases)
		}
	}

	// Sanity: no leftover "aaron" from before refactor.
	for _, p := range phrases {
		if p == "aaron will" || p == "aaron is going to" {
			t.Errorf("found legacy 'aaron' phrase in generalized output: %q", p)
		}
	}
}

func TestSlackSource_commitmentPhrases_emptyNameSkipsThirdPerson(t *testing.T) {
	src := New(true, "", "", nil, nil)
	phrases := src.commitmentPhrases()

	if len(phrases) != len(firstPersonCommitmentPhrases) {
		t.Errorf("empty name should yield only first-person phrases: got %d, want %d", len(phrases), len(firstPersonCommitmentPhrases))
	}
}

func TestSlackSource_searchQueries_emptyNameSkipsThirdPerson(t *testing.T) {
	withName := New(true, "", "juliano", nil, nil)
	withoutName := New(true, "", "", nil, nil)

	gotWith := len(withName.searchQueries())
	gotWithout := len(withoutName.searchQueries())
	if gotWith <= gotWithout {
		t.Errorf("setting a name should add third-person queries: with=%d without=%d", gotWith, gotWithout)
	}
}

func TestSlackSource_searchQueries_includesNameWhenSet(t *testing.T) {
	src := New(true, "", "juliano", nil, nil)
	queries := src.searchQueries()

	wantOne := "juliano will"
	found := false
	for _, q := range queries {
		if q == wantOne {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q in searchQueries, got %v", wantOne, queries)
	}
}

func TestBuildConversationContext(t *testing.T) {
	// All same day (Mar 17 2026 Pacific). Messages are newest-first.
	messages := []slackHistoryEntry{
		{Type: "message", Text: "I'll get this to you by EOD", TS: "1773788070.287879"},  // idx 0 — candidate ~3:54pm
		{Type: "message", Text: "Can you send me the mockup?", TS: "1773787000.000000"},   // idx 1 — ~3:36pm
		{Type: "message", Text: "The design looks great", TS: "1773786000.000000"},         // idx 2 — ~3:20pm
		{Type: "message", Text: "Here's the latest version", TS: "1773785000.000000"},      // idx 3 — ~3:03pm
	}

	t.Run("returns preceding messages in chronological order", func(t *testing.T) {
		got := buildConversationContext(messages, 0, 15, nil)
		want := "Here's the latest version\nThe design looks great\nCan you send me the mockup?\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("limits to n messages", func(t *testing.T) {
		got := buildConversationContext(messages, 0, 2, nil)
		want := "The design looks great\nCan you send me the mockup?\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty when candidate is last message", func(t *testing.T) {
		got := buildConversationContext(messages, 3, 15, nil)
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("skips non-message entries", func(t *testing.T) {
		msgs := []slackHistoryEntry{
			{Type: "message", Text: "I'll handle it", TS: "1773788070.000000"},
			{Type: "channel_join", Text: "", TS: "1773787000.000000"},
			{Type: "message", Text: "Can you take this?", TS: "1773786000.000000"},
		}
		got := buildConversationContext(msgs, 0, 15, nil)
		want := "Can you take this?\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("stops at day boundary", func(t *testing.T) {
		// Candidate is Mar 17 ~3:54pm Pacific; last message is Mar 16 ~11pm Pacific.
		msgs := []slackHistoryEntry{
			{Type: "message", Text: "I'll get this to you by EOD", TS: "1773788070.287879"},  // Mar 17
			{Type: "message", Text: "Can you send me the mockup?", TS: "1773787000.000000"},   // Mar 17
			{Type: "message", Text: "Yesterday's update", TS: "1773720000.000000"},             // Mar 16 ~9:20pm Pacific
		}
		got := buildConversationContext(msgs, 0, 15, nil)
		want := "Can you send me the mockup?\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
