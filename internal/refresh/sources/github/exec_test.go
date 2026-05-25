package github

import (
	"strings"
	"testing"
)

func TestFilterGHTokenEnv(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "removes GH_TOKEN",
			in:   []string{"PATH=/bin", "GH_TOKEN=stale-value", "HOME=/Users/x"},
			want: []string{"PATH=/bin", "HOME=/Users/x"},
		},
		{
			name: "removes GITHUB_TOKEN",
			in:   []string{"PATH=/bin", "GITHUB_TOKEN=also-stale", "HOME=/Users/x"},
			want: []string{"PATH=/bin", "HOME=/Users/x"},
		},
		{
			name: "removes both",
			in:   []string{"GH_TOKEN=a", "GITHUB_TOKEN=b", "PATH=/bin"},
			want: []string{"PATH=/bin"},
		},
		{
			name: "preserves unrelated vars even if names look similar",
			in:   []string{"GH_TOKEN_BACKUP=keep", "MY_GH_TOKEN=keep", "GH_TOKEN=remove"},
			want: []string{"GH_TOKEN_BACKUP=keep", "MY_GH_TOKEN=keep"},
		},
		{
			name: "no-op when neither var is set",
			in:   []string{"PATH=/bin", "HOME=/Users/x"},
			want: []string{"PATH=/bin", "HOME=/Users/x"},
		},
		{
			name: "empty input",
			in:   []string{},
			want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterGHTokenEnv(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %d want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got=%q want=%q", i, got[i], tc.want[i])
				}
			}
			// Defensive: confirm no GH_TOKEN= prefix survives.
			for _, e := range got {
				if strings.HasPrefix(e, "GH_TOKEN=") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
					t.Errorf("leak: %q should have been removed", e)
				}
			}
		})
	}
}
