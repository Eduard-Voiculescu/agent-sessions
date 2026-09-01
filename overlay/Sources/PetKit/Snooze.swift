import Foundation

/// Snooze is how long the pet has been told to keep quiet. It is deliberately
/// not persisted: a pet that started up silent with no visible reason why would
/// read as broken, so restarting it always wakes it.
public struct Snooze: Sendable, Equatable {
    public enum Duration: Sendable, Equatable {
        case minutes(Int)
        /// The next occurrence of this hour — today if it is still ahead, else
        /// tomorrow. Asking for "until 9am" at 07:00 means two hours, not
        /// twenty-six: a snooze that outlasts the working day by accident is
        /// worse than one that ends too early.
        case untilMorning(hour: Int)
        case indefinite
    }

    /// nil means awake. Date.distantFuture is the indefinite case, which is what
    /// lets isActive stay a single comparison.
    public private(set) var until: Date?

    public init(until: Date? = nil) {
        self.until = until
    }

    public static func starting(_ now: Date, duration: Duration, calendar: Calendar = .current) -> Snooze {
        switch duration {
        case let .minutes(count):
            return Snooze(until: now.addingTimeInterval(TimeInterval(count) * 60))
        case let .untilMorning(hour):
            return Snooze(until: nextOccurrence(of: hour, after: now, calendar: calendar))
        case .indefinite:
            return Snooze(until: .distantFuture)
        }
    }

    public func isActive(at now: Date) -> Bool {
        guard let until else { return false }
        // The indefinite case is checked rather than compared: distantFuture is
        // not less than itself, so a plain comparison reports the one snooze that
        // never expires as awake at exactly the moment it matters least and reads
        // worst.
        if until == .distantFuture {
            return true
        }
        return now < until
    }

    /// label is what the menu and the badge show: whole minutes under an hour,
    /// whole hours above it, and "on" for a snooze with no end. Nil when awake,
    /// so a caller cannot draw a countdown that is not running.
    public func label(at now: Date) -> String? {
        guard let until, isActive(at: now) else { return nil }
        if until == .distantFuture {
            return "on"
        }

        let remaining = until.timeIntervalSince(now)
        if remaining >= 3600 {
            return "\(Int((remaining / 3600).rounded()))h"
        }
        return "\(max(Int((remaining / 60).rounded(.up)), 1))m"
    }

    private static func nextOccurrence(of hour: Int, after now: Date, calendar: Calendar) -> Date {
        var parts = calendar.dateComponents([.year, .month, .day], from: now)
        parts.hour = hour
        parts.minute = 0
        parts.second = 0

        guard let today = calendar.date(from: parts) else {
            return now.addingTimeInterval(3600)
        }
        if today > now {
            return today
        }
        return calendar.date(byAdding: .day, value: 1, to: today) ?? today.addingTimeInterval(86400)
    }
}
