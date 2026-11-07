import SwiftUI

/// The trip the app closed during (DESIGN-iOS §4.8): the route's badge, the
/// headsign and the run, then the vehicle and when the trip started. One
/// element for VoiceOver, read as one sentence.
struct ResumeTripCard: View {
    let card: ResumeCard

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 16) {
                RouteBadge(shortName: card.badgeName, color: card.routeColor, textColor: card.routeTextColor, size: .card)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text(card.title)
                        .font(.headline)
                        .lineLimit(2)
                        .accessibilityLabel(card.spokenRoute)
                    if let runTime = card.runTime {
                        Text(ResumeText.run(runTime))
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            Divider()
            Fact(symbol: "bus.fill") {
                Text(ResumeText.vehicle(card.vehicleID))
            }
            Fact(symbol: "clock") {
                StartedText(card: card)
            }
        }
        .padding(20)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .accessibilityElement(children: .combine)
    }
}

/// "Started 19 min ago, at 6:58 AM", in a view of its own: it changes once a
/// minute, counted from the trip's start, and nothing else on the screen
/// redraws with it.
private struct StartedText: View {
    let card: ResumeCard

    var body: some View {
        TimelineView(.periodic(from: card.startedAt, by: 60)) { context in
            Text(card.started(now: context.date))
                .monospacedDigit()
                .accessibilityLabel(card.spokenStarted(now: context.date))
        }
    }
}

/// One fact about the trip, with its symbol in front.
private struct Fact<Content: View>: View {
    let symbol: String
    @ViewBuilder let content: Content

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: symbol)
                .font(.system(size: 22))
                .foregroundStyle(.secondary)
                .frame(width: 28)
                .accessibilityHidden(true)
            content
        }
    }
}
