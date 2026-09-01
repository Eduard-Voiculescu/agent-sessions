import CoreGraphics
import Foundation

/// Settings is the pet's half of the configuration, read through
/// `agent-sessions config --json` so the config format is parsed in one language.
public struct Settings: Sendable, Equatable {
    public let corner: Corner
    public let offset: CGPoint
    public let size: CGFloat
    public let raise: Set<String>
    public let sound: Bool

    public init(corner: Corner, offset: CGPoint, size: CGFloat, raise: Set<String>, sound: Bool) {
        self.corner = corner
        self.offset = offset
        self.size = size
        self.raise = raise
        self.sound = sound
    }

    /// The documented defaults, duplicated here on purpose: the pet has to draw
    /// itself somewhere even when the CLI cannot be reached at all.
    public static let fallback = Settings(
        corner: .bottomLeft,
        offset: CGPoint(x: 24, y: 24),
        size: 72,
        raise: ["waiting"],
        sound: false
    )

    private struct Document: Decodable {
        struct Overlay: Decodable {
            let corner: String
            let offset: [Int]
            let size: Int
            let raise: [String]
            let sound: Bool
        }

        let overlay: Overlay
    }

    public static func decode(_ json: String) -> Settings? {
        guard let data = json.data(using: .utf8),
              let document = try? JSONDecoder().decode(Document.self, from: data)
        else { return nil }

        let overlay = document.overlay
        let offset = overlay.offset.count == 2
            ? CGPoint(x: overlay.offset[0], y: overlay.offset[1])
            : fallback.offset

        return Settings(
            // An unknown corner falls back rather than refusing to start: the CLI
            // rejects one at parse time, so reaching here means the two halves
            // disagree, and a pet in the wrong corner beats no pet.
            corner: Corner(rawValue: overlay.corner) ?? fallback.corner,
            offset: offset,
            size: overlay.size > 0 ? CGFloat(overlay.size) : fallback.size,
            raise: overlay.raise.isEmpty ? fallback.raise : Set(overlay.raise),
            sound: overlay.sound
        )
    }

    /// moved is this configuration in another corner. The corner travels in the
    /// settings rather than in a variable beside them, because the window and the
    /// pill stack both place themselves from this one value — a second copy is a
    /// second thing to forget to update.
    ///
    /// Not written back to the config file: the CLI owns that file, comments and
    /// all, and a pet that rewrote it would be a second author of somebody else's
    /// document. A move lasts until the pet is restarted.
    public func moved(to corner: Corner) -> Settings {
        Settings(corner: corner, offset: offset, size: size, raise: raise, sound: sound)
    }

    /// load runs the CLI once at launch. A pet that cannot read its settings uses
    /// the fallback rather than exiting: it is an ornament, not a gate.
    public static func load(binary: String = "agent-sessions") -> Settings {
        guard let output = Shell.run(binary, ["config", "--json"]) else { return fallback }
        return decode(output) ?? fallback
    }
}
