import SwiftUI
import Testing
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

    @Test(arguments: ThemeTests.styles) func aMissingOrUnreadableColourFallsBackToTheAccent(_ style: UIUserInterfaceStyle) throws {
        let accent = try ThemeTests.named("AccentColor", style).hex
        let onAccent = try ThemeTests.named("OnAccent", style).hex
        for color in ["", "ZZZZZZ", "12345"] {
            #expect(hexes(RouteBadgeColors(color: color, textColor: "000000"), style).background == accent, "\(color)")
        }
        for textColor in ["", "ZZZZZZ"] {
            #expect(hexes(RouteBadgeColors(color: "FDB71A", textColor: textColor), style).text == onAccent, "\(textColor)")
        }
    }

    @Test func cornersAreContinuousAtTwentyEightPercentOfTheHeight() {
        let rect = CGRect(x: 0, y: 0, width: 60, height: 44)
        #expect(RouteBadgeShape().path(in: rect) == RoundedRectangle(cornerRadius: 12.32, style: .continuous).path(in: rect))
    }
}
