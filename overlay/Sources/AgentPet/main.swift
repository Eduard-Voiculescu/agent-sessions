import AppKit
import PetKit
import SwiftUI

/// MenuHost carries the sprite and answers right-clicks. The menu is built per
/// event rather than assigned once, because half of what it says — how long the
/// snooze has left, whether to offer waking up — is only true at the moment it
/// opens.
final class MenuHost: NSView {
    var buildMenu: (() -> NSMenu)?
    /// The parts of this view that are actually drawn. The window is bigger than
    /// the creature so the badge can overhang its corner, and that margin is
    /// transparent — without this, a dozen points of invisible window would
    /// swallow clicks meant for whatever sits behind it.
    var opaqueRegions: [NSRect] = []

    override func menu(for event: NSEvent) -> NSMenu? {
        buildMenu?()
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        guard opaqueRegions.contains(where: { $0.contains(point) }) else { return nil }
        return super.hitTest(point)
    }
}

/// AppDelegate holds the pieces: the feed that arrives, the engine that turns
/// frames into a face, the two windows that show it, and the snooze that silences
/// all of it.
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var window: PetWindow!
    private var host: MenuHost!
    /// Optional and short-lived on purpose: see renderStack.
    private var stack: StackWindow?
    private var feed: Feed!
    private var engine: MoodEngine!
    private var settings = Settings.fallback

    private var mood: Mood = .unknown
    private var attention: [SessionInfo] = []
    private var notifications: [PetNotification] = []
    private var cycled = 0
    private var expandedID: String?
    private var snooze = Snooze()
    private var dismissals = Dismissals()

    func applicationDidFinishLaunching(_ notification: Notification) {
        // .accessory is LSUIElement without a bundle: no dock icon, no menu bar,
        // and the app still owns a window. It is why this ships as a plain
        // SwiftPM executable rather than an .app.
        NSApp.setActivationPolicy(.accessory)

        settings = Settings.load()
        engine = MoodEngine(raise: settings.raise)
        window = PetWindow(settings: settings)

        host = MenuHost(frame: .zero)
        host.buildMenu = { [weak self] in self?.menu() ?? NSMenu() }
        window.contentView = host
        renderPet()
        window.orderFrontRegardless()

        NotificationCenter.default.addObserver(
            forName: NSApplication.didChangeScreenParametersNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            guard let self else { return }
            self.window.place(settings: self.settings)
            self.renderStack()
        }

        feed = Feed(
            onFrame: { [weak self] frame in
                DispatchQueue.main.async { self?.apply(frame) }
            },
            onDisconnect: { [weak self] in
                DispatchQueue.main.async { self?.apply(nil) }
            }
        )
        feed.start()
    }

    func applicationWillTerminate(_ notification: Notification) {
        feed.stop()
    }

    private func apply(_ frame: Frame?) {
        // The snooze expires on a frame rather than on a timer of its own: the feed
        // already ticks, and a second clock would be a second thing to get wrong.
        if snooze.until != nil, !snooze.isActive(at: Date()) {
            snooze = Snooze()
        }
        let quiet = snooze.isActive(at: Date())
        let next = engine.update(frame, now: ProcessInfo.processInfo.systemUptime, snoozed: quiet)

        // Once per arrival at attention, not once per frame while it stays there:
        // a beep every two seconds is an alarm, not a notification.
        if settings.sound, next == .needsYou, mood != .needsYou {
            NSSound.beep()
        }

        let all = engine.attention.map(PetNotification.waiting) + engine.finished.map(PetNotification.ready)
        // Pruned against everything notifying, not against what survives the
        // filter, or a dismissal would forget itself on the very next frame.
        dismissals.prune(against: all)
        let pills = dismissals.visible(all)
        let changed = next != mood || pills != notifications
        mood = next
        attention = engine.attention
        notifications = pills

        if changed {
            cycled = 0
            // An opened pill whose session has stopped waiting has nothing left to
            // show, and leaving it open would hold the stack at a height nothing
            // fills.
            if let expandedID, !pills.contains(where: { $0.id == expandedID }) {
                self.expandedID = nil
            }
            renderPet()
            renderStack()
        } else if mood == .snoozed {
            // The countdown keeps moving even when nothing else does.
            renderPet()
        }
    }

    /// renderPet builds the sprite and its badge as two sibling views. They cannot
    /// be one: the sprite's layer masks to its own bounds to get AppKit's corner
    /// curve, and a badge inside that mask is a badge with its overhang cut off.
    private func renderPet() {
        host.frame = NSRect(origin: .zero, size: window.frame.size)
        host.subviews.forEach { $0.removeFromSuperview() }

        let sprite = NSHostingView(rootView: PetSpriteView(
            mood: mood,
            onTap: { [weak self] in self?.clicked() },
            onDoubleTap: { [weak self] in self?.doubleClicked() }
        ))
        sprite.frame = window.spriteBounds

        // The rounding lives on the layer rather than in the SwiftUI shape: a
        // shape composited against a transparent window leaves squared-off edge
        // pixels, while a masked layer is antialiased by the compositor and gets
        // the same continuous corner curve every other macOS surface has.
        sprite.wantsLayer = true
        sprite.layer?.cornerRadius = PetWindow.cornerRadius
        sprite.layer?.cornerCurve = .continuous
        sprite.layer?.masksToBounds = true
        sprite.layer?.borderWidth = 0.5
        sprite.layer?.borderColor = NSColor.white.withAlphaComponent(0.18).cgColor
        host.addSubview(sprite)

        var regions = [sprite.frame]
        if let text = badgeText {
            let height: CGFloat = 22
            let width = PetBadgeView.width(for: text, height: height)
            let badge = NSHostingView(rootView: PetBadgeView(
                text: text,
                snoozed: mood == .snoozed,
                onTap: { [weak self] in self?.clicked() },
                onDoubleTap: { [weak self] in self?.doubleClicked() }
            ))

            // Centred on the sprite's top-right corner, so it sits half on and half
            // off the creature the way an app icon's badge does.
            badge.frame = NSRect(
                x: sprite.frame.maxX - width / 2,
                y: sprite.frame.maxY - height / 2,
                width: width,
                height: height
            )
            host.addSubview(badge)
            regions.append(badge.frame)
        }

        host.opaqueRegions = regions
        host.toolTip = mood.tooltip(attention: attention.count, snoozeLabel: snooze.label(at: Date()))
    }

    /// badgeText is the count while awake and the countdown while snoozed. A single
    /// waiting session gets no badge: the card beside it already says "1 wants
    /// you", so the digit would be duplicate ink.
    private var badgeText: String? {
        if mood == .snoozed {
            return snooze.label(at: Date())
        }
        // Counted from the pills rather than from the feed, so the number and the
        // stack under it cannot disagree: dismissing a pill takes it off both. The
        // sprite stays its mood's colour either way — something is still blocked on
        // a human, and the pet's job is to keep saying so.
        let waiting = notifications.filter { $0.kind == .waiting }.count
        return waiting > 1 ? "\(waiting)" : nil
    }

    /// renderStack builds a new window for each run of pills rather than hiding one
    /// and showing it again.
    ///
    /// A window kept around between bursts loses its Space: after a day of being
    /// ordered out and back in, `isVisible` reports true, the frame is right, and
    /// `isOnActiveSpace` is false — so the pills draw somewhere nobody is looking
    /// and no amount of ordering front brings them back. Only a fresh window is
    /// born into the Space the human is actually on.
    private func renderStack() {
        guard !notifications.isEmpty else {
            stack?.orderOut(nil)
            stack = nil
            return
        }

        let stack = self.stack ?? StackWindow()
        self.stack = stack

        let shown = Array(notifications.prefix(StackLayout.maxPills))
        let view = StackView(
            pills: shown,
            overflow: notifications.count - shown.count,
            expandedID: expandedID,
            onExpand: { [weak self] id in self?.toggleExpanded(id) },
            onJump: { [weak self] pill in self?.jump(pid: pill.pid) },
            onDismiss: { [weak self] pill in self?.dismiss(pill) }
        )

        stack.contentView = NSHostingView(rootView: view)
        stack.place(
            pet: window.spriteFrame,
            corner: settings.corner,
            pills: notifications.count,
            expanded: expandedID != nil,
            screen: window.screen
        )
        stack.orderFrontRegardless()
    }

    private func dismiss(_ pill: PetNotification) {
        dismissals.dismiss(pill)
        notifications = dismissals.visible(notifications)
        if expandedID == pill.id {
            expandedID = nil
        }
        renderPet()
        renderStack()
    }

    private func toggleExpanded(_ id: String) {
        expandedID = expandedID == id ? nil : id
        renderStack()
    }

    /// clicked drains the queue rather than always landing on the same pane: with
    /// three sessions waiting, three clicks visit all three.
    private func clicked() {
        guard !attention.isEmpty else { return }

        let target = attention[cycled % attention.count]
        cycled += 1
        jump(pid: target.pid)
    }

    /// doubleClicked goes to the picker rather than to a session, in every mood
    /// and on both the creature and its badge: single click cycles what is
    /// waiting, and this one gesture has to mean the same thing whatever the
    /// agents happen to be doing.
    private func doubleClicked() {
        if !Action.picker() {
            FileHandle.standardError.write(Data("agent-pet: no agent-sessions picker to jump to\n".utf8))
        }
    }

    private func jump(pid: Int?) {
        guard let pid else { return }

        // A refusal is written where it can be read — the first click raises
        // macOS's own consent dialog for this app, and a silent no-op there looks
        // like a broken pet rather than a pending permission.
        if !Action.jump(pid: pid) {
            FileHandle.standardError.write(Data("agent-pet: could not jump to pid \(pid)\n".utf8))
        }
    }

    private func menu() -> NSMenu {
        let menu = NSMenu()

        if let remaining = snooze.label(at: Date()) {
            menu.addItem(withTitle: "Snoozed for \(remaining)", action: nil, keyEquivalent: "").isEnabled = false
            menu.addItem(withTitle: "Wake up", action: #selector(wakeUp), keyEquivalent: "")
        } else {
            menu.addItem(withTitle: "Snooze 15 minutes", action: #selector(snoozeFifteen), keyEquivalent: "")
            menu.addItem(withTitle: "Snooze 1 hour", action: #selector(snoozeHour), keyEquivalent: "")
            menu.addItem(withTitle: "Snooze until 9am", action: #selector(snoozeUntilMorning), keyEquivalent: "")
            menu.addItem(withTitle: "Snooze until I say so", action: #selector(snoozeIndefinitely), keyEquivalent: "")
        }

        menu.addItem(.separator())

        // Only the moves that exist from where it stands: two items rather than
        // four with two of them greyed out, and no diagonal, which would be two
        // moves wearing one label.
        for move in settings.corner.moves {
            let item = menu.addItem(withTitle: move.direction.label, action: #selector(movePet(_:)), keyEquivalent: "")
            item.image = NSImage(systemSymbolName: move.direction.symbol, accessibilityDescription: move.direction.label)
            item.representedObject = move.corner.rawValue
        }

        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit", action: #selector(quit), keyEquivalent: "q")

        for item in menu.items where item.action != nil {
            item.target = self
        }
        return menu
    }

    /// movePet reads its destination off the item rather than needing a selector
    /// per corner. The pills follow: they place themselves from the same settings,
    /// so a move flips which side of the creature they sit on and which way they
    /// stack.
    @objc private func movePet(_ sender: NSMenuItem) {
        guard let raw = sender.representedObject as? String, let corner = Corner(rawValue: raw) else {
            return
        }

        settings = settings.moved(to: corner)
        window.place(settings: settings)
        renderPet()
        renderStack()
    }

    @objc private func snoozeFifteen() { begin(.minutes(15)) }
    @objc private func snoozeHour() { begin(.minutes(60)) }
    @objc private func snoozeUntilMorning() { begin(.untilMorning(hour: 9)) }
    @objc private func snoozeIndefinitely() { begin(.indefinite) }

    @objc private func wakeUp() {
        snooze = Snooze()
        apply(nil)
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }

    /// begin silences the pet now rather than on the next frame: the menu closing
    /// while the creature still bounces would read as the click having missed.
    private func begin(_ duration: Snooze.Duration) {
        snooze = Snooze.starting(Date(), duration: duration)
        mood = engine.update(nil, now: ProcessInfo.processInfo.systemUptime, snoozed: true)
        attention = engine.attention
        expandedID = nil
        notifications = []
        renderPet()
        renderStack()
    }
}

let delegate = AppDelegate()
let app = NSApplication.shared
app.delegate = delegate
app.run()
