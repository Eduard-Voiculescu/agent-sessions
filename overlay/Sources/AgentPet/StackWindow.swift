import AppKit
import PetKit

/// StackWindow holds the notification pills over the creature. It is a second
/// window rather than a taller pet window because the sprite's frame is what
/// Placement decided: growing one window to hold pills would move the creature
/// every time a session started waiting.
final class StackWindow: NSWindow {
    init() {
        super.init(contentRect: .zero, styleMask: [.borderless], backing: .buffered, defer: false)

        level = .statusBar
        isOpaque = false
        backgroundColor = .clear
        hasShadow = true
        collectionBehavior = [.canJoinAllSpaces, .stationary]
        isMovableByWindowBackground = false
    }

    // Like the pet, the pills must never take focus: they appear over whatever you
    // are typing in, and a window that grabs the keyboard when an agent happens to
    // ask a question would be worse than no notification at all.
    override var canBecomeKey: Bool { false }

    /// screen is the creature's own, not `NSScreen.main`: main is wherever the
    /// keyboard focus is, so on a second display the pills would be clamped into
    /// the screen the human is typing on while the pet stands on the other one.
    func place(pet: NSRect, corner: Corner, pills: Int, expanded: Bool, screen: NSScreen?) {
        let visible = (screen ?? NSScreen.main)?.visibleFrame ?? CGRect(x: 0, y: 0, width: 1440, height: 900)
        setFrame(
            StackLayout.frame(pet: pet, corner: corner, pills: pills, expanded: expanded, in: visible),
            display: true
        )
    }
}
