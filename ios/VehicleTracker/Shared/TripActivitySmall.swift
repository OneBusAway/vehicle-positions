import SwiftUI

/// The trip in the Apple Watch Smart Stack and on the CarPlay Dashboard,
/// 170 to 240 × 78 pt in the car: the route and the lead, the headsign, then
/// the reports word or a problem's second line. Read at a glance while
/// driving, so no buttons and three short lines. The widget draws it dark.
struct TripActivitySmall: View {
    /// The narrowest Dashboard slot.
    static let size = CGSize(width: 170, height: 78)

    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(attributes.routeName)
                        .font(.system(.headline, design: .rounded, weight: .heavy))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Text(state.lead)
                        .font(.system(.subheadline, design: .rounded, weight: .bold))
                        .foregroundStyle(state.attention ? .amber : state.tone.text)
                        .lineLimit(1)
                        .minimumScaleFactor(0.7)
                }
                Text(attributes.headsign)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .opacity(isStale ? TripActivityStaleRow.contentOpacity : 1)
            if isStale {
                Label(attributes.staleTitle, systemImage: TripActivityStaleRow.symbol)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.amber)
                    .lineLimit(1)
            } else {
                TripActivityReportsLine(state: state)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(state.attention ? .amber : .secondary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(isStale ? TripActivityStaleRow.spoken(attributes, state) : state.spoken)
        .accessibilityAddTraits(.updatesFrequently)
    }
}
