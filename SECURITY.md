# Security

## What this tool trusts

`agent-sessions` reads state that coding agents write about themselves:

- `~/.claude/projects/**/*.jsonl` — transcripts
- `~/.claude/sessions/*.json` — the live process registry
- `~/.claude/jobs/*/state.json` — background agent records
- `~/.local/share/opencode/storage/session/**/*.json` — opencode sessions

**None of it is trusted input.** A transcript records what an agent read, which
includes web pages, dependency source and files from untrusted repositories. Any
process running as you can also write one. So every value taken from these files
is treated as hostile:

- Nothing reaches the terminal carrying a character the terminal reads as a
  command. `internal/untrusted.Text` neutralises it, wired in at the single
  choke point `internal/ui.guarded` so a render site added later is covered
  without having to remember this, and again on `list`'s table. `list --json`
  is deliberately not filtered: the encoder escapes a control character on the
  way out, where it is inert, and a consumer may legitimately want it back —
  decode that JSON straight onto a terminal at your own risk.
- Nothing reaches another program's argv that its parser would read as a flag.
  `internal/untrusted.ArgValue` refuses it — a transcript named
  `--dangerously-skip-permissions.jsonl` does not become that flag.
- A working directory is validated before any tool runs in it, and the git
  configuration keys that turn reading a repository into running a command are
  overridden in the subprocess environment (`internal/action/githarden.go`).
- A pid is confirmed to still belong to the process its record describes before
  it is signalled.
- Transcripts are opened with `O_NOFOLLOW`, so a symlink planted among them is
  not read.

**If you add a provider, this applies to it.** Return values through
`internal/untrusted` and build argv through `untrusted.ArgValue`.

## Residual risks

These are known and not fully closable:

- **Running a tool in a directory trusts that directory.** `open PR on GitHub`
  runs `gh`, which runs `git`, in the directory the session recorded. The
  dangerous config keys are overridden, but git and gh are large programs and
  the override list is a denylist. If a row names a directory you do not
  recognise, do not run a tool on it.
- **`kill process` sends SIGTERM to a pid from a file.** The pid's age is
  checked against the record, which closes pid recycling, but a process that
  genuinely is the recorded one is killed as asked.
- **`send` types into a terminal pane.** The message is your own text, and
  control characters are refused, but it is delivered to whatever is reading
  that pane.
- **The preview shows transcript content.** Escape sequences are neutralised;
  the words themselves are shown as written, including any prompt injection an
  agent read. Read it as data, not as instructions.

## Reporting a vulnerability

Open a private security advisory through GitHub's "Report a vulnerability"
button on this repository. Please do not open a public issue for anything exploitable.
Expect an acknowledgement within a week.
