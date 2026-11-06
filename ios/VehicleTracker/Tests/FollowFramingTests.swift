import Testing
@testable import VehicleTracker

/// Android's `FollowPaddingTest`, in points. Latitude needs no test of its
/// own here: the map measures what a point covers on its own camera, where
/// Android's `followZoom` has to work it out from the latitude.
@Suite struct FollowFramingTests {
    @Test func theVehicleSitsAThirdOfTheWayUpTheMapNothingCovers() {
        // A 2000 pt map under a 300 pt card and above a 400 pt sheet leaves 1300 pt uncovered.
        let framing = FollowFraming(mapHeight: 2000, coveredTop: 300, coveredBottom: 400)
        #expect(framing.uncoveredHeight == 1300)
        #expect(abs(framing.vehicleY - (300 + 1300 * 2 / 3)) < 1e-9)
        #expect(abs(framing.vehicleOffsetFromCentre - (300 + 1300 * 2 / 3 - 1000)) < 1e-9)
        #expect(abs(framing.pointsAhead - 1300 * 2 / 3) < 1e-9)
        #expect(abs(framing.metresPerPoint - 500 / (1300 * 2 / 3)) < 1e-9)
    }

    @Test func withNothingOverItTheVehicleSitsAThirdOfTheWayUpTheWholeMap() {
        let framing = FollowFraming(mapHeight: 2000, coveredTop: 0, coveredBottom: 0)
        #expect(abs(framing.vehicleY - 2000 * 2 / 3) < 1e-9)
    }

    @Test func aMapCoveredFromEdgeToEdgeAsksForNothingNegative() {
        let framing = FollowFraming(mapHeight: 500, coveredTop: 400, coveredBottom: 400)
        #expect(framing.uncoveredHeight == 0)
        #expect(framing.vehicleY == 400)
        #expect(framing.pointsAhead == 0)
        #expect(framing.metresPerPoint == 500, "no division by zero: the widest view")
    }

    /// The two phones, with a 156 pt card 8 pt under the status bar and the
    /// 76 pt peek over the home indicator. A problem line is not an input:
    /// the framing is built from the card alone, so the map does not move
    /// when a line comes or goes.
    @Test(arguments: [
        // iPhone SE: 667 pt, a 20 pt status bar, no home indicator.
        (FollowFraming(mapHeight: 667, coveredTop: 184, coveredBottom: 76), 455.33, 121.83, 1.8428),
        // iPhone 17: 852 pt, a 59 pt status bar, a 34 pt home indicator.
        (FollowFraming(mapHeight: 852, coveredTop: 223, coveredBottom: 110), 569.0, 143.0, 1.4451),
    ])
    func onEachPhone(framing: FollowFraming, vehicleY: Double, offset: Double, metresPerPoint: Double) {
        #expect(abs(framing.vehicleY - vehicleY) < 0.01)
        #expect(abs(framing.vehicleOffsetFromCentre - offset) < 0.01)
        #expect(abs(framing.metresPerPoint - metresPerPoint) < 0.001)
    }
}
