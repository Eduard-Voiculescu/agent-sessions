import CoreGraphics

/// StackLayout places the notification pills. They stack away from the corner the
/// creature stands in — rising off its head in a bottom corner, hanging below it
/// in a top one — because a stack that grew towards the screen edge would run out
/// of screen immediately.
///
/// It replaces the single card this used to draw beside the pet: one pill per
/// session reads at a glance where a list of rows had to be read.
public enum StackLayout {
    public static let pillWidth: CGFloat = 300
    public static let pillHeight: CGFloat = 54
    /// What an opened pill adds: three lines of summary under its two.
    public static let expandedHeight: CGFloat = 46
    public static let pillGap: CGFloat = 8
    /// Pills drawn in full. Past this the extras peek out behind the far one, so
    /// twelve waiting sessions and five are the same height.
    public static let maxPills = 4
    /// How far the peeked pills show behind the stack.
    public static let peekHeight: CGFloat = 10
    /// The gap over the creature's head. It has to clear the badge, which
    /// straddles the sprite's top corner by half its own width.
    public static let badgeClearance: CGFloat = 16

    public static func height(pills: Int, expanded: Bool) -> CGFloat {
        let drawn = min(max(pills, 1), maxPills)
        let peeked: CGFloat = pills > maxPills ? peekHeight : 0
        return CGFloat(drawn) * pillHeight
            + CGFloat(max(drawn - 1, 0)) * pillGap
            + peeked
            + (expanded ? expandedHeight : 0)
    }

    /// frame is the window the whole stack draws into. It is aligned to the
    /// sprite's near edge rather than centred on it: a 300-point pill centred on a
    /// 72-point creature standing 24 points from the screen edge hangs off it.
    public static func frame(pet: CGRect, corner: Corner, pills: Int, expanded: Bool, in visible: CGRect) -> CGRect {
        let stackHeight = height(pills: pills, expanded: expanded)

        let x: CGFloat
        switch corner {
        case .bottomLeft, .topLeft:
            x = pet.minX
        case .bottomRight, .topRight:
            x = pet.maxX - pillWidth
        }

        let y: CGFloat
        switch corner {
        case .bottomLeft, .bottomRight:
            y = pet.maxY + badgeClearance
        case .topLeft, .topRight:
            y = pet.minY - badgeClearance - stackHeight
        }

        let drawnWidth = min(pillWidth, visible.width)
        let drawnHeight = min(stackHeight, visible.height)
        let clampedX = min(max(x, visible.minX), max(visible.maxX - drawnWidth, visible.minX))
        let clampedY = min(max(y, visible.minY), max(visible.maxY - drawnHeight, visible.minY))
        return CGRect(x: clampedX, y: clampedY, width: drawnWidth, height: drawnHeight)
    }
}
