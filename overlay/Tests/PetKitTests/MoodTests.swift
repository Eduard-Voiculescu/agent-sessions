import XCTest
@testable import PetKit

private func frame(worst: String, attention: Int = 1, sessions: [SessionInfo] = []) -> Frame {
    Frame(at: "2026-08-27T16:04:02Z", worst: worst, attention: attention, sessions: sessions)
}

private func session(id: String, status: String, live: Bool = true, pid: Int = 1) -> SessionInfo {
    SessionInfo(agent: "claude", id: id, name: id, cwd: "/tmp/\(id)", live: live, pid: pid, status: status)
}

final class MoodTests: XCTestCase {
    func testAWaitingFeedRaisesThePet() {
        var engine = MoodEngine(raise: ["waiting"])
        XCTAssertEqual(engine.update(frame(worst: "waiting"), now: 0), .needsYou)
    }

    // `raise` is configurable, so a class the owner did not list must not raise
    // the pet even though the CLI ranked it worst.
    func testAClassNotListedInRaiseDoesNotRaiseThePet() {
        var engine = MoodEngine(raise: ["waiting"])
        XCTAssertEqual(engine.update(frame(worst: "blocked"), now: 0), .working)
    }

    func testBusyIdleAndEmptyMapToTheirOwnMoods() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0)
        XCTAssertEqual(engine.update(frame(worst: "busy"), now: 0), .working)
        XCTAssertEqual(engine.update(frame(worst: "idle"), now: 1), .idle)
        XCTAssertEqual(engine.update(frame(worst: "none", attention: 0), now: 2), .asleep)
    }

    func testNoFeedIsUnknown() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0)
        XCTAssertEqual(engine.update(nil, now: 0), .unknown)
    }

    // A mood holds for the floor so an agent flapping between busy and waiting
    // cannot make the pet strobe.
    func testAMoodHoldsForTheFloor() {
        var engine = MoodEngine(raise: ["waiting"], floor: 2)
        XCTAssertEqual(engine.update(frame(worst: "waiting"), now: 0), .needsYou)
        XCTAssertEqual(engine.update(frame(worst: "busy"), now: 1), .needsYou)
        XCTAssertEqual(engine.update(frame(worst: "busy"), now: 2.5), .working)
    }

    // A session leaving the busy class is the other thing worth knowing
    // off-screen, and the previous frame is what makes it visible.
    func testASessionLeavingBusyPulsesDone() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 5)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)

        let finished = frame(worst: "idle", sessions: [session(id: "a", status: "idle")])
        XCTAssertEqual(engine.update(finished, now: 1), .done)
    }

    func testDoneDecaysBackToTheFeed() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 5)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)
        let finished = frame(worst: "idle", sessions: [session(id: "a", status: "idle")])
        XCTAssertEqual(engine.update(finished, now: 1), .done)
        XCTAssertEqual(engine.update(finished, now: 7), .idle)
    }

    // Attention outranks the transient: a second agent asking for a human while
    // the first one's "done" pulse is on screen must not be swallowed by it.
    func testNeedsYouInterruptsDone() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 5)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)
        _ = engine.update(frame(worst: "idle", sessions: [session(id: "a", status: "idle")]), now: 1)

        let waiting = frame(worst: "waiting", sessions: [session(id: "b", status: "waiting: input needed")])
        XCTAssertEqual(engine.update(waiting, now: 2), .needsYou)
    }

    func testTheAttentionSetIsTheSessionsWaiting() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0)
        let waiting = frame(worst: "waiting", attention: 2, sessions: [
            session(id: "a", status: "waiting: input needed"),
            session(id: "b", status: "busy"),
            session(id: "c", status: "waiting: permission"),
        ])
        _ = engine.update(waiting, now: 0)

        XCTAssertEqual(engine.attention.map(\.id), ["a", "c"])
    }
}

extension MoodTests {
    // Snoozing is a human decision, so it takes effect on the next frame rather
    // than waiting out the floor — and it empties the attention set, which is
    // what keeps the card hidden without a second check anywhere else.
    func testSnoozeSuppressesAttention() {
        var engine = MoodEngine(raise: ["waiting"], floor: 2)
        let waiting = frame(worst: "waiting", sessions: [session(id: "a", status: "waiting: input needed")])

        XCTAssertEqual(engine.update(waiting, now: 0), .needsYou)
        XCTAssertEqual(engine.update(waiting, now: 0.5, snoozed: true), .snoozed)
        XCTAssertTrue(engine.attention.isEmpty, "a snoozed pet has nothing to click through")
    }

    func testWakingUpRaisesAgainImmediately() {
        var engine = MoodEngine(raise: ["waiting"], floor: 2)
        let waiting = frame(worst: "waiting", sessions: [session(id: "a", status: "waiting: input needed")])

        _ = engine.update(waiting, now: 0, snoozed: true)
        XCTAssertEqual(engine.update(waiting, now: 0.5), .needsYou)
        XCTAssertEqual(engine.attention.map(\.id), ["a"])
    }

    // A frame that never arrived while snoozed must not resurrect the feed's mood:
    // the pet is asleep, not disconnected.
    func testSnoozeOutranksAMissingFeed() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0)

        XCTAssertEqual(engine.update(nil, now: 0, snoozed: true), .snoozed)
    }
}

extension MoodTests {
    // These two are the pair a glance has to separate: nothing running versus a
    // pet that was told to keep quiet. They shared a face once and it read as a
    // bug in the feed.
    func testAsleepAndSnoozedDoNotLookAlike() {
        XCTAssertNotEqual(Mood.asleep.face, Mood.snoozed.face)
        XCTAssertNotEqual(Mood.asleep.tooltip(attention: 0, snoozeLabel: nil),
                          Mood.snoozed.tooltip(attention: 0, snoozeLabel: "15m"))
    }

    func testEveryMoodHasAFace() {
        for mood in [Mood.needsYou, .working, .idle, .asleep, .done, .unknown, .snoozed] {
            XCTAssertFalse(mood.face.isEmpty, "\(mood) has no face")
        }
    }
}

extension MoodTests {
    // The pet is read at a glance from the corner of an eye, so two moods sharing
    // an expression is the one bug its whole purpose cannot survive.
    func testEveryMoodWearsADifferentFace() {
        let moods: [Mood] = [.needsYou, .working, .idle, .asleep, .done, .unknown, .snoozed]
        let faces = moods.map(\.face)

        XCTAssertEqual(Set(faces).count, moods.count, "two moods share a face: \(faces)")
    }
}

extension MoodTests {
    // A green "Ready" pill needs to know which session finished, not merely that
    // one did — and it has to survive the process disappearing, since a finished
    // agent often exits before anybody looks.
    func testAFinishedSessionIsRememberedForItsPill() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 8)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)

        _ = engine.update(frame(worst: "idle", sessions: [session(id: "a", status: "idle")]), now: 1)

        XCTAssertEqual(engine.finished.map(\.id), ["a"])
    }

    func testAFinishedSessionIsForgottenWhenItsPillExpires() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 8)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)
        _ = engine.update(frame(worst: "idle", sessions: [session(id: "a", status: "idle")]), now: 1)

        _ = engine.update(frame(worst: "idle", sessions: [session(id: "a", status: "idle")]), now: 10)

        XCTAssertTrue(engine.finished.isEmpty)
    }

    // The common case: an agent finishes and its process is gone by the next
    // frame, so the pill has to be built from what was last seen of it.
    func testASessionThatExitedStillGetsItsPill() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 8)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)

        _ = engine.update(frame(worst: "none", attention: 0, sessions: []), now: 1)

        XCTAssertEqual(engine.finished.map(\.id), ["a"])
    }

    func testSnoozingClearsTheFinishedPillsToo() {
        var engine = MoodEngine(raise: ["waiting"], floor: 0, transient: 8)
        _ = engine.update(frame(worst: "busy", sessions: [session(id: "a", status: "busy")]), now: 0)
        _ = engine.update(frame(worst: "idle", sessions: [session(id: "a", status: "idle")]), now: 1)

        _ = engine.update(frame(worst: "idle", sessions: []), now: 2, snoozed: true)

        XCTAssertTrue(engine.finished.isEmpty)
    }
}
