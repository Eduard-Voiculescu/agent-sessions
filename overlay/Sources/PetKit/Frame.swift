import Foundation

/// One line of `agent-sessions watch`.
///
/// `at` stays a String on purpose: Go emits RFC 3339 with fractional seconds,
/// which `JSONDecoder`'s `.iso8601` strategy rejects, and nothing here needs the
/// value — the pet measures staleness against its own clock.
public struct Frame: Codable, Sendable {
    public let at: String
    public let worst: String
    public let attention: Int
    public let sessions: [SessionInfo]

    public init(at: String, worst: String, attention: Int, sessions: [SessionInfo]) {
        self.at = at
        self.worst = worst
        self.attention = attention
        self.sessions = sessions
    }
}

/// A session as the feed reports it. Everything the Go side marks `omitempty` is
/// optional here, and nothing else is read: the pet renders a mood and asks for a
/// pid, so a field it does not use is a field it must not require.
public struct SessionInfo: Codable, Sendable {
    public let agent: String
    public let id: String
    public let name: String?
    public let title: String?
    public let cwd: String?
    public let gitBranch: String?
    public let live: Bool?
    public let pid: Int?
    public let status: String?

    public init(agent: String, id: String, name: String?, title: String? = nil, cwd: String?,
                gitBranch: String? = nil, live: Bool?, pid: Int?, status: String?) {
        self.agent = agent
        self.id = id
        self.name = name
        self.title = title
        self.cwd = cwd
        self.gitBranch = gitBranch
        self.live = live
        self.pid = pid
        self.status = status
    }

    /// label is what the card calls this session: an agent's own title when it has
    /// minted one, else the name the feed derived from its first prompt.
    public var label: String {
        let candidates = [title, name].compactMap { $0 }.filter { !$0.isEmpty }
        return candidates.first ?? id
    }

    public var isWaiting: Bool {
        (status ?? "").lowercased().hasPrefix("waiting")
    }
}
