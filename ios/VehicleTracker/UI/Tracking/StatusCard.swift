import SwiftUI

/// The schedule answer at the top of the map: a symbol in a disc, the answer,
/// and the next stop with its time and distance (DESIGN-iOS §4.7.1).
struct StatusCard: View {
    let status: TripStatus

    @ScaledMetric(relativeTo: .largeTitle) private var answerSize = 40.0

    var body: some View {
        let card = status.card
        HStack(alignment: .top, spacing: 14) {
            ZStack {
                Circle().fill(.white.opacity(0.18))
                Image(systemName: card.symbol)
                    .font(.system(size: 24, weight: .semibold))
                    .symbolRenderingMode(.hierarchical)
                    .contentTransition(.symbolEffect(.replace))
            }
            .frame(width: 48, height: 48)
            VStack(alignment: .leading, spacing: 4) {
                Text(card.answer)
                    .font(.system(size: answerSize, weight: .semibold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.6)
                    .contentTransition(.numericText())
                    .animation(.smooth(duration: 0.25), value: card.answer)
                (Text(card.label) + Text(verbatim: " ") + Text(card.emphasis).fontWeight(.semibold))
                    .font(.title3)
                    .lineLimit(2)
                Text(card.line2)
                    .font(.body)
                    .monospacedDigit()
                    .lineLimit(2)
            }
        }
        .foregroundStyle(.onStatusFill)
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(card.tone.fill, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
        // The fill and the symbol cross-fade when the kind of answer changes,
        // never on the fixes in between (DESIGN-iOS §2.6).
        .animation(.smooth(duration: 0.25), value: status.category)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(status.sentence)
    }
}
