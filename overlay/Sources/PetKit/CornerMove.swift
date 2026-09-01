import Foundation

/// CornerMove is one step the pet can take: flip across an axis to a neighbouring
/// corner. Which steps exist depends on where it stands, which is why this is a
/// value the menu asks for rather than four items the menu draws and disables.
public struct CornerMove: Sendable, Equatable {
    public enum Direction: String, Sendable, Equatable {
        case left
        case right
        case up
        case down

        public var label: String {
            "Move " + rawValue
        }

        public var symbol: String {
            "arrow." + rawValue
        }
    }

    public let direction: Direction
    public let corner: Corner
}

public extension Corner {
    /// moves is where the pet can go from here: across, then up or down. Never a
    /// diagonal — that is two moves wearing one label — and never the corner it is
    /// already in.
    var moves: [CornerMove] {
        switch self {
        case .bottomLeft:
            return [CornerMove(direction: .right, corner: .bottomRight),
                    CornerMove(direction: .up, corner: .topLeft)]
        case .bottomRight:
            return [CornerMove(direction: .left, corner: .bottomLeft),
                    CornerMove(direction: .up, corner: .topRight)]
        case .topLeft:
            return [CornerMove(direction: .right, corner: .topRight),
                    CornerMove(direction: .down, corner: .bottomLeft)]
        case .topRight:
            return [CornerMove(direction: .left, corner: .topLeft),
                    CornerMove(direction: .down, corner: .bottomRight)]
        }
    }
}
