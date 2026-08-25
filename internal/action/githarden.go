package action

import (
	"fmt"
	"os"
	"strconv"
)

// gitUnsafeConfig is every git configuration key that turns reading a repository
// into running a command, paired with the value that disarms it. gh resolves the
// repository by shelling out to git in the directory it is handed, and that
// directory is named by a transcript: a .git/config planted there would
// otherwise choose the program git runs, and core.fsmonitor alone fires on a
// plain `git status`.
var gitUnsafeConfig = [][2]string{
	{"core.pager", "cat"},
	{"core.sshCommand", ""},
	{"core.fsmonitor", ""},
	{"core.askPass", ""},
	{"credential.helper", ""},
	{"diff.external", ""},
	{"protocol.ext.allow", "never"},
}

// gitSafeEnv is the environment for a git or gh subprocess run in a directory
// this tool did not choose. GIT_CONFIG_KEY_n is applied as though it were
// `git -c`, which outranks every configuration file including the repository's
// own — an override placed here cannot be undone by the directory being read.
//
// This narrows the exposure rather than removing it: the list is a denylist over
// two large programs, so running any tool in a directory remains an act of
// trusting that directory. SECURITY.md says so.
func gitSafeEnv() []string {
	env := append(os.Environ(),
		"GIT_CONFIG_COUNT="+strconv.Itoa(len(gitUnsafeConfig)),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	for i, pair := range gitUnsafeConfig {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, pair[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, pair[1]),
		)
	}
	return env
}
