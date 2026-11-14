import Testing
@testable import VehicleTracker

/// Android's `RouteLineTest`, so the same feed colour gives the same line on
/// both apps.
@Suite struct RouteLineColorsTests {
    @Test func theLineTakesTheRouteColourOrSystemBlueWithoutOne() {
        #expect(RouteLineColors.line(hex: "0077C0", dark: false).hex == "0077C0")
        #expect(RouteLineColors.line(hex: "#0077C0", dark: false).hex == "0077C0")
        // route_color is optional in GTFS, and the server passes "" through as "".
        #expect(RouteLineColors.line(hex: "", dark: false).hex == "007AFF")
        #expect(RouteLineColors.line(hex: "ZZZZZZ", dark: false).hex == "007AFF")
    }

    /// Greys either side of each cut-off: #A0A0A0 is luminance 0.352 and
    /// #9F9F9F 0.347, #767676 0.181 and #757575 0.178.
    @Test func onTheLightMapABrightRouteHasAnInkCasingAndADarkOneWhite() throws {
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "FDB71A")), dark: false).hex == "1F1F1F")
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "A0A0A0")), dark: false).hex == "1F1F1F")
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "9F9F9F")), dark: false).hex == "FFFFFF")
        #expect(RouteLineColors.casing(line: RouteLineColors.fallback, dark: false).hex == "FFFFFF")
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "9C182F")), dark: false).hex == "FFFFFF")
    }

    @Test func onTheDarkMapABrightRouteHasANearBlackCasingAndADarkOneWhite() throws {
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "FDB71A")), dark: true).hex == "0A0A0A")
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "767676")), dark: true).hex == "0A0A0A")
        #expect(RouteLineColors.casing(line: try #require(RGB(hex: "757575")), dark: true).hex == "FFFFFF")
    }

    @Test func atNightARouteTooDarkForTheMapIsLiftedUntilItReadsInTheSameHue() throws {
        for hex in ["9C182F", "000000", "1F1F1F", "002060"] {
            // Lifted until it reads, and no further: it keeps as much of its
            // own colour as it can.
            let lifted = RouteLineColors.liftedForNight(try #require(RGB(hex: hex)))
            let ratio = lifted.contrast(with: RouteLineColors.nightBackground)
            #expect(ratio >= 3.0 && ratio < 3.3, "\(hex) at \(ratio):1")
        }
        // Maroon stays red: more red than green or blue.
        let maroon = RouteLineColors.liftedForNight(try #require(RGB(hex: "9C182F")))
        #expect(maroon.red > maroon.green && maroon.red > maroon.blue)
        // Black has no hue to keep, and lifts to a grey.
        let black = RouteLineColors.liftedForNight(RGB(0x000000))
        #expect(black.red == black.green && black.green == black.blue)
    }

    @Test func aRouteThatAlreadyReadsAtNightIsLeftAsItIs() throws {
        #expect(RouteLineColors.liftedForNight(try #require(RGB(hex: "FDB71A"))).hex == "FDB71A")
        #expect(RouteLineColors.line(hex: "", dark: true) == RouteLineColors.fallback)
        #expect(RouteLineColors.line(hex: "0077C0", dark: true).hex == "0077C0")
    }

    @Test func onlyTheDarkMapLiftsARoute() {
        #expect(RouteLineColors.line(hex: "9C182F", dark: false).hex == "9C182F")
        #expect(RouteLineColors.line(hex: "9C182F", dark: true).hex != "9C182F")
    }

    @Test func hexReadsSixDigitsAndNothingElse() {
        #expect(RGB(hex: "FDB71A")?.hex == "FDB71A")
        #expect(RGB(hex: " #fdb71a ")?.hex == "FDB71A")
        #expect(RGB(hex: "12345") == nil)
        #expect(RGB(hex: "GGGGGG") == nil)
        #expect(RGB(hex: "") == nil)
        #expect(RGB(0x26272A).hex == "26272A")
    }
}
