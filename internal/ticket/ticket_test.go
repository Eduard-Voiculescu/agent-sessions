package ticket

import "testing"

func TestFromBranch(t *testing.T) {
	prefixes := []string{"abc", "eng"}

	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{name: "lowercase prefix is upper-cased", branch: "eng-3140-move-the-search ranking-endpoints", want: "ENG-3140"},
		{name: "after a path separator", branch: "feature/ENG-3120-quick-actions", want: "ENG-3120"},
		{name: "bare key", branch: "eng-3120", want: "ENG-3120"},
		{name: "second allowlisted prefix", branch: "eng-704/fix", want: "ENG-704"},
		{name: "dated branch is not a ticket", branch: "patch-2026-07-24", want: ""},
		{name: "encoding name is not a ticket", branch: "fix-utf-8-encoding", want: ""},
		{name: "allowlisted key after a rejected numbered segment", branch: "release-2-eng-3120", want: "ENG-3120"},
		{name: "prefix not allowlisted", branch: "zzz-1234-thing", want: ""},
		{name: "no digits", branch: "eng-thing", want: ""},
		{name: "develop", branch: "develop", want: ""},
		{name: "empty", branch: "", want: ""},
		{name: "detached head", branch: "HEAD", want: ""},
		{name: "real key after a rejected numbered segment", branch: "release-2/eng-3120-hotfix", want: "ENG-3120"},
		{name: "real key after a rejected sprint segment", branch: "sprint-24/eng-3120-fix-bug", want: "ENG-3120"},
		{name: "trailing digit run is excluded", branch: "eng-3140-2", want: "ENG-3140"},
		{name: "worktree tooling prefixes the key", branch: "worktree-eng-3110-batch-queue-retry-settings", want: "ENG-3110"},
		{name: "worktree tooling with a short suffix", branch: "worktree-eng-3120-quick-actions", want: "ENG-3120"},
		{name: "a verb before the key", branch: "fix-eng-3120-typo", want: "ENG-3120"},
		{name: "a longer prefix run is not the allowlisted one", branch: "add-neng-3120-thing", want: ""},
		{name: "a fused prefix is not the allowlisted one", branch: "myeng-3120", want: ""},
		{name: "an ordinal after the prefix is not a ticket", branch: "revert-eng-1-of-3", want: ""},
		{name: "two ordinals after the prefix are not tickets", branch: "merge-eng-2-into-eng-3", want: ""},
		{name: "an anchored key is trusted at one digit", branch: "eng-7-fix", want: "ENG-7"},
		{name: "an anchored key after a slash is trusted at two digits", branch: "hotfix/ENG-88", want: "ENG-88"},
		{name: "a two-digit key anchored at the start is a ticket", branch: "eng-99-old", want: "ENG-99"},
		{name: "the digit floor stops a small counter but not a large number", branch: "revert-eng-100-of-200", want: "ENG-100"},
		{name: "a year mid-branch reads as a ticket", branch: "eng-2024-migration", want: "ENG-2024"},
		{name: "the first allowlisted match wins", branch: "merge-eng-300-into-eng-400", want: "ENG-300"},
		{name: "an anchored key beats a longer one later on the branch", branch: "eng-88-and-eng-3110", want: "ENG-88"},
		{name: "a key the digit floor rejects leaves a later one standing", branch: "revert-eng-88-and-eng-3110", want: "ENG-3110"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromBranch(tt.branch, prefixes); got != tt.want {
				t.Errorf("FromBranch(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestFromBranchWithNoPrefixesAllowsNothing(t *testing.T) {
	if got := FromBranch("eng-3140-x", nil); got != "" {
		t.Errorf("FromBranch with no allowlist = %q, want empty", got)
	}
}

func TestIssueURL(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		key       string
		want      string
	}{
		{name: "normal", workspace: "acme", key: "ENG-3140", want: "https://linear.app/acme/issue/ENG-3140"},
		{name: "no workspace", workspace: "", key: "ENG-3140", want: ""},
		{name: "no key", workspace: "acme", key: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IssueURL(tt.workspace, tt.key); got != tt.want {
				t.Errorf("IssueURL(%q, %q) = %q, want %q", tt.workspace, tt.key, got, tt.want)
			}
		})
	}
}
