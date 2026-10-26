import SwiftUI
import UIKit

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
/// contrast override, so a route looks the same on both apps. With no route
/// colour the badge is the accent pair, as Android's `RouteBadge` falls back
/// to `primary` and `onPrimary`. With a route colour but no text colour, the
/// text is black or white for that colour: the accent's label can be 1.6:1
/// on a feed's dark red.
struct RouteBadgeColors {
    let background: Color
    let text: Color

    init(color: String, textColor: String) {
        guard let route = UIColor(hex: color) else {
            background = .accent
            text = Color(hex: textColor) ?? .onAccent
            return
        }
        background = Color(uiColor: route)
        text = Color(hex: textColor) ?? Self.legibleText(on: route)
    }

    /// Black or white, whichever has more contrast with `background`. One of
    /// them always reaches 4.5:1.
    static func legibleText(on background: UIColor) -> Color {
        var red: CGFloat = 0, green: CGFloat = 0, blue: CGFloat = 0, alpha: CGFloat = 0
        background.getRed(&red, green: &green, blue: &blue, alpha: &alpha)
        func linear(_ value: CGFloat) -> CGFloat {
            value <= 0.04045 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        let luminance = 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue)
        let onBlack = (luminance + 0.05) / 0.05, onWhite = 1.05 / (luminance + 0.05)
        return onBlack >= onWhite ? .black : .white
    }
}

/// Continuous corners at 28% of the height, as OneBusAway iOS's `RouteBadgeView`.
nonisolated struct RouteBadgeShape: Shape {
    func path(in rect: CGRect) -> Path {
        RoundedRectangle(cornerRadius: rect.height * 0.28, style: .continuous).path(in: rect)
    }
}
