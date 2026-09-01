# Alfred

Launching the attention pet from Alfred, and a couple of things worth wiring up
while you are in there.

Everything below goes through one script, `scripts/agent-pet`, which starts the
pet if it is not running and stops it if it is. It is written for a launcher
rather than a shell, which matters more than it sounds — see
[Why a script rather than the binary](#why-a-script-rather-than-the-binary).

## Once, before any of this works

```sh
make build   # puts agent-sessions on your PATH, with watch and jump
make pet     # builds overlay/.build/release/AgentPet
```

`make build` is not optional. The pet reads its state by spawning
`agent-sessions watch`, so a CLI predating that command leaves it showing a
dead-feed face (`x_x`) with nothing on screen to say why. The script checks for
this and refuses with the fix rather than starting something broken:

```
$ scripts/agent-pet
this agent-sessions has no watch command — run: make build
```

## A keyword: `pet`

**Alfred Preferences → Workflows → + → Blank Workflow.** Name it *Attention
pet*.

1. Right-click the canvas → **Inputs → Keyword**. Keyword `pet`, title
   *Toggle attention pet*, and set it to **No Argument**.
2. Right-click → **Actions → Run Script**. Language `/bin/bash`, and paste the
   one line below, with your own checkout path:

   ```sh
   ~/git/personal/agent-sessions/scripts/agent-pet
   ```

3. Right-click → **Outputs → Post Notification**. Title *Attention pet*, text
   `{query}` — the script prints `attention pet started` or `attention pet
   stopped`, so the notification tells you which way the toggle went.
4. Drag a connection from Keyword → Run Script → Post Notification.

`pet` now toggles it, and Alfred tells you what happened.

## A hotkey instead, or as well

Right-click the canvas → **Inputs → Hotkey**, bind whatever is free — `⌥⌘P`
sits under the fingers — and connect it to the same Run Script node. A hotkey
and a keyword can both feed one action; there is no reason to duplicate it.

## Starting it at login

Alfred is not the right tool for this one — a LaunchAgent is:

```sh
cat > ~/Library/LaunchAgents/dev.agent-sessions.pet.plist <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.agent-sessions.pet</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/YOU/git/personal/agent-sessions/overlay/.build/release/AgentPet</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/Users/YOU/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><false/>
</dict>
</plist>
PLIST
launchctl load ~/Library/LaunchAgents/dev.agent-sessions.pet.plist
```

Substitute your own username in both paths. The `PATH` entry is the whole point
of the `EnvironmentVariables` block: launchd hands an agent almost nothing, and
without `~/.local/bin` on it the pet cannot find the CLI it reads.

`launchctl unload` the same path to stop it starting at login.

## Why a script rather than the binary

Pointing Alfred straight at `overlay/.build/release/AgentPet` looks like it
should work and mostly does not, for three reasons the script handles:

- **Alfred's PATH is launchd's**, roughly `/usr/bin:/bin:/usr/sbin:/sbin`. The
  pet spawns `agent-sessions` through `/usr/bin/env`, so from Alfred it finds
  nothing and sits there as `x_x`. The script appends the usual install
  locations — appends rather than prepends, so a CLI you deliberately put on
  PATH still wins.
- **A second copy**. Run the binary again while one is already running and you
  get two pets in the same corner, one drawn over the other, both jumping. The
  script toggles instead.
- **Nothing to read when it fails.** The script's messages are Alfred
  notifications; the pet's own output goes to
  `~/Library/Logs/agent-pet.log`, which is where to look when it starts and then
  vanishes.

## While you are in there: jumping to a session from Alfred

The CLI has two commands built for exactly this, and neither needs the pet:

```sh
agent-sessions watch --once | jq -r '.sessions[] | select(.live) | "\(.pid)\t\(.name)"'
agent-sessions jump --pid 51234
```

A **Script Filter** over the first, feeding `jump --pid {query}`, gives you
`⌥Space` → type part of a session name → land in its terminal pane. That is a
different workflow from this one and worth its own, so it is not written here —
ask and it can be.
