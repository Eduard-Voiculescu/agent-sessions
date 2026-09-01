import PetKit
import SwiftUI

/// PetSpriteView is the creature itself, filling whatever bounds it is given. It
/// draws no rounded shape: the hosting view's layer rounds and clips it, which is
/// what gets AppKit's own antialiasing and its continuous corner curve rather
/// than a shape composited against a transparent window, whose edge pixels come
/// out chewed.
struct PetSpriteView: View {
    let mood: Mood
    let onTap: () -> Void

    var body: some View {
        background
            .overlay(
                Text(face)
                    .font(.system(size: 22, weight: .medium, design: .monospaced))
                    .foregroundStyle(.white)
            )
            .scaleEffect(bounce ? 1.06 : 1)
            .animation(animation, value: bounce)
            .contentShape(Rectangle())
            .onTapGesture(perform: onTap)
            .help(mood.tooltip(attention: 0, snoozeLabel: nil))
    }

    private var face: String { mood.face }

    private var background: Color { mood.colour }

    private var bounce: Bool { mood == .needsYou || mood == .done }

    private var animation: Animation? {
        bounce ? .easeInOut(duration: 0.45).repeatForever(autoreverses: true) : .default
    }
}

/// PetBadgeView is the count, drawn in its own unclipped view so it can straddle
/// the sprite's corner the way an app icon's badge does. Its white ring is what
/// separates it from whatever colour the mood put underneath.
struct PetBadgeView: View {
    let text: String
    let snoozed: Bool
    let onTap: () -> Void

    var body: some View {
        Text(text)
            .font(.system(size: 12, weight: .bold, design: .rounded))
            .monospacedDigit()
            .foregroundStyle(.white)
            .padding(.horizontal, 6)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(
                Capsule(style: .continuous)
                    .fill(snoozed ? Color.black.opacity(0.7) : Color.red)
                    .overlay(Capsule(style: .continuous).strokeBorder(.white, lineWidth: 1.5))
                    .shadow(color: .black.opacity(0.35), radius: 1.5, y: 0.5)
            )
            .contentShape(Capsule(style: .continuous))
            .onTapGesture(perform: onTap)
    }

    /// width is what the badge needs for this many characters: a circle for one
    /// digit, a capsule once there are more, like the badges this imitates.
    static func width(for text: String, height: CGFloat) -> CGFloat {
        max(height, 10 + CGFloat(text.count) * 8)
    }
}

extension Mood {
    var colour: Color {
        switch self {
        case .needsYou: return Color.orange.opacity(0.92)
        case .working: return Color.blue.opacity(0.75)
        case .idle: return Color.gray.opacity(0.6)
        // Dimmer than snoozed, and a different face: an empty machine and a muted
        // pet must not read the same.
        case .asleep: return Color.black.opacity(0.4)
        case .done: return Color.green.opacity(0.8)
        case .unknown: return Color.purple.opacity(0.45)
        case .snoozed: return Color.gray.opacity(0.5)
        }
    }
}
