import CoreGraphics
import XCTest
@testable import PetKit

final class StackLayoutTests: XCTestCase {
    let visible = CGRect(x: 0, y: 0, width: 1440, height: 900)

    private func pet(_ corner: Corner) -> CGRect {
        Placement.frame(corner: corner, offset: CGPoint(x: 24, y: 24), size: 72, in: visible)
    }

    // Pills rise off the top of a creature standing in a bottom corner, and hang
    // below one in a top corner: away from the edge it was placed against.
    func testPillsRiseAboveABottomPet() {
        let sprite = pet(.bottomLeft)
        let frame = StackLayout.frame(pet: sprite, corner: .bottomLeft, pills: 2, expanded: false, in: visible)

        XCTAssertGreaterThan(frame.minY, sprite.maxY)
        XCTAssertTrue(visible.contains(frame), "\(frame) is outside \(visible)")
    }

    func testPillsHangBelowATopPet() {
        let sprite = pet(.topRight)
        let frame = StackLayout.frame(pet: sprite, corner: .topRight, pills: 2, expanded: false, in: visible)

        XCTAssertLessThan(frame.maxY, sprite.minY)
    }

    // The badge straddles the sprite's top-right corner, so a stack that merely
    // sits above the sprite cuts through the count.
    func testTheStackClearsTheBadge() {
        let sprite = pet(.bottomLeft)
        let frame = StackLayout.frame(pet: sprite, corner: .bottomLeft, pills: 1, expanded: false, in: visible)

        XCTAssertGreaterThanOrEqual(frame.minY, sprite.maxY + 11, "the stack overlaps the badge")
    }

    // A 300-point pill centred on a 72-point sprite 24 points from the screen edge
    // would hang off it, so the stack aligns to the sprite's near edge instead.
    func testTheStackAlignsToTheNearEdge() {
        let left = StackLayout.frame(pet: pet(.bottomLeft), corner: .bottomLeft, pills: 1, expanded: false, in: visible)
        XCTAssertEqual(left.minX, pet(.bottomLeft).minX, accuracy: 0.5)

        let right = StackLayout.frame(pet: pet(.bottomRight), corner: .bottomRight, pills: 1, expanded: false, in: visible)
        XCTAssertEqual(right.maxX, pet(.bottomRight).maxX, accuracy: 0.5)
    }

    func testEveryCornerKeepsTheStackOnScreen() {
        for corner in Corner.allCases {
            let frame = StackLayout.frame(pet: pet(corner), corner: corner, pills: 4, expanded: true, in: visible)
            XCTAssertTrue(visible.contains(frame), "\(corner): \(frame) is outside \(visible)")
        }
    }

    func testMorePillsMeansATallerStack() {
        let one = StackLayout.height(pills: 1, expanded: false)
        let three = StackLayout.height(pills: 3, expanded: false)

        XCTAssertGreaterThan(three, one)
    }

    // Past the cap the extras peek out behind rather than growing the stack, so
    // twelve waiting sessions and five are the same height.
    func testThePeekedOverflowDoesNotGrowTheStack() {
        // The ceiling is the cap plus the sliver the peeked ones show through, so
        // the comparison is against the first overflowing count, not the cap.
        let ceiling = StackLayout.height(pills: StackLayout.maxPills + 1, expanded: false)

        XCTAssertEqual(StackLayout.height(pills: 12, expanded: false), ceiling, accuracy: 0.5)
        XCTAssertGreaterThan(ceiling, StackLayout.height(pills: StackLayout.maxPills, expanded: false))
    }

    func testExpandingAPillMakesRoomForItsSummary() {
        XCTAssertGreaterThan(
            StackLayout.height(pills: 2, expanded: true),
            StackLayout.height(pills: 2, expanded: false)
        )
    }
}
