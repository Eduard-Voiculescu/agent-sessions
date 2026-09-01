import CoreGraphics

/// Where the pet sits. The raw values are the config file's own spelling, so one
/// vocabulary crosses the seam between the two halves of this program.
public enum Corner: String, Codable, CaseIterable, Sendable {
    case bottomLeft = "bottom-left"
    case bottomRight = "bottom-right"
    case topLeft = "top-left"
    case topRight = "top-right"
}

public enum Placement {
    /// The pet's frame in screen coordinates. `visible` is meant to be
    /// `NSScreen.visibleFrame`, which already excludes the menu bar and the Dock,
    /// and whose origin is not zero on a second display.
    ///
    /// The result is clamped inside `visible`: an offset larger than the screen
    /// would place the pet where nobody can see it, which reads as the app having
    /// failed to launch rather than as a bad setting.
    public static func frame(corner: Corner, offset: CGPoint, size: CGFloat, in visible: CGRect) -> CGRect {
        let x: CGFloat
        let y: CGFloat

        switch corner {
        case .bottomLeft:
            x = visible.minX + offset.x
            y = visible.minY + offset.y
        case .bottomRight:
            x = visible.maxX - offset.x - size
            y = visible.minY + offset.y
        case .topLeft:
            x = visible.minX + offset.x
            y = visible.maxY - offset.y - size
        case .topRight:
            x = visible.maxX - offset.x - size
            y = visible.maxY - offset.y - size
        }

        let clampedX = min(max(x, visible.minX), max(visible.maxX - size, visible.minX))
        let clampedY = min(max(y, visible.minY), max(visible.maxY - size, visible.minY))
        return CGRect(x: clampedX, y: clampedY, width: size, height: size)
    }
}
