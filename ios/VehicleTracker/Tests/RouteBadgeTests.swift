import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

@Suite struct RouteBadgeTests {
    func hexes(_ colors: RouteBadgeColors, _ style: UIUserInterfaceStyle) -> (background: String, text: String) {
        (ThemeTests.hex(colors.background, style), ThemeTests.hex(colors.text, style))
    }

    @Test(arguments: ThemeTests.styles) func theFeedsColoursAreUsedAsGiven(_ style: UIUserInterfaceStyle) {
        // No contrast override: black on KCM's gold stays as the feed wrote it.
        #expect(hexes(RouteBadgeColors(color: "FDB71A", textColor: "000000"), style) == ("FDB71A", "000000"))
        // GTFS omits the '#', but a feed that includes one still means the same colour.
        #expect(hexes(RouteBadgeColors(color: "#0055B3", textColor: "#FFFFFF"), style) == ("0055B3", "FFFFFF"))
    }

    @Test(arguments: ThemeTests.styles) func noRouteColourIsTheAccentPair(_ style: UIUserInterfaceStyle) throws {
        let accent = try ThemeTests.named("AccentColor", style).hex
        let onAccent = try ThemeTests.named("OnAccent", style).hex
        for color in ["", "ZZZZZZ", "12345"] {
            #expect(hexes(RouteBadgeColors(color: color, textColor: ""), style) == (accent, onAccent), "\(color)")
        }
    }

    /// The accent's label is 1.8:1 on this blue in dark mode, and white is
    /// 1.8:1 on KCM's gold, so text the feed leaves out suits the route colour.
    @Test(arguments: ThemeTests.styles) func noTextColourIsBlackOrWhiteForTheRouteColour(_ style: UIUserInterfaceStyle) {
        #expect(hexes(RouteBadgeColors(color: "0055B3", textColor: ""), style) == ("0055B3", "FFFFFF"))
        #expect(hexes(RouteBadgeColors(color: "FDB71A", textColor: "ZZZZZZ"), style) == ("FDB71A", "000000"))
    }

    @Test func fallbackTextReadsOnAnyRouteColour() {
        // Either side of the grey where black and white tie, and a spread of route colours.
        for hex in ["000000", "FFFFFF", "747474", "777777", "0055B3", "FDB71A", "9C182F", "00A0E0", "8E44AD"] {
            let route = UIColor(hex: hex)!
            let text = UIColor(RouteBadgeColors.legibleText(on: route))
            #expect(ThemeTests.contrast(text, route) >= 4.5, "\(hex)")
        }
    }

    @Test func cornersAreContinuousAtTwentyEightPercentOfTheHeight() {
        let rect = CGRect(x: 0, y: 0, width: 60, height: 44)
        #expect(RouteBadgeShape().path(in: rect) == RoundedRectangle(cornerRadius: 12.32, style: .continuous).path(in: rect))
    }
}
