import SwiftUI

/// The route's short name on its GTFS colour, as riders see it.
struct RouteBadge: View {
    var shortName: String
    var color: String
    var textColor: String

    var body: some View {
        let colors = RouteBadgeColors(color: color, textColor: textColor)
        Text(shortName.isEmpty ? "—" : shortName)
            .font(.title3.bold())
            .foregroundStyle(colors.text)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(colors.background, in: RouteBadgeShape())
    }
}

/// The feed's `route_color` and `route_text_color` as it gives them, with no
/// contrast override, so a route looks the same on both apps. A colour that
/// is missing or unreadable falls back to the accent pair, as Android's
/// `RouteBadge` falls back to `primary` and `onPrimary`.
struct RouteBadgeColors {
    let background: Color
    let text: Color

    init(color: String, textColor: String) {
        background = Color(hex: color) ?? .accent
        text = Color(hex: textColor) ?? .onAccent
    }
}

/// Continuous corners at 28% of the height, as OneBusAway iOS's `RouteBadgeView`.
nonisolated struct RouteBadgeShape: Shape {
    func path(in rect: CGRect) -> Path {
        RoundedRectangle(cornerRadius: rect.height * 0.28, style: .continuous).path(in: rect)
    }
}
