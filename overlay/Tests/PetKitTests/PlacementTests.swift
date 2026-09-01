import CoreGraphics
import XCTest
@testable import PetKit

final class PlacementTests: XCTestCase {
    // A 1000x800 screen whose origin is not zero, because a second display sits
    // at an offset and a placement computed from size alone lands on the wrong
    // monitor.
    let visible = CGRect(x: 100, y: 50, width: 1000, height: 800)

    func testBottomLeftSitsAtTheOffsetFromTheBottomLeft() {
        let frame = Placement.frame(corner: .bottomLeft, offset: CGPoint(x: 24, y: 24), size: 72, in: visible)
        XCTAssertEqual(frame, CGRect(x: 124, y: 74, width: 72, height: 72))
    }

    func testBottomRightMeasuresFromTheRightEdge() {
        let frame = Placement.frame(corner: .bottomRight, offset: CGPoint(x: 24, y: 24), size: 72, in: visible)
        XCTAssertEqual(frame, CGRect(x: 1004, y: 74, width: 72, height: 72))
    }

    // Cocoa's y grows upwards, so a top corner is the far edge minus the sprite.
    func testTopLeftMeasuresDownFromTheTopEdge() {
        let frame = Placement.frame(corner: .topLeft, offset: CGPoint(x: 24, y: 24), size: 72, in: visible)
        XCTAssertEqual(frame, CGRect(x: 124, y: 754, width: 72, height: 72))
    }

    func testTopRightMeasuresFromBothFarEdges() {
        let frame = Placement.frame(corner: .topRight, offset: CGPoint(x: 24, y: 24), size: 72, in: visible)
        XCTAssertEqual(frame, CGRect(x: 1004, y: 754, width: 72, height: 72))
    }

    // An offset larger than the screen would put the pet where nobody can see
    // it, which reads as the app having failed to start.
    func testAnOffsetBiggerThanTheScreenIsClamped() {
        let small = CGRect(x: 0, y: 0, width: 100, height: 100)
        let frame = Placement.frame(corner: .bottomLeft, offset: CGPoint(x: 500, y: 500), size: 72, in: small)
        XCTAssertTrue(small.contains(frame), "\(frame) is outside \(small)")
    }

    func testCornerParsesTheConfigSpelling() {
        XCTAssertEqual(Corner(rawValue: "bottom-left"), .bottomLeft)
        XCTAssertEqual(Corner(rawValue: "top-right"), .topRight)
        XCTAssertNil(Corner(rawValue: "middle"))
    }
}
