import Foundation
import Testing
@testable import VehicleTracker

/// A port of the route cases in Android's `ViewModelsTest`.
@Suite struct RouteListTests {
    func route(_ id: String, _ shortName: String, _ longName: String, color: String = "", textColor: String = "") -> RouteInfo {
        RouteInfo(id: id, shortName: shortName, longName: longName, color: color, textColor: textColor, type: 3)
    }

    /// Route 15 leads route 5 on purpose: "5" must not match "15".
    var catalog: [RouteInfo] {
        [route("R15", "15", "Rainier Beach", color: "0077C0", textColor: "FFFFFF"),
         route("R5", "5", "Downtown Loop"),
         route("R9", "9", "Airport", color: "00AA00", textColor: "000000")]
    }

    func visible(_ query: String) -> [String] {
        RouteList.of(catalog, query: query, recentIDs: []).visible.map(\.id)
    }

    @Test func searchIsAPrefixOnTheNumberAndASubstringOnTheName() {
        // Route 15 contains a 5 but does not start with one: a driver typing "5" means route 5.
        #expect(visible("5") == ["R5"])
        #expect(visible("beach") == ["R15"])
    }

    @Test func searchIsCaseInsensitiveAndIgnoresSurroundingSpace() {
        #expect(visible("  BEACH ") == ["R15"])
    }

    @Test func anEmptyQueryShowsEveryRoute() {
        #expect(visible("") == ["R15", "R5", "R9"])
    }

    @Test func recentRoutesArePinnedOnlyWhileTheQueryIsEmpty() {
        #expect(RouteList.of(catalog, query: "", recentIDs: ["R9"]).recent.map(\.id) == ["R9"])
        // Searching is the driver saying which route they want; the shortcut gets out of the way.
        #expect(RouteList.of(catalog, query: "9", recentIDs: ["R9"]).recent.isEmpty)
    }

    @Test func recentRoutesKeepTheirRecencyOrderAndDropIDsTheCatalogNoLongerHas() {
        // Newest first, and the route the agency has since dropped from the
        // feed is skipped rather than shown as a row that cannot be tapped.
        #expect(RouteList.of(catalog, query: "", recentIDs: ["R9", "RETIRED", "R5"]).recent.map(\.id) == ["R9", "R5"])
    }

    @Test func routesAreAGridOnlyWhenTheFeedNamesNoneOfThem() {
        #expect(RouteList.of([route("7", "7", ""), route("8", "8", " ")], query: "", recentIDs: []).grid)
        // One named route keeps every route a row, so the list never mixes rows and badges.
        #expect(!RouteList.of([route("7", "7", ""), route("8", "8", "Rainier Beach")], query: "", recentIDs: []).grid)
        #expect(!RouteList.of([route("7", "7", "Downtown"), route("8", "8", "Rainier Beach")], query: "", recentIDs: []).grid)
    }

    @Test func aSearchDoesNotChangeRowsIntoAGridOrTheTotal() {
        let mixed = [route("7", "7", ""), route("8", "8", "Rainier Beach")]
        let searched = RouteList.of(mixed, query: "7", recentIDs: [])
        // Only the unnamed route is left, but the layout follows the whole feed.
        #expect(searched.visible.map(\.id) == ["7"])
        #expect(!searched.grid)
        #expect(searched.total == 2)
    }
}
