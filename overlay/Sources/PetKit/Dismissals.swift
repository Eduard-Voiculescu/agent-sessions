import Foundation

/// Dismissals remembers which pills have been waved away.
///
/// It cannot be a simple set of ids. The feed repeats itself every couple of
/// seconds, so a dismissal has to outlive the frame it was made in — and a
/// session that goes on to ask for something else has news, which a permanent
/// mute would swallow. So what is remembered is the id *and what it said*: the
/// pill stays hidden while it keeps saying the same thing.
///
/// Nothing here is persisted. Restarting the pet shows everything again, which is
/// the right default for a thing whose whole job is to tell you what is waiting.
public struct Dismissals: Sendable {
    private var silenced: [String: String] = [:]

    public init() {}

    public mutating func dismiss(_ pill: PetNotification) {
        silenced[pill.id] = pill.subtitle
    }

    public func visible(_ pills: [PetNotification]) -> [PetNotification] {
        pills.filter { silenced[$0.id] != $0.subtitle }
    }

    /// prune forgets sessions that are no longer notifying at all. Without it a
    /// session dismissed while waiting would still be silenced when it finished,
    /// started again and waited on something new hours later.
    public mutating func prune(against pills: [PetNotification]) {
        let live = Set(pills.map(\.id))
        silenced = silenced.filter { live.contains($0.key) }
    }
}
