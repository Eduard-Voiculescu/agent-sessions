// Package ticket derives issue-tracker keys from git branch names.
package ticket

import (
	"regexp"
	"strings"
)

// keyPattern captures the maximal alphanumeric run before a dash and digits, so
// a differently spelled or longer run fails the allowlist: patch-2026-07-24
// yields PATCH, fix-utf-8-encoding yields UTF, neng-3120 yields NAVI.
var keyPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9]*)-(\d+)`)

// minUnanchoredDigits is what a key buys its way in with when it sits mid-branch:
// three digits reject the small counters branch names carry (revert-eng-1-of-3,
// merge-eng-2-into-eng-3) but nothing stops a large coincidental number, so
// revert-eng-100-of-200 still resolves ENG-100 and eng-2024-migration ENG-2024.
const minUnanchoredDigits = 3

// FromBranch returns the issue key encoded in branch, or "" when the branch
// encodes none. A match counts only when its prefix is allowlisted and it is
// either anchored — at the start of the branch or just after a slash, where a
// branch names its own subject, so any number is trusted there — or long enough
// to clear minUnanchoredDigits. The first counting match wins:
// eng-88-and-eng-3110 resolves ENG-88, while revert-eng-88-and-eng-3110 resolves
// ENG-3110, its leading key having been rejected as too short to trust
// unanchored.
func FromBranch(branch string, prefixes []string) string {
	if branch == "" || len(prefixes) == 0 {
		return ""
	}

	allowed := make(map[string]struct{}, len(prefixes))
	for _, p := range prefixes {
		allowed[strings.ToUpper(p)] = struct{}{}
	}

	for _, m := range keyPattern.FindAllStringSubmatchIndex(branch, -1) {
		start, prefix, number := m[0], strings.ToUpper(branch[m[2]:m[3]]), branch[m[4]:m[5]]
		if _, ok := allowed[prefix]; !ok {
			continue
		}
		if anchored(branch, start) || len(number) >= minUnanchoredDigits {
			return prefix + "-" + number
		}
	}
	return ""
}

func anchored(branch string, start int) bool {
	return start == 0 || branch[start-1] == '/'
}

// IssueURL returns the browser URL for key, or "" when either part is missing.
func IssueURL(workspace, key string) string {
	if workspace == "" || key == "" {
		return ""
	}
	return "https://linear.app/" + workspace + "/issue/" + key
}
