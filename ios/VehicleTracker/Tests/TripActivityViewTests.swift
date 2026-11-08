import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

@Suite struct TripActivityViewTests {
    /// The narrowest Lock Screen, an iPhone SE's 375 pt less its margins,
    /// where a card is tallest.
    static let lockScreenWidth: CGFloat = 343

    /// ActivityKit cuts a Lock Screen card off past 160 pt. The tallest card
    /// has a two-line headsign, a problem and the stale warning, at the
    /// default text size and at the largest the Lock Screen uses.
    @Test(arguments: [DynamicTypeSize.large, .xxLarge, .xxxLarge, .accessibility5])
    func theLongestCardFitsTheLockScreen(size: DynamicTypeSize) {
        let attributes = TripActivityAttributes(serverTripID: 7, routeName: "E Line", routeColor: "FDB71A", routeTextColor: "000000",
                                                headsign: "Downtown Seattle Via Rainier Ave S and Beacon Hill Station",
                                                staleLine: "Reports paused · Open the app to keep reporting.", staleTitle: "Reports paused")
        let state = TripActivityAttributes.ContentState(
            tone: .late, attention: true, symbol: "scope", lead: "Precise Location off", minutes: nil,
            next: "Off schedule · Next 3rd Ave & Seneca St · 10:25 PM", reportsSymbol: nil,
            reports: "Open the app to keep reporting.", spoken: "")
        let card = TripActivityCard(attributes: attributes, state: state, isStale: true).environment(\.dynamicTypeSize, size)
        let height = UIHostingController(rootView: card).sizeThatFits(in: CGSize(width: Self.lockScreenWidth, height: 1000)).height
        #expect(height <= 160, "\(height) pt at \(size)")
    }

    /// The route's name in the black Island: its own colour only where that
    /// reads, white otherwise.
    @Test(arguments: [("000080", "FFFFFF"), ("9C182F", "FFFFFF"), ("FFD700", "FFD700"), ("0077C0", "0077C0"), ("", "FFFFFF"), ("GGGGGG", "FFFFFF")])
    func routeColourOnBlack(route: String, shown: String) {
        #expect(TripActivityColors.routeOnBlack(route).hex == shown)
    }

    /// The Watch and the car show three lines in 78 pt: the narrowest
    /// Dashboard slot must hold all of them, stale or not.
    @Test(arguments: [false, true])
    func theSmallLayoutFitsTheNarrowestDashboardSlot(isStale: Bool) {
        let attributes = TripActivityAttributes(serverTripID: 7, routeName: "E Line", routeColor: "FDB71A", routeTextColor: "000000",
                                                headsign: "Downtown Seattle Via Rainier Ave S",
                                                staleLine: "Reports paused · Open the app to keep reporting.", staleTitle: "Reports paused")
        let state = TripActivityAttributes.ContentState(
            tone: .late, attention: true, symbol: "icloud.slash.fill", lead: "No connection", minutes: nil,
            next: "Next 3rd Ave & Seneca St · 10:25 PM", reportsSymbol: nil, reports: "Riders can't see you", spoken: "")
        let small = TripActivitySmall(attributes: attributes, state: state, isStale: isStale)
        let height = UIHostingController(rootView: small).sizeThatFits(in: CGSize(width: TripActivitySmall.size.width, height: 1000)).height
        #expect(height <= TripActivitySmall.size.height, "\(height) pt")
    }
}
