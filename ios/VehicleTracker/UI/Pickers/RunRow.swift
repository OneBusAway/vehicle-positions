import SwiftUI

/// One departure on the board (DESIGN-iOS §4.4, Android's `RunRow`): the
/// start time big, where it goes under it, and the Now or Next tag. While a
/// start is in flight the run being started shows a spinner and the rest
/// dim and ignore taps. Equatable over what it shows, so a minute tick
/// redraws only the rows whose tag, dimming or place changed.
struct RunRow: View, Equatable {
    let run: BoardRun
    let timezone: String
    let starting: Bool
    let dimmed: Bool
    let onSelect: () -> Void

    static func == (lhs: RunRow, rhs: RunRow) -> Bool {
        lhs.run == rhs.run && lhs.timezone == rhs.timezone && lhs.starting == rhs.starting && lhs.dimmed == rhs.dimmed
    }

    var body: some View {
        Button(action: onSelect) {
            HStack(spacing: 14) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(Formatters.clock(run.startsAt, timezone: timezone))
                        .font(.title2.weight(.medium).monospacedDigit())
                        .foregroundStyle(run.past ? Color(.secondaryLabel) : Color(.label))
                    Text(run.headsign)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                if starting {
                    ProgressView()
                } else if let highlight = run.highlight {
                    HighlightTag(highlight: highlight)
                }
            }
            .padding(.leading, 24)
            .padding(.trailing, 20)
            .frame(maxWidth: .infinity, minHeight: 72, alignment: .leading)
            .background(starting ? Color(.secondarySystemFill) : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(RunRowStyle())
        .disabled(starting || dimmed)
        .opacity(dimmed ? 0.4 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityValue(starting ? PickerText.starting : "")
    }
}

/// Presses dim the row; being disabled does not, so the run being started
/// keeps its time at full strength while the rest dim to 40%, as Android's.
private struct RunRowStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.opacity(configuration.isPressed ? 0.6 : 1)
    }
}

/// Now on the run under way, Next on the one to start (Android's `HighlightTag`).
private struct HighlightTag: View {
    let highlight: RunHighlight

    var body: some View {
        Text(highlight == .now ? PickerText.now : PickerText.next)
            .font(.caption.bold())
            .foregroundStyle(.onAccentContainer)
            .padding(.horizontal, 10)
            .frame(minHeight: 28)
            .background(.accentContainer, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

/// A dot and a line between the runs that have gone and the rest, which
/// VoiceOver reads as "Now" (Android's `NowLine`).
struct NowLine: View {
    var body: some View {
        HStack(spacing: 0) {
            Circle()
                .fill(.accent)
                .frame(width: 12, height: 12)
            Rectangle()
                .fill(.accent)
                .frame(height: 2)
        }
        .frame(height: 12)
        .padding(.leading, 18)
        .padding(.trailing, 24)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(PickerText.now)
    }
}
