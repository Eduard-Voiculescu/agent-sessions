import AppKit
import PetKit

/// PetWindow is the floating sprite. Everything unusual about it is deliberate:
/// `.statusBar` level puts it over ordinary windows, `canJoinAllSpaces` keeps it
/// on screen when you switch desktops, `stationary` stops Mission Control
/// animating it away, and a clear window background is what lets the layer's
/// rounded corners read as a creature rather than a box.
///
/// The window is larger than the creature by `overhang` on every side. A window
/// cannot draw outside its own frame and the sprite's layer clips to its own
/// bounds, so a badge that straddles the corner the way an app icon's does needs
/// that margin to live in.
final class PetWindow: NSWindow {
    /// The sprite's corner radius, read by whoever builds the layer that rounds
    /// it. A squircle at roughly a quarter of the box, which is the proportion
    /// AppKit's own rounded surfaces use.
    static let cornerRadius: CGFloat = 18
    static let overhang: CGFloat = 12

    init(settings: Settings) {
        super.init(contentRect: .zero, styleMask: [.borderless], backing: .buffered, defer: false)

        level = .statusBar
        isOpaque = false
        backgroundColor = .clear
        // A shadow is what makes it read as floating over the app behind rather
        // than painted onto it. It works here only because the sprite's own layer
        // is opaque and masked: a shadow around a fully clear window has nothing
        // to trace.
        hasShadow = true
        collectionBehavior = [.canJoinAllSpaces, .stationary]
        isMovableByWindowBackground = false
        ignoresMouseEvents = false

        place(settings: settings)
    }

    // The pet must never take focus: it sits over whatever you are working in,
    // and stealing key events from that would make it a nuisance rather than an
    // ornament.
    override var canBecomeKey: Bool { false }

    /// place recomputes the frame against the current screen. It is called again
    /// on didChangeScreenParameters: unplugging a display otherwise leaves the pet
    /// on coordinates that no longer exist, which looks like a crash.
    func place(settings: Settings) {
        let visible = NSScreen.main?.visibleFrame ?? CGRect(x: 0, y: 0, width: 1440, height: 900)
        let sprite = Placement.frame(
            corner: settings.corner,
            offset: settings.offset,
            size: settings.size,
            in: visible
        )
        setFrame(sprite.insetBy(dx: -Self.overhang, dy: -Self.overhang), display: true)
    }

    /// spriteBounds is where the creature sits inside this window, in view
    /// coordinates. Everything outside it is margin the badge overhangs into.
    var spriteBounds: NSRect {
        NSRect(origin: .zero, size: frame.size).insetBy(dx: Self.overhang, dy: Self.overhang)
    }

    /// spriteFrame is that same rect in screen coordinates, which is what the card
    /// aligns against — aligning to the window instead would shift the card by the
    /// margin and leave it looking a dozen points out.
    var spriteFrame: NSRect {
        frame.insetBy(dx: Self.overhang, dy: Self.overhang)
    }
}
