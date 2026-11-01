import SwiftUI

/// The trip that was reporting when the session ended, which signing in
/// again picks up (DESIGN-iOS §4.9, Android's `TripStillOpen`). One element
/// for VoiceOver: the message names the route, so the badge isn't read too.
struct TripStillOpenCard: View {
    let trip: TripGeometry

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            RouteBadge(shortName: trip.badgeName, color: trip.route.color, textColor: trip.route.textColor, size: .trip)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(SignInText.tripStillOpen)
                    .font(.headline)
                Text(SignInText.tripStillOpenMessage(route: trip.badgeName))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(16)
        .background(.attentionContainer, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .accessibilityElement(children: .combine)
    }
}
