package action

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gh resolves a repository by shelling out to git in the directory it is given,
// and that directory comes from a transcript. A .git/config planted there can
// name a pager, an fsmonitor hook or a credential helper, and git runs it —
// verified against git 2.x, where a repo-local core.fsmonitor fires on a plain
// `git status`. GIT_CONFIG_KEY_n is applied as though it were `git -c`, which
// outranks every config file including the repository's own.
func TestGitSafeEnvOverridesEveryKeyThatCanRunACommand(t *testing.T) {
	index := map[string]string{}
	for _, entry := range gitSafeEnv() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			index[key] = value
		}
	}

	if index["GIT_CONFIG_COUNT"] == "" {
		t.Fatal("gitSafeEnv set no GIT_CONFIG_COUNT, so none of the overrides apply")
	}

	overridden := map[string]bool{}
	for i := 0; ; i++ {
		key, ok := index["GIT_CONFIG_KEY_"+strconv.Itoa(i)]
		if !ok {
			break
		}
		overridden[key] = true
	}

	for _, key := range []string{"core.pager", "core.sshCommand", "core.fsmonitor", "credential.helper", "diff.external", "core.askPass"} {
		if !overridden[key] {
			t.Errorf("gitSafeEnv does not override %s, which git would run", key)
		}
	}

	// git reads the keys up to the count and no further, so a count that does not
	// match the pairs silently drops the tail of the list.
	if got, want := index["GIT_CONFIG_COUNT"], strconv.Itoa(len(overridden)); got != want {
		t.Errorf("GIT_CONFIG_COUNT = %s, want %s; git ignores the overrides past the count", got, want)
	}
	if index["GIT_TERMINAL_PROMPT"] != "0" {
		t.Error("gitSafeEnv leaves GIT_TERMINAL_PROMPT unset, so git can block on a credential prompt")
	}
}

// The env builder being right is not the same as the subprocess being safe, so
// this drives real git through SystemOutput against a repository whose own
// config tries to run something. Without the override a plain `git status` runs
// it, which is the whole of finding 3.
func TestSystemOutputDisarmsARepositoryThatTriesToRunSomething(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	hook := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho PWNED >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"config", "core.fsmonitor", hook},
	} {
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// Control: the planted config really does run the hook, so a pass below means
	// the override worked rather than that the vector never fired.
	control := exec.Command(git, "status", "--porcelain")
	control.Dir = repo
	if out, _ := control.CombinedOutput(); !strings.Contains(string(out), "PWNED") {
		t.Skipf("this git does not honour core.fsmonitor here, so there is nothing to disarm: %s", out)
	}

	if out, err := SystemOutput(context.Background(), repo, git, "status", "--porcelain"); strings.Contains(out, "PWNED") {
		t.Errorf("SystemOutput ran the repository's own program: %q (err %v)", out, err)
	} else if err != nil && strings.Contains(err.Error(), "PWNED") {
		t.Errorf("SystemOutput ran the repository's own program: %v", err)
	}
}
