import ActivityKit
import SwiftUI
import WidgetKit

/// The running trip's Live Activity. Everything it draws comes in the
/// attributes and the content state, so it reads nothing from the app. It
/// has no buttons and no URL: tapping opens the app, which is on Tracking or
/// Resume whenever an activity exists, and the app's URL scheme is the
/// server setup link.
struct TripActivityWidget: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: TripActivityAttributes.self) { context in
            TripActivityCard(attributes: context.attributes, state: context.state)
                .activityBackgroundTint(Color(uiColor: .systemBackground))
                .activitySystemActionForegroundColor(.primary)
        } dynamicIsland: { context in
            let state = context.state
            return DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    HStack(spacing: 6) {
                        Image(systemName: "bus.fill")
                            .foregroundStyle(state.islandColor)
                        Text(context.attributes.routeName)
                            .font(.system(.title3, design: .rounded, weight: .heavy))
                            .foregroundStyle(.white)
                            .lineLimit(1)
                            .minimumScaleFactor(0.6)
                    }
                    .padding(.leading, 6)
                    .accessibilityHidden(true)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    Text(state.lead)
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                        .foregroundStyle(state.islandColor)
                        .lineLimit(1)
                        .minimumScaleFactor(0.5)
                        .padding(.trailing, 6)
                        .accessibilityHidden(true)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    IslandExpandedBottom(attributes: context.attributes, state: state)
                }
            } compactLeading: {
                IslandCompactLeading(attributes: context.attributes, state: state)
            } compactTrailing: {
                IslandCompactTrailing(state: state)
            } minimal: {
                Image(systemName: state.symbol)
                    .foregroundStyle(state.islandColor)
                    .accessibilityLabel(state.spoken)
            }
            .keylineTint(state.islandColor)
        }
    }
}

extension TripActivityAttributes.ContentState {
    /// The status in the always-dark Island: amber for a problem or Signed
    /// out, otherwise the status text colour.
    var islandColor: Color {
        attention ? .amber : tone.text
    }
}

/// Under a divider: the headsign, then a status dot with the next stop, or
/// with a problem's second line while there is one.
private struct IslandExpandedBottom: View {
    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Rectangle()
                .fill(.white.opacity(0.15))
                .frame(height: 1)
                .padding(.vertical, 4)
            Text(attributes.headsign)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            HStack(spacing: 6) {
                Circle()
                    .fill(state.islandColor)
                    .frame(width: 6, height: 6)
                Text(state.attention ? state.reports : state.next ?? state.reports)
                    .font(.caption.weight(.medium))
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(state.spoken)
        .accessibilityAddTraits(.updatesFrequently)
    }
}

/// The route's name, in its colour where that reads on black. On iOS 27,
/// where the Island can be limited in width (in landscape), only the status
/// symbol: Apple asks for what fits a 28 pt circle.
private struct IslandCompactLeading: View {
    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState

    var body: some View {
        Group {
            if #available(iOS 27, *) {
                LimitedWidth(full: route, limited: Image(systemName: state.symbol).foregroundStyle(state.islandColor))
            } else {
                route
            }
        }
        .accessibilityLabel(state.spoken)
        .accessibilityAddTraits(.updatesFrequently)
    }

    private var route: some View {
        Text(attributes.routeName)
            .font(.system(.body, design: .rounded, weight: .bold))
            .foregroundStyle(Color(uiColor: TripActivityColors.routeOnBlack(attributes.routeColor).uiColor))
            .lineLimit(1)
            .minimumScaleFactor(0.6)
    }
}

/// The status symbol, and the minutes while late or early ("6 min"). Empty
/// where the Island is limited in width. The leading side speaks for both.
private struct IslandCompactTrailing: View {
    let state: TripActivityAttributes.ContentState

    var body: some View {
        Group {
            if #available(iOS 27, *) {
                LimitedWidth(full: status, limited: EmptyView())
            } else {
                status
            }
        }
        .accessibilityHidden(true)
    }

    private var status: some View {
        HStack(spacing: 4) {
            Image(systemName: state.symbol)
            if let minutes = state.minutes {
                Text(minutes)
            }
        }
        .font(.system(.body, design: .rounded, weight: .bold))
        .foregroundStyle(state.islandColor)
    }
}

/// `full`, or `limited` while the Dynamic Island is limited in width.
@available(iOS 27, *)
private struct LimitedWidth<Full: View, Limited: View>: View {
    let full: Full
    let limited: Limited
    @Environment(\.isDynamicIslandLimitedInWidth) private var isLimited

    var body: some View {
        if isLimited {
            limited
        } else {
            full
        }
    }
}
