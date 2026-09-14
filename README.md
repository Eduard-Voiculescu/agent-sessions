# agent-sessions

A command and control centre for your coding-agent sessions: one screen that
shows every session on the machine — running or finished, whatever directory it
was started in — and lets you read it, reply to it, jump to its terminal, resume
it, or start a new one anywhere.

`claude agents` shows you the sessions in the directory you happen to be
standing in. This shows you all of them.

```
  █▀█ █▀▀   agent-sessions v0.1.0
  █▀█ ▀▀█   5 sessions

╭─ agent-sessions ─────────────────────────────────────────────────────────────────────────────────╮
│ sessions 5    ○ history 2   ● busy 1   ● waiting: input needed 1   ● idle 1                      │
│ filters  --live off   --cwd —   ticket ENG                                                       │
│ dir      ~/.claude                                                                               │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  AGENT     STATUS                    NAME                BRANCH        DIRECTORY               AGE
▸ claude    ● busy                    Agent sessions CLI  HEAD          ~/git/tools/agent-ses…  now
  claude    ● waiting: input needed   Cache warmup on c…  eng-3160-ca…  ~/git/acme/api          1m
  claude    ● idle                    Report endpoint p…  eng-3180-pa…  ~/git/acme/api          32m
  ──────────────────────────────────────────────────────────────────────────────────────────────────
  claude    ◐ working                 Search reindex to…  eng-3150-re…  ~/git/acme/search       2d
  claude    -                         Add rate limiting…  eng-3170-ra…  ~/git/acme/web          1h

  ⏎ preview  ^n new  ^p actions  ^j jump  ^h live  / filter  ? help  q quit
```

## Install

There is no published module yet, so build it from the checkout:

```sh
git clone <this repo> agent-sessions
cd agent-sessions
go build -o ~/.local/bin/agent-sessions .
```

## The attention pet

A creature that floats over every app and says whether a session wants you.

```sh
make pet                            # build it
scripts/agent-pet                   # start it, or stop it if it is running
```

```
  ╭─ Draft Fortellis email response   ✓ ─╮   green, fades after 8s
  ╰─ Ready ──────────────────────────────╯

  ╭─ api                              ! ─╮   orange, stays until you deal with it
  ╰─ Input needed ───────────────────────╯

     ╭─────╮
     │ >_< │  ← one pill per session, stacked over its head
     ╰─────╯
```

| Face | Means |
| --- | --- |
| `>_<` | a session wants you — orange, bouncing, count on the badge |
| `o_o` | working |
| `-_-` | idle |
| `^_^` | one just finished |
| `...` | nothing running |
| `zZ` | snoozed, with the time left on the badge |
| `x_x` | no feed — is `agent-sessions` on `PATH`? |

**On a pill:** single-click opens its summary (agent, branch, directory), double-click
jumps to that session's terminal pane, right-click dismisses it. A dismissed pill
stays gone while that session keeps saying the same thing, and comes back when it
has something new to ask — dismissing means "I have seen this", not "never mention
this session again".

**On the creature:** click cycles through the sessions waiting, so three clicks
visit three panes. Double-click goes to the picker itself, which is the useful
move when nothing is waiting and a single click has nothing to cycle; with no
picker open it does nothing. Right-click snoozes it — 15 minutes, an hour, until 9am, or
until you say so — moves it to another corner, and holds **Quit**. Snoozing
silences the pills and greys the creature; the countdown sits on the badge.

The move items are only the two that make sense from where it stands: across, and
up or down. The pills follow, flipping which side of the creature they sit on and
which way they stack. A move lasts until the pet restarts — set `corner` in the
config for a permanent home, since the CLI owns that file and the pet does not
write to it.

Where it sits, how big it is, which classes raise it and whether it beeps come
from `[overlay]` in the config file. It reads `agent-sessions watch` for state and
calls `agent-sessions jump` when clicked, so it knows nothing about any agent's
on-disk layout — and it owns the feed process, so there is no daemon to install.

macOS attributes Automation consent to the app that asks, so the pet's first jump
raises its own dialog separately from the terminal's. Allow it once.

Swift 6 and SwiftPM, no Xcode project — the pills use Liquid Glass where the
system has it and a material where it does not. `make pet-test` runs its suite;
`make check` deliberately does not, so the Go half still builds on a machine with
no Swift installed.

`scripts/agent-pet` is what to point a launcher at — [ALFRED.md](ALFRED.md) wires
it to an Alfred keyword, a hotkey, or a LaunchAgent that starts it at login.

## Trust boundary

This tool reads state that coding agents write about themselves, and treats every
value in it as untrusted: a transcript records what an agent read, and any
process running as you can write one. Escape sequences are neutralised before
anything is drawn, argv is built from validated values only, and a pid is
confirmed before it is signalled. [SECURITY.md](SECURITY.md) states the boundary
in full, along with the risks that running an external tool on a session's own
directory cannot avoid.

Go 1.25.2 or newer. Four dependencies: cobra, bubbletea, lipgloss, and
`golang.org/x/sync`.

Everything works on any platform Go builds for, with three exceptions that need
help from outside:

| Feature | Needs |
| --- | --- |
| `^j` jump to a session's terminal pane | macOS and iTerm2 |
| `i` send a message into a running session | macOS and iTerm2 |
| `^n` start a session in a new terminal tab | macOS and iTerm2 |
| `jump` the command | macOS and iTerm2 |
| `open PR on GitHub` | the `gh` CLI, authenticated |

The first jump or send raises the macOS Automation consent dialog. Click Allow,
or it is later found under System Settings → Privacy & Security → Automation.

## Run

```sh
agent-sessions
```

That opens the picker over every Claude Code session it can find. The rest is
keys.

**Browsing**

| | |
| --- | --- |
| `⏎` | preview the last messages |
| `f` | fork into a new session |
| `^n` | start a session somewhere |
| `r` | rename this session |
| `^p` | actions and commands |
| `^j` | focus the terminal pane running it |
| `^h` | only sessions with a process |
| `/` | filter name, directory, branch, ticket |
| `j/k` `↑/↓` `^d/^u` `g/G` | move |
| `?` | the key list, on screen |
| `q` | quit |

**In the preview**

| | |
| --- | --- |
| `↑/↓` `^u/^d` | scroll |
| `⏎` | resume this session |
| `i` | write a message to it |
| `e` | expand messages held back by the per-message cap |
| `^j` | focus its terminal pane |
| `esc` | back to the list |

`i` behaves differently depending on the row, and the prompt says which: a
session with a running process is **typed into**, so you stay in the preview and
watch the reply arrive. A finished session has no process to type into, so
sending **resumes** it with your message as the opening prompt — which replaces
`agent-sessions` with the agent itself.

**Starting a session** (`^n`)

Two questions — which agent, then where — asked in a box over the list, so what
is already running stays on screen while a new one is started:

```
  AGENT     STATUS     ╭─ start claude — where ───────────────────── esc ─╮  AGE
▸ claude    ● busy     │ search  _                                       │  now
  claude    ● waiting: │                                                 │  1m
  ─────────────────────│ ▸ ~/git/acme/api                     3 sessions │─────
  claude    ◐ working  │   ~/git/acme/web                      1 session │  2d
  claude    -          │   ~/git/tools/agent-sessions          1 session │  1h
                       │                                                 │
                       │ ↑/↓ move · type a filter or a path · enter start │
                       ╰─────────────────────────────────────────────────╯
```

The agents offered are the ones `[providers] enable` leaves on, so there is no
second list to keep in step. The directories offered are the ones your sessions
are already in, most-used first; type a path beginning with `/`, `~` or `.` to
start somewhere new instead.

The agent starts in a new iTerm2 tab and **focus stays here**, so three of them
can be started in a row. Nothing is added to the table by hand: the agent writes
its own registry entry, so the row arrives on the next tick with a real pid, and
`^j`, `i` and `kill process` work on it like any other.

**Actions** (`^p`)

Grouped by what they touch:

```
session      rename session, clear custom name
go to        open in Linear - Ticket ENG-3170, open PR on GitHub,
             open in Fork, open in VS Code
clipboard    resume command, session id, transcript path, working directory
danger       kill process, delete session
picker       toggle --live filter, reload sessions
```

**Renaming** (`r`, or `rename session` in the palette)

An agent names a session for you — from its first prompt, or a title it generates
later. `r` replaces that with your own name, prefilled with what is on screen so
changing one word costs one word. Empty it to go back to the agent's name.

Your names live in `~/.agent-sessions/names.json`, keyed by agent and session id,
and are applied over whatever the provider derived — so a rename shows in the
picker, in `list`, and in the attention pet's notifications. Nothing is written to
an agent's own state: Claude Code rewrites its session registry at will, and a
finished session has no registry entry to write to at all.

The palette floats over the list in the same box, so the row being acted on stays
visible behind it. An action that cannot run says why rather than disappearing —
`open in Fork — unavailable: fork is not on PATH` — because a missing entry
tells you nothing to fix.

### Other commands

```sh
agent-sessions list                 # the table, no TUI
agent-sessions list --json          # for scripts
agent-sessions watch                # one JSON line per tick, for anything watching
agent-sessions jump --pid 51234      # focus the pane running a session
agent-sessions jump --picker        # focus the pane running the picker itself
agent-sessions config               # what was configured, and where from
agent-sessions config --json        # the same, resolved, for a program
agent-sessions purge --dry-run      # what deleting sessions left behind
```

`watch` is a status pipe: each line carries every session plus `worst` and
`attention` — the class that most wants a human, and how many sessions are in it.

```sh
agent-sessions watch | jq -c '{worst, attention}'
```

`delete session` moves a transcript to `~/.claude/.agent-sessions-trash` rather
than unlinking it, so it can be recovered; the footer prints the `mv` that puts
it back. `purge` empties that trash for good:

```sh
agent-sessions purge --older-than 720h    # older than 30 days
agent-sessions purge                      # everything
```

### Flags

| | |
| --- | --- |
| `--live` | only sessions with a running process |
| `--cwd <prefix>` | only sessions under a directory |
| `--agent <name>` | only sessions from one agent |
| `--limit <n>` | most recent transcripts to parse (default 200) |
| `--claude-dir <dir>` | where Claude Code keeps its state (default `$CLAUDE_CONFIG_DIR`, then `~/.claude`) |
| `--linear-workspace`, `--ticket-prefix` | ticket settings; prefer the config file |

## Configuration

Which editor, which git client, which tracker, and which actions appear at all
come from a config file. Write a commented starter and edit it:

```sh
agent-sessions config --init
```

It lands at the first of these that applies:

1. `$AGENT_SESSIONS_CONFIG`
2. `$XDG_CONFIG_HOME/agent-sessions/config`
3. `~/.agent-sessions/config`

```ini
[editor]
default = vscode          # cursor | intellij | sublime | vscode | zed

[vcs]
default = fork            # fork | gitkraken | sourcetree | tower

[tickets]
provider  = linear
workspace = acme
prefixes  = eng

[forge]
provider = github

[actions]
hide = copy.transcript, process.kill

[list]
limit = 200               # transcripts to parse; 0 means no limit

[filters]
live = false              # start showing only sessions with a process
cwd  = ~/git              # start filtered to one directory tree

[claude]
dir = ~/.claude           # where Claude Code keeps its state

[overlay]
corner = bottom-left      # bottom-left | bottom-right | top-left | top-right
offset = 24,24            # points from that corner, x,y
size   = 72               # sprite box, points
raise  = waiting          # status classes that raise the attention pet
sound  = false
```

A leading `~` is expanded, since no shell reads this file — left literal it
would make a filter match nothing at all, silently. `limit` and `live` are typed:
a whole number and `true`/`false`, nothing else. `yes` and `1` are rejected
rather than guessed at.

Every section and key is optional, and **no file is needed at all**: with
`[editor]` unset, the first editor in the table above that is actually installed
is used, and likewise for `[vcs]`. The file is for correcting a wrong guess, not
for making the tool work.

`[actions] hide` takes action ids, which are what `agent-sessions config` lists:

```
ticket.open     copy.resume    copy.id      copy.transcript   copy.cwd
launch.editor   launch.vcs     pr.open      process.kill      session.delete
```

Hiding is not the same as unavailable. An unavailable action still appears with
its reason; a hidden one is gone.

### What wins

```
explicit flag  >  environment variable  >  config file  >  built-in default
```

A flag *left at its default* does not beat the file — only one you actually
typed. So `--linear-workspace ""` turns tickets off for one run even with a
workspace configured, and `--live=false` overrides `[filters] live = true`.

One exception worth knowing: `[claude] dir` beats `$CLAUDE_CONFIG_DIR`. That
variable belongs to Claude Code and is this tool's fallback default, not somebody
overriding this tool. Only `--claude-dir` outranks the file.

### When something is wrong

Ask, rather than guess:

```
$ agent-sessions config
file      ~/.agent-sessions/config

editor      VS Code           (config)
vcs         Fork              (detected)
tickets     linear            (config)
workspace   acme              (config)
prefixes    eng               (config)
hidden      —                 (unset)
limit       200               (default)
live        false             (default)
cwd         —                 (unset)
claude dir  ~/.claude         (config)
```

Each value names where it came from — `(config)`, `(env)`, `(flag)`,
`(detected)`, `(unset)` — which is the actual question when an action is
missing. `(detected)` means no file said anything and the first installed tool
in the table was used.

A malformed file is fatal rather than ignored, and an unknown key is an error
rather than a silent no-op, because a setting quietly dropped reads exactly like
a setting applied:

```
~/.agent-sessions/config:7: unknown key "defualt" in [editor]: known keys are default
unknown editor "helix" in ~/.agent-sessions/config: known editors are cursor, intellij, sublime, vscode, zed
```

The file names tools; it can never name a command to run. Adding a tool means a
row in the table and a release — deliberately, so that a synced dotfile
repository or a stale backup can never become someone else's code running as
you.

## How it finds sessions

Nothing is registered or indexed. Three places on disk are read directly:

- **Transcripts** — `~/.claude/projects/<slug>/<id>.jsonl`, which supply the
  name, working directory, branch and messages.
- **The live registry** — `~/.claude/sessions/<pid>.json`, which supplies
  liveness and status (`busy`, `idle`, `waiting: input needed`).
- **Background agents** — `~/.claude/jobs/<id>/state.json`, which supply the
  progress line an agent running under the daemon writes about itself. Those
  never appear in the live registry, so without this they look like ordinary
  history.

Everything else is derived. The Linear key comes from the branch name, the pull
request from asking `gh` — so both work retroactively across every session
already on the machine, with nothing to sync and no API key to hold.

Two agents are read today. **opencode** is the second: one JSON record per
session under `~/.local/share/opencode/storage/session/`, honouring
`$XDG_DATA_HOME`. It stores a real title, so nothing has to be salvaged from a
first prompt — but it records no git branch, so BRANCH reads `-` on those rows
and the ticket and pull-request actions, both derived from the branch, do not
offer themselves. It publishes no registry of running processes either, so its
rows carry no status and `^j`, `i` and `kill process` stay hidden. A child
session — one with a `parentID` — is skipped, the same as a Claude sidechain:
nobody held that conversation directly.

Narrow to one agent with `--agent claude` or `--agent opencode`, or turn one off
in the config:

```ini
[providers]
enable = claude          # omit to read every agent this binary knows

[opencode]
dir = ~/.local/share/opencode/storage
```

codex and gemini are not read. codex has the best-designed store of the four
surveyed and zero rows in it; gemini hashes the project directory into its path
so the working directory cannot be recovered, and resumes by a positional index
that shifts as sessions are added. Neither is worth the adapter yet.
