import Foundation

public enum Mood: String, Sendable {
    case needsYou
    case working
    case idle
    case asleep
    case done
    case unknown
    case snoozed
}

extension Mood {
    /// face is what the creature's expression says. asleep and snoozed are
    /// deliberately different: one means nothing is running and the other means
    /// somebody muted a pet that has plenty to say, and a shared "zZ" made the
    /// two impossible to tell apart at a glance.
    public var face: String {
        switch self {
        // Scrunched up: an agent has been stuck waiting on a human long enough to
        // be uncomfortable about it.
        case .needsYou: return ">_<"
        case .working: return "o_o"
        case .idle: return "-_-"
        case .asleep: return "..."
        case .done: return "^_^"
        // A dead feed rather than a puzzled pet: the question-mark face belongs to
        // the mood that is actually asking you for something.
        case .unknown: return "x_x"
        case .snoozed: return "zZ"
        }
    }

    public func tooltip(attention: Int, snoozeLabel: String?) -> String {
        switch self {
        case .needsYou: return attention > 1 ? "\(attention) sessions want you" : "a session wants you"
        case .working: return "working"
        case .idle: return "idle"
        case .asleep: return "nothing running"
        case .done: return "a session just finished"
        case .unknown: return "no feed — is agent-sessions on PATH?"
        case .snoozed: return "snoozed\(snoozeLabel.map { " for \($0)" } ?? "") — right-click to wake"
        }
    }
}

/// MoodEngine turns a stream of frames into a face. It is a value type with an
/// injected clock, so the whole of it is testable without a timer.
public struct MoodEngine: Sendable {
    private let raise: Set<String>
    private let floor: TimeInterval
    private let transient: TimeInterval

    private var current: Mood = .unknown
    private var changedAt: TimeInterval = -.infinity
    private var doneUntil: TimeInterval = -.infinity
    private var busyIDs: Set<String> = []
    /// The last thing seen of each session, so a pill can still describe one whose
    /// process has since exited — which is the common case for a finish.
    private var lastSeen: [String: SessionInfo] = [:]
    private var finishedUntil: TimeInterval = -.infinity

    /// The sessions wanting a human, in feed order — what a click cycles through.
    public private(set) var attention: [SessionInfo] = []
    /// The sessions that have just stopped working, until their pills expire.
    public private(set) var finished: [SessionInfo] = []

    public init(raise: Set<String>, floor: TimeInterval = 2, transient: TimeInterval = 5) {
        self.raise = raise
        self.floor = floor
        self.transient = transient
    }

    /// snoozed defaults to false so the common call reads as it did before the pet
    /// could be told to keep quiet.
    public mutating func update(_ frame: Frame?, now: TimeInterval, snoozed: Bool = false) -> Mood {
        if snoozed {
            // Emptied rather than kept: the pills are drawn from these, so one rule
            // here is what silences them without a second check in the app.
            attention = []
            finished = []
            busyIDs = []
            return settle(.snoozed, now: now)
        }
        guard let frame else {
            attention = []
            finished = []
            busyIDs = []
            return settle(.unknown, now: now)
        }

        attention = raise.contains(frame.worst) ? frame.sessions.filter(\.isWaiting) : []

        for session in frame.sessions {
            lastSeen[session.id] = session
        }

        let nowBusy = Set(
            frame.sessions
                .filter { ($0.live ?? false) && !$0.isWaiting && ($0.status ?? "") != "idle" }
                .map(\.id)
        )
        let justFinished = busyIDs.subtracting(nowBusy)
        if !justFinished.isEmpty {
            doneUntil = now + transient
            finishedUntil = now + transient
            // Described from lastSeen rather than from this frame: a finished agent
            // has often exited by now and is in no frame at all.
            finished = justFinished.sorted().compactMap { lastSeen[$0] }
        } else if now >= finishedUntil {
            finished = []
        }
        busyIDs = nowBusy

        let feedMood = mood(for: frame)
        // Attention outranks the pulse: a second agent asking for a human while
        // the first one's "done" is on screen must not be swallowed by it.
        if feedMood != .needsYou, now < doneUntil {
            return settle(.done, now: now)
        }
        return settle(feedMood, now: now)
    }

    private func mood(for frame: Frame) -> Mood {
        if raise.contains(frame.worst), frame.attention > 0 {
            return .needsYou
        }
        switch frame.worst {
        case "none":
            return .asleep
        case "idle":
            return .idle
        default:
            return .working
        }
    }

    /// settle holds a mood for the floor, so a session flapping between classes
    /// cannot make the pet strobe. Attention is exempt: the whole point of the pet
    /// is that this one arrives late for nothing.
    private mutating func settle(_ next: Mood, now: TimeInterval) -> Mood {
        if next == current {
            return current
        }
        // Attention and snoozing are both exempt: one is why the pet exists, and
        // the other is a human who has just said "not now" and must be believed
        // on the next frame rather than two seconds later.
        if next != .needsYou, next != .snoozed, now - changedAt < floor {
            return current
        }
        current = next
        changedAt = now
        return current
    }
}
