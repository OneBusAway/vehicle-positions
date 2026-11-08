import Foundation

/// Colours for the Dynamic Island, which is always black.
enum TripActivityColors {
    /// Text in the route colour must stand 3:1 from the black Island, as
    /// large bold text needs.
    static let routeContrastOnBlack = 3.0

    /// The route's name in the compact Island: in the feed's colour when that
    /// reads on black, otherwise white. A navy or maroon route, or one with no
    /// colour, would be lost there.
    static func routeOnBlack(_ hex: String) -> RGB {
        guard let route = RGB(hex: hex), route.contrast(with: RGB(0x000000)) >= routeContrastOnBlack else {
            return RGB(0xFFFFFF)
        }
        return route
    }
}
