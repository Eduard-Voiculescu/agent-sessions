import XCTest
@testable import PetKit

final class DismissalsTests: XCTestCase {
    private func pill(_ id: String, _ status: String) -> PetNotification {
        PetNotification.waiting(SessionInfo(
            agent: "claude", id: id, name: id, title: nil, cwd: "/tmp/\(id)",
            gitBranch: nil, live: true, pid: 1, status: status
        ))
    }

    func testDismissingHidesThatPillAndLeavesTheRest() {
        let pills = [pill("a", "waiting: input needed"), pill("b", "waiting: permission")]
        var dismissals = Dismissals()

        dismissals.dismiss(pills[0])

        XCTAssertEqual(dismissals.visible(pills).map(\.id), ["b"])
    }

    // The feed repeats itself every couple of seconds, so a dismissal that only
    // skipped one frame would be no dismissal at all.
    func testADismissalSurvivesTheFeedRepeatingItself() {
        let pills = [pill("a", "waiting: input needed")]
        var dismissals = Dismissals()
        dismissals.dismiss(pills[0])

        for _ in 0..<5 {
            dismissals.prune(against: pills)
            XCTAssertTrue(dismissals.visible(pills).isEmpty)
        }
    }

    // Dismissing means "I have seen this", not "never speak of this session
    // again": when it goes on to ask for something else, that is news.
    func testTheSameSessionComesBackWithSomethingNewToSay() {
        var dismissals = Dismissals()
        dismissals.dismiss(pill("a", "waiting: input needed"))

        let asking = [pill("a", "waiting: permission")]

        XCTAssertEqual(dismissals.visible(asking).map(\.id), ["a"])
    }

    // A session that stops waiting and later waits again is a new event, so the
    // old dismissal must not silence it.
    func testASessionThatStopsWaitingIsForgotten() {
        var dismissals = Dismissals()
        let waiting = [pill("a", "waiting: input needed")]
        dismissals.dismiss(waiting[0])

        dismissals.prune(against: [])

        XCTAssertEqual(dismissals.visible(waiting).map(\.id), ["a"])
    }

    func testAReadyPillCanBeDismissedToo() {
        let ready = PetNotification.ready(SessionInfo(
            agent: "claude", id: "c", name: "pipeline", title: nil, cwd: nil,
            gitBranch: nil, live: true, pid: 3, status: "idle"
        ))
        var dismissals = Dismissals()

        dismissals.dismiss(ready)

        XCTAssertTrue(dismissals.visible([ready]).isEmpty)
    }

    func testNothingIsHiddenBeforeAnythingIsDismissed() {
        let pills = [pill("a", "waiting: input needed"), pill("b", "waiting: permission")]

        XCTAssertEqual(Dismissals().visible(pills).count, 2)
    }
}
