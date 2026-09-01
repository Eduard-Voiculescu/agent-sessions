import CoreGraphics
import XCTest
@testable import PetKit

final class SettingsTests: XCTestCase {
    // Shaped from real `agent-sessions config --json`, trimmed to the block the
    // pet reads plus a couple of keys it must ignore.
    let document = """
    {"file":"/Users/dev/.agent-sessions/config","limit":200,"live":false,
     "overlay":{"corner":"top-right","offset":[40,12],"size":96,"raise":["waiting","blocked"],"sound":true}}
    """

    func testReadsTheOverlayBlock() throws {
        let settings = try XCTUnwrap(Settings.decode(document))

        XCTAssertEqual(settings.corner, .topRight)
        XCTAssertEqual(settings.offset, CGPoint(x: 40, y: 12))
        XCTAssertEqual(settings.size, 96)
        XCTAssertEqual(settings.raise, ["waiting", "blocked"])
        XCTAssertTrue(settings.sound)
    }

    // The pet must draw itself somewhere even if the CLI cannot be reached, so
    // the defaults are duplicated here on purpose and match the spec's table.
    func testFallsBackToTheDocumentedDefaults() {
        let settings = Settings.fallback

        XCTAssertEqual(settings.corner, .bottomLeft)
        XCTAssertEqual(settings.offset, CGPoint(x: 24, y: 24))
        XCTAssertEqual(settings.size, 72)
        XCTAssertEqual(settings.raise, ["waiting"])
        XCTAssertFalse(settings.sound)
    }

    func testAnUnknownCornerFallsBackRatherThanThrowing() throws {
        let settings = try XCTUnwrap(Settings.decode("{\"overlay\":{\"corner\":\"middle\",\"offset\":[1,2],\"size\":8,\"raise\":[],\"sound\":false}}"))
        XCTAssertEqual(settings.corner, .bottomLeft)
    }
}
