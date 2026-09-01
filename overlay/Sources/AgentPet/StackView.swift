import PetKit
import SwiftUI

/// StackView draws the pills over the creature: one per session, newest nearest
/// its head, with anything past the cap peeking out behind the far one so depth
/// carries the count rather than a "+2 more" line.
struct StackView: View {
    let pills: [PetNotification]
    let overflow: Int
    let expandedID: String?
    let onExpand: (String) -> Void
    let onJump: (PetNotification) -> Void
    let onDismiss: (PetNotification) -> Void

    var body: some View {
        // Bottom-up: the pill closest to the creature is the one it is complaining
        // about, and the eye travels from the sprite to it.
        VStack(alignment: .leading, spacing: StackLayout.pillGap) {
            if overflow > 0 {
                PeekView(count: overflow)
            }
            ForEach(pills.reversed()) { pill in
                PillView(
                    pill: pill,
                    expanded: expandedID == pill.id,
                    onExpand: { onExpand(pill.id) },
                    onJump: { onJump(pill) },
                    onDismiss: { onDismiss(pill) }
                )
            }
        }
        .frame(width: StackLayout.pillWidth, alignment: .leading)
    }
}

/// PillView is one notification. Its shape and its colours come from the system
/// rather than from literals: glass where the OS has it, a material where it does
/// not, and semantic foreground styles so it reads on any wallpaper in either
/// appearance.
struct PillView: View {
    let pill: PetNotification
    let expanded: Bool
    let onExpand: () -> Void
    let onJump: () -> Void
    let onDismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .center, spacing: 10) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(pill.title)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    Text(pill.subtitle)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                StatusGlyph(kind: pill.kind)
            }

            if expanded {
                VStack(alignment: .leading, spacing: 2) {
                    detail([pill.agent, pill.branch].compactMap { $0 }.joined(separator: " · "))
                    detail(shortenHome(pill.directory ?? "—"))
                    detail(pill.pid.map { "pid \($0) — double-click to jump" } ?? "no process")
                }
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .frame(width: StackLayout.pillWidth, alignment: .leading)
        .glassPill()
        .contentShape(Rectangle())
        // Declared before the single tap so the double click is matched first; the
        // single one is the fallback, and expanding is instant enough that a double
        // click doing both reads as intent rather than lag.
        .onTapGesture(count: 2, perform: onJump)
        .onTapGesture(perform: onExpand)
        .contextMenu {
            Button(action: onDismiss) {
                Label("Dismiss", systemImage: "xmark.circle")
            }
            Button(action: onJump) {
                Label("Jump to session", systemImage: "arrow.right.circle")
            }
        }
        .animation(.spring(response: 0.3, dampingFraction: 0.8), value: expanded)
    }

    private func detail(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 10, design: .monospaced))
            .foregroundStyle(.tertiary)
            .lineLimit(1)
            .truncationMode(.middle)
    }
}

/// StatusGlyph is the filled circle at the trailing edge: the one thing on the
/// pill readable without reading.
struct StatusGlyph: View {
    let kind: PetNotification.Kind

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: 11, weight: .bold))
            .foregroundStyle(.white)
            .frame(width: 22, height: 22)
            .background(Circle().fill(tint))
    }

    private var symbol: String {
        switch kind {
        case .waiting: return "exclamationmark"
        case .ready: return "checkmark"
        }
    }

    private var tint: Color {
        switch kind {
        case .waiting: return .orange
        case .ready: return .green
        }
    }
}

/// PeekView is what a stack past its cap shows: a sliver, dimmed and inset, that
/// says there are more without spending a pill on saying so.
struct PeekView: View {
    let count: Int

    var body: some View {
        HStack {
            Spacer()
            Text("\(count) more")
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(.secondary)
            Spacer()
        }
        .frame(width: StackLayout.pillWidth - 28, height: StackLayout.peekHeight + 8)
        .glassPill()
        .opacity(0.7)
    }
}

private extension View {
    /// glassPill is Liquid Glass where the system has it, and a material where it
    /// does not. Both are asked for the same concentric shape, so the pill's
    /// corners match whichever one draws them.
    @ViewBuilder
    func glassPill() -> some View {
        let shape = RoundedRectangle(cornerRadius: 18, style: .continuous)

        if #available(macOS 26.0, *) {
            self.glassEffect(.regular, in: shape)
        } else {
            self
                .background(.regularMaterial, in: shape)
                .overlay(shape.strokeBorder(.white.opacity(0.12), lineWidth: 0.5))
                .shadow(color: .black.opacity(0.25), radius: 8, y: 3)
        }
    }
}

/// shortenHome mirrors what the picker's rows do, because a 300-point pill cannot
/// afford to spend a third of a line on /Users/somebody.
func shortenHome(_ path: String) -> String {
    let home = NSHomeDirectory()
    guard !home.isEmpty, path.hasPrefix(home) else { return path }
    return "~" + path.dropFirst(home.count)
}
