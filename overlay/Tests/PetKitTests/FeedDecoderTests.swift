import XCTest
@testable import PetKit

final class FeedDecoderTests: XCTestCase {
    // Shaped from real `agent-sessions watch --once` output, fields and all: the
    // feed carries title, gitBranch, transcript, startedAt and a local-offset
    // lastActive that this side never reads, so decoding must ignore them rather
    // than reject the frame.
    let line = """
    {"at":"2026-08-28T13:16:00.309782Z","worst":"waiting","attention":1,"sessions":[{"agent":"claude","id":"a1b2","name":"api","title":"api","cwd":"/Users/dev/git/acme/api","gitBranch":"eng-3170","transcript":"/Users/dev/.claude/projects/-Users-dev-git-acme-api/a1b2.jsonl","live":true,"pid":51234,"status":"waiting: input needed","lastActive":"2026-08-28T09:15:50.507397801-04:00","startedAt":"2026-08-18T10:29:20.567-04:00"},{"agent":"claude","id":"c3d4","name":"web","cwd":"/Users/dev/git/acme/web","live":false,"lastActive":"2026-08-27T15:02:00Z"}]}
    """

    func testDecodesARealFeedLine() throws {
        let frame = try XCTUnwrap(FeedDecoder.decode(line: line))

        XCTAssertEqual(frame.worst, "waiting")
        XCTAssertEqual(frame.attention, 1)
        XCTAssertEqual(frame.sessions.count, 2)
        XCTAssertEqual(frame.sessions[0].pid, 51234)
        XCTAssertTrue(frame.sessions[0].isWaiting)
    }

    // A finished session carries no pid and no status at all, because Go omits an
    // empty one — requiring either would reject a perfectly good frame.
    func testDecodesASessionWithNoPidOrStatus() throws {
        let frame = try XCTUnwrap(FeedDecoder.decode(line: line))

        XCTAssertNil(frame.sessions[1].pid)
        XCTAssertNil(frame.sessions[1].status)
        XCTAssertFalse(frame.sessions[1].isWaiting)
    }

    func testRejectsRatherThanCrashes() {
        XCTAssertNil(FeedDecoder.decode(line: "warning: one transcript could not be read"))
        XCTAssertNil(FeedDecoder.decode(line: ""))
        XCTAssertNil(FeedDecoder.decode(line: "{\"worst\":"))
    }
}

extension FeedDecoderTests {
    // The card's summary needs the branch and the agent's own title, both of
    // which the feed already carries.
    func testDecodesTheFieldsTheCardSummaryShows() throws {
        let frame = try XCTUnwrap(FeedDecoder.decode(line: line))
        let waiting = frame.sessions[0]

        XCTAssertEqual(waiting.gitBranch, "eng-3170")
        XCTAssertEqual(waiting.title, "api")
        XCTAssertEqual(waiting.cwd, "/Users/dev/git/acme/api")
        XCTAssertEqual(waiting.agent, "claude")
    }

    // A session with no branch is ordinary — a detached head, or an agent that
    // never recorded one — so the summary must render without it.
    func testASessionWithNoBranchDecodes() throws {
        let frame = try XCTUnwrap(FeedDecoder.decode(line: line))

        XCTAssertNil(frame.sessions[1].gitBranch)
    }
}
