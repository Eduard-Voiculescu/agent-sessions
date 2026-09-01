import Foundation

public enum FeedDecoder {
    /// decode returns nil rather than throwing: the feed's stdout carries only
    /// frames, but a line that is not one must cost the pet a dropped tick and
    /// nothing more.
    public static func decode(line: String) -> Frame? {
        guard let data = line.data(using: .utf8), !data.isEmpty else { return nil }
        return try? JSONDecoder().decode(Frame.self, from: data)
    }
}
