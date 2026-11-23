import SwiftUI

/// The Live Activity on the Lock Screen (DESIGN-iOS §4.10): the route badge,
/// the headsign and the lead, then the next stop, then the reports line. The
/// widget sets it on `systemBackground`, so the status text colours read on
/// it as they do in the app. ActivityKit cuts a card off past 160 pt, so the
/// headsign and the lead take two lines at most, the text stops growing at
/// `.xxLarge`, and once stale the warning takes the reports line's place: it
/// is what that line can no longer vouch for.
struct TripActivityCard: View {
    /// The largest text size at which the tallest card stays within 160 pt.
    static let largestTextSize = DynamicTypeSize.xxLarge
    static let leadWidth: CGFloat = 136

    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            content
                .opacity(isStale ? TripActivityStaleRow.contentOpacity : 1)
            if isStale {
                TripActivityStaleRow(text: attributes.staleLine, color: .amberOnSurface)
            } else {
                TripActivityReportsLine(state: state)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .dynamicTypeSize(...Self.largestTextSize)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(isStale ? TripActivityStaleRow.spoken(attributes, state) : state.spoken)
        .accessibilityAddTraits(.updatesFrequently)
    }

    private var content: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .center, spacing: 12) {
                RouteBadge(shortName: attributes.routeName, color: attributes.routeColor,
                           textColor: attributes.routeTextColor, size: .activity)
                Text(attributes.headsign)
                    .font(.headline.weight(.heavy))
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                lead
            }
            if let next = state.next {
                Text(next)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }

    /// The answer in its status colour; a problem or Signed out in amber, with
    /// its symbol. As wide as its words, up to `leadWidth`, so the headsign
    /// keeps room for a word like "Downtown" even on an iPhone SE. At the
    /// largest sizes a long answer shrinks a little rather than being cut.
    private var lead: some View {
        HStack(spacing: 4) {
            if state.attention {
                Image(systemName: state.symbol)
            }
            Text(state.lead)
                .minimumScaleFactor(0.7)
        }
        .font(.system(.title3, design: .rounded, weight: .bold))
        .foregroundStyle(state.attention ? Color.amberOnSurface : state.tone.text)
        .lineLimit(2)
        .multilineTextAlignment(.trailing)
        .frame(maxWidth: Self.leadWidth, alignment: .trailing)
        .fixedSize()
    }
}

/// "Reporting" with its symbol, "Ending…", or a problem's second line.
struct TripActivityReportsLine: View {
    let state: TripActivityAttributes.ContentState

    var body: some View {
        Group {
            if let symbol = state.reportsSymbol {
                Label(state.reports, systemImage: symbol)
            } else {
                Text(state.reports)
            }
        }
        .lineLimit(1)
    }
}
