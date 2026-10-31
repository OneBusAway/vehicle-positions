import SwiftUI
import UIKit

/// The route's short name on its GTFS colour, as riders see it.
struct RouteBadge: View {
    enum Size {
        /// In a route row: at least 56 × 40 pt (Android's `RouteRow`).
        case row
        /// A cell of the route grid: 56 pt tall and the cell's full width,
        /// in larger type (Android's `BigBadge`).
        case grid
        /// On the trip bar: at least a 44 pt square, in larger type (DESIGN-iOS §2.3).
        case trip
    }

    var shortName: String
    var color: String
    var textColor: String
    var size: Size

    var body: some View {
        let colors = RouteBadgeColors(color: color, textColor: textColor)
        Text(shortName.isEmpty ? "—" : shortName)
            .font(size == .row ? .title3.bold() : .title2.bold())
            .lineLimit(1)
            .minimumScaleFactor(0.6)
            .foregroundStyle(colors.text)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .frame(minWidth: minWidth, maxWidth: size == .grid ? .infinity : nil, minHeight: minHeight)
            .background(colors.background, in: RouteBadgeShape())
    }

    private var minWidth: CGFloat {
        switch size {
        case .row: 56
        case .grid: 0
        case .trip: 44
        }
    }

    private var minHeight: CGFloat {
        switch size {
        case .row: 40
        case .grid: 56
        case .trip: 44
        }
    }
}

/// The feed's `route_color` and `route_text_color` as it gives them, with no
/// contrast override, so a route looks the same on both apps. With no route
/// colour the badge is the accent pair (Android's `primary` and `onPrimary`),
/// even if the feed gives a text colour, because that text was chosen for the
/// route colour and not for the accent. With a route colour but no text
/// colour, the text is black or white for that colour: the accent's label can
/// be 1.6:1 on a feed's dark red.
struct RouteBadgeColors {
    let background: Color
    let text: Color

    init(color: String, textColor: String) {
        guard let route = UIColor(hex: color) else {
            background = .accent
            text = .onAccent
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
