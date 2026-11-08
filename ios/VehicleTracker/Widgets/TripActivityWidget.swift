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
            TripActivityPresentation(context: context)
        } dynamicIsland: { context in
            let state = context.state
            let isStale = context.isStale
            let dimmed = isStale ? TripActivityStaleRow.contentOpacity : 1
            return DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    HStack(spacing: 6) {
                        Image(systemName: isStale ? TripActivityStaleRow.symbol : "bus.fill")
                            .foregroundStyle(isStale ? .amber : state.islandColor)
                        Text(context.attributes.routeName)
                            .font(.system(.title3, design: .rounded, weight: .heavy))
                            .foregroundStyle(.white)
                            .lineLimit(1)
                            .minimumScaleFactor(0.6)
                            .opacity(dimmed)
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
                        .opacity(dimmed)
                        .padding(.trailing, 6)
                        .accessibilityHidden(true)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    IslandExpandedBottom(attributes: context.attributes, state: state, isStale: isStale)
                }
            } compactLeading: {
                IslandCompactLeading(attributes: context.attributes, state: state, isStale: isStale)
            } compactTrailing: {
                IslandCompactTrailing(state: state)
                    .opacity(dimmed)
            } minimal: {
                IslandMinimal(attributes: context.attributes, state: state, isStale: isStale)
            }
            .keylineTint(isStale ? .amber : state.islandColor)
        }
        // The Apple Watch Smart Stack and the CarPlay Dashboard ask for
        // `.small`; without it they fall back to the compact Island's pair.
        // Since iOS 26 every Live Activity shows on the Dashboard, with no
        // way to keep it off, so `.small` is made to be read in a car.
        .supplementalActivityFamilies([.small])
    }
}

/// The Lock Screen card on iPhone, or the small layout for the Watch and
/// the car.
private struct TripActivityPresentation: View {
    let context: ActivityViewContext<TripActivityAttributes>
    @Environment(\.activityFamily) private var family

    var body: some View {
        if family == .small {
            // The phone resolves colours in its own appearance, so a phone in
            // light mode tinted the Watch's card white while the Watch drew
            // its text for a dark face (OneBusAway iOS #1591).
            TripActivitySmall(attributes: context.attributes, state: context.state, isStale: context.isStale)
                .environment(\.colorScheme, .dark)
                .activityBackgroundTint(.black)
        } else {
            TripActivityCard(attributes: context.attributes, state: context.state, isStale: context.isStale)
                .activityBackgroundTint(Color(uiColor: .systemBackground))
                .activitySystemActionForegroundColor(.primary)
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
/// with a problem's second line while there is one. Stale, they dim and the
/// warning goes under them at full opacity.
private struct IslandExpandedBottom: View {
    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Rectangle()
                .fill(.white.opacity(0.15))
                .frame(height: 1)
                .padding(.vertical, 4)
            VStack(alignment: .leading, spacing: 4) {
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
            .opacity(isStale ? TripActivityStaleRow.contentOpacity : 1)
            if isStale {
                TripActivityStaleRow(text: attributes.staleLine, color: .amber)
            }
        }
        .padding(.horizontal, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(isStale ? TripActivityStaleRow.spoken(attributes, state) : state.spoken)
        .accessibilityAddTraits(.updatesFrequently)
    }
}

/// The route's name, in its colour where that reads on black. On iOS 27,
/// where the Island can be limited in width (in landscape), only the status
/// symbol: Apple asks for what fits a 28 pt circle. Stale, the warning glyph
/// in place of either.
private struct IslandCompactLeading: View {
    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        Group {
            if isStale {
                StaleGlyph()
            } else if #available(iOS 27, *) {
                LimitedWidth(full: route, limited: Image(systemName: state.symbol).foregroundStyle(state.islandColor))
            } else {
                route
            }
        }
        .accessibilityLabel(isStale ? TripActivityStaleRow.spoken(attributes, state) : state.spoken)
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

/// The status symbol alone; stale, the warning glyph.
private struct IslandMinimal: View {
    let attributes: TripActivityAttributes
    let state: TripActivityAttributes.ContentState
    let isStale: Bool

    var body: some View {
        Group {
            if isStale {
                StaleGlyph()
            } else {
                Image(systemName: state.symbol)
                    .foregroundStyle(state.islandColor)
            }
        }
        .accessibilityLabel(isStale ? TripActivityStaleRow.spoken(attributes, state) : state.spoken)
    }
}

private struct StaleGlyph: View {
    var body: some View {
        Image(systemName: TripActivityStaleRow.symbol)
            .font(.system(.body, design: .rounded, weight: .bold))
            .foregroundStyle(.amber)
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
