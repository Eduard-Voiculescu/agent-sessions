import XCTest
@testable import PetKit

final class SnoozeTests: XCTestCase {
    // A fixed instant so "next 9am" has a definite answer. 2026-08-28 07:00 local.
    private func at(_ hour: Int, _ minute: Int = 0) -> Date {
        var parts = DateComponents()
        parts.year = 2026
        parts.month = 8
        parts.day = 28
        parts.hour = hour
        parts.minute = minute
        return Calendar.current.date(from: parts)!
    }

    func testAwakeByDefault() {
        let snooze = Snooze()

        XCTAssertFalse(snooze.isActive(at: at(12)))
        XCTAssertNil(snooze.label(at: at(12)))
    }

    func testAFixedDurationExpires() {
        let snooze = Snooze.starting(at(12), duration: .minutes(15))

        XCTAssertTrue(snooze.isActive(at: at(12, 14)))
        XCTAssertFalse(snooze.isActive(at: at(12, 16)))
    }

    func testTheLabelCountsDownInWholeMinutes() {
        let snooze = Snooze.starting(at(12), duration: .minutes(15))

        XCTAssertEqual(snooze.label(at: at(12)), "15m")
        XCTAssertEqual(snooze.label(at: at(12, 14)), "1m")
    }

    func testALongSnoozeReadsInHours() {
        let snooze = Snooze.starting(at(12), duration: .minutes(180))

        XCTAssertEqual(snooze.label(at: at(12)), "3h")
    }

    // "until 9am" means the next 9am, so asking for it at 07:00 is two hours and
    // not twenty-six: a snooze that outlasts the working day by accident is worse
    // than one that ends too early.
    func testUntilMorningTakesTheNextNineAM() {
        let early = Snooze.starting(at(7), duration: .untilMorning(hour: 9))
        XCTAssertTrue(early.isActive(at: at(8, 59)))
        XCTAssertFalse(early.isActive(at: at(9, 1)))

        let late = Snooze.starting(at(11), duration: .untilMorning(hour: 9))
        XCTAssertTrue(late.isActive(at: at(23, 59)))
    }

    func testIndefiniteNeverExpiresOnItsOwn() {
        let snooze = Snooze.starting(at(12), duration: .indefinite)

        XCTAssertTrue(snooze.isActive(at: Date.distantFuture))
        XCTAssertEqual(snooze.label(at: at(12)), "on")
    }
}
