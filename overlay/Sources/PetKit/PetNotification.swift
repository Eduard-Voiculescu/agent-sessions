import Foundation

/// PetNotification is one pill above the creature. The mapping from a session
/// lives here rather than in the view so the wording is testable and so a pill
/// carries everything it needs to draw itself — including the summary it shows
/// when opened, which is otherwise a view digging through the feed.
public struct PetNotification: Identifiable, Sendable, Equatable {
    public enum Kind: Sendable, Equatable {
        /// A human is blocking this session. It stays until they unblock it.
        case waiting
        /// It just finished. It fades on its own.
        case ready
    }

    public let id: String
    public let title: String
    public let subtitle: String
    public let kind: Kind
    public let pid: Int?
    public let agent: String
    public let branch: String?
    public let directory: String?

    public static func waiting(_ session: SessionInfo) -> PetNotification {
        PetNotification(session, kind: .waiting, subtitle: waitingSubtitle(session.status))
    }

    public static func ready(_ session: SessionInfo) -> PetNotification {
        PetNotification(session, kind: .ready, subtitle: "Ready")
    }

    private init(_ session: SessionInfo, kind: Kind, subtitle: String) {
        self.id = session.id
        self.title = session.label
        self.subtitle = subtitle
        self.kind = kind
        self.pid = session.pid
        self.agent = session.agent
        self.branch = session.gitBranch
        self.directory = session.cwd
    }

    /// waitingSubtitle turns the feed's machine string into the one line a pill
    /// has room for: the class is already said by the pill's colour and glyph, so
    /// what stays is what the agent is actually waiting on.
    private static func waitingSubtitle(_ status: String?) -> String {
        let raw = (status ?? "").trimmingCharacters(in: .whitespaces)
        guard let separator = raw.firstIndex(of: ":") else {
            return "Needs you"
        }

        let detail = raw[raw.index(after: separator)...].trimmingCharacters(in: .whitespaces)
        guard !detail.isEmpty else {
            return "Needs you"
        }
        return detail.prefix(1).uppercased() + detail.dropFirst()
    }
}
