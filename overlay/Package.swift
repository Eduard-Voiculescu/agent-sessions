// swift-tools-version: 6.0
import PackageDescription

// The language mode is 5 on purpose: this app's substance is a window and a
// sprite, and Swift 6's strict concurrency checking would turn a few hundred
// lines of AppKit glue into an exercise in Sendable conformances.
let mode: [SwiftSetting] = [.swiftLanguageMode(.v5)]

let package = Package(
    name: "AgentPet",
    platforms: [.macOS(.v13)],
    targets: [
        // The logic lives in a library so it can be tested without an
        // NSApplication: placement, mood and decoding are the parts worth
        // testing, and a test bundle that has to launch an app cannot run
        // headless.
        .target(name: "PetKit", swiftSettings: mode),
        .executableTarget(name: "AgentPet", dependencies: ["PetKit"], swiftSettings: mode),
        .testTarget(name: "PetKitTests", dependencies: ["PetKit"], swiftSettings: mode),
    ]
)
