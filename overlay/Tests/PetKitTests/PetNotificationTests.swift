import XCTest
@testable import PetKit

final class PetNotificationTests: XCTestCase {
    private func session(_ status: String?, name: String = "api", pid: Int? = 51234) -> SessionInfo {
        SessionInfo(agent: "claude", id: "a1b2", name: name, title: nil, cwd: "/Users/dev/git/acme/api",
                    gitBranch: "eng-3170", live: true, pid: pid, status: status)
    }

    // The feed's status is a machine string with its class glued on the front.
    // A pill has one line for it, so the class goes and the rest reads as English.
    func testAWaitingPillSaysWhatItIsWaitingFor() {
        let pill = PetNotification.waiting(session("waiting: input needed"))

        XCTAssertEqual(pill.title, "api")
        XCTAssertEqual(pill.subtitle, "Input needed")
        XCTAssertEqual(pill.kind, .waiting)
        XCTAssertEqual(pill.pid, 51234)
    }

    func testAWaitingPillWithNothingMoreToSayStillSaysSomething() {
        XCTAssertEqual(PetNotification.waiting(session("waiting")).subtitle, "Needs you")
        XCTAssertEqual(PetNotification.waiting(session(nil)).subtitle, "Needs you")
    }

    func testAFinishedPillReadsAsReady() {
        let pill = PetNotification.ready(session("idle"))

        XCTAssertEqual(pill.kind, .ready)
        XCTAssertEqual(pill.subtitle, "Ready")
    }

    // The summary the expanded pill shows, carried on the pill so the view does no
    // digging of its own.
    func testAPillCarriesItsSummary() {
        let pill = PetNotification.waiting(session("waiting: permission"))

        XCTAssertEqual(pill.agent, "claude")
        XCTAssertEqual(pill.branch, "eng-3170")
        XCTAssertEqual(pill.directory, "/Users/dev/git/acme/api")
    }

    func testTitleFallsBackToTheSessionsOwnLabel() {
        let unnamed = SessionInfo(agent: "claude", id: "c3d4", name: nil, title: nil, cwd: nil,
                                  gitBranch: nil, live: true, pid: 7, status: "waiting")

        XCTAssertEqual(PetNotification.waiting(unnamed).title, "c3d4")
    }
}
