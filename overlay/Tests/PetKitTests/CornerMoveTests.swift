import XCTest
@testable import PetKit

final class CornerMoveTests: XCTestCase {
    // Every corner offers exactly two moves: flip across one axis or the other.
    // Offering four would mean offering the corner it already sits in, and
    // offering diagonals would mean two moves dressed as one.
    func testEachCornerOffersTheTwoMovesThatMakeSense() {
        let expected: [Corner: [(CornerMove.Direction, Corner)]] = [
            .bottomLeft: [(.right, .bottomRight), (.up, .topLeft)],
            .bottomRight: [(.left, .bottomLeft), (.up, .topRight)],
            .topLeft: [(.right, .topRight), (.down, .bottomLeft)],
            .topRight: [(.left, .topLeft), (.down, .bottomRight)],
        ]

        for (corner, want) in expected {
            let moves = corner.moves
            XCTAssertEqual(moves.count, 2, "\(corner) offers \(moves.count) moves")
            XCTAssertEqual(moves.map(\.direction), want.map(\.0), "\(corner) directions")
            XCTAssertEqual(moves.map(\.corner), want.map(\.1), "\(corner) destinations")
        }
    }

    func testNoMoveLeadsBackToWhereItAlreadyIs() {
        for corner in Corner.allCases {
            for move in corner.moves {
                XCTAssertNotEqual(move.corner, corner, "\(corner) offers a move to itself")
            }
        }
    }

    // Moving right then left has to come home, or the menu would walk the pet
    // somewhere it cannot walk back from.
    func testEveryMoveCanBeUndone() {
        for corner in Corner.allCases {
            for move in corner.moves {
                let back = move.corner.moves.map(\.corner)
                XCTAssertTrue(back.contains(corner), "\(corner) → \(move.corner) is one-way")
            }
        }
    }

    func testAMoveIsLabelledByItsDirection() {
        XCTAssertEqual(CornerMove.Direction.right.label, "Move right")
        XCTAssertEqual(CornerMove.Direction.up.label, "Move up")
        XCTAssertEqual(CornerMove.Direction.down.symbol, "arrow.down")
    }

    // The settings carry the corner, so moving is a new settings value rather than
    // a mutation somewhere the window cannot see.
    func testMovingKeepsEverySettingButTheCorner() {
        let moved = Settings.fallback.moved(to: .topRight)

        XCTAssertEqual(moved.corner, .topRight)
        XCTAssertEqual(moved.offset, Settings.fallback.offset)
        XCTAssertEqual(moved.size, Settings.fallback.size)
        XCTAssertEqual(moved.raise, Settings.fallback.raise)
        XCTAssertEqual(moved.sound, Settings.fallback.sound)
    }
}
