import Foundation

enum Shell {
    /// run executes a command to completion and returns its stdout, or nil if it
    /// could not be launched or exited non-zero. Used for the two short-lived
    /// calls — `config --json` and `jump` — never for the feed, which streams.
    ///
    /// It goes through `env` so the binary is found on PATH: an app launched from
    /// Finder inherits a login PATH rather than a shell's, and hardcoding a
    /// location would break every install that is not the one on this machine.
    static func run(_ binary: String, _ arguments: [String]) -> String? {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [binary] + arguments

        let out = Pipe()
        process.standardOutput = out
        process.standardError = Pipe()

        do {
            try process.run()
        } catch {
            return nil
        }

        let data = out.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else { return nil }
        return String(data: data, encoding: .utf8)
    }
}

public enum Action {
    /// jump puts a session's terminal pane in front by asking the CLI, which owns
    /// every AppleScript in this program.
    ///
    /// The first call raises macOS's own Automation consent dialog attributed to
    /// this app, even where the terminal was granted it long ago, so a refusal
    /// here is reported by the caller rather than swallowed.
    @discardableResult
    public static func jump(pid: Int, binary: String = "agent-sessions") -> Bool {
        Shell.run(binary, ["jump", "--pid", String(pid)]) != nil
    }

    /// picker puts the agent-sessions picker itself in front — the one place from
    /// which every session is reachable.
    ///
    /// Which process that is, is the CLI's question to answer: the pet's own feed
    /// child is an agent-sessions process too, so a search from here would find
    /// it and jump at something with no pane. False means none is running, which
    /// the caller is free to treat as nothing to do.
    @discardableResult
    public static func picker(binary: String = "agent-sessions") -> Bool {
        Shell.run(binary, ["jump", "--picker"]) != nil
    }
}
