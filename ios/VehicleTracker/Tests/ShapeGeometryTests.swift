import Testing
@testable import VehicleTracker

/// straight runs due north for ~1 km from (47.6000, -122.3300).
private let straight = ShapeGeometry(points: [
    GeoPoint(47.6000, -122.3300), GeoPoint(47.6045, -122.3300), GeoPoint(47.6090, -122.3300),
])!

/// loop is a square: north, east, south, back west to the start.
private let loop = ShapeGeometry(points: [
    GeoPoint(47.6000, -122.3300),
    GeoPoint(47.6045, -122.3300), // north 500 m
    GeoPoint(47.6045, -122.3234), // east ~500 m
    GeoPoint(47.6000, -122.3234), // south 500 m
    GeoPoint(47.6000, -122.3300), // west back to start
])!

private func near(_ a: Double, _ b: Double, _ delta: Double) -> Bool { abs(a - b) <= delta }

@Suite struct ShapeGeometryTests {
    @Test func haversineDistance() {
        #expect(near(Geo.distance(GeoPoint(47.6000, -122.3300), GeoPoint(47.6090, -122.3300)), 1001, 5))
        #expect(Geo.distance(GeoPoint(1, 1), GeoPoint(1, 1)) == 0)
    }

    @Test func initialBearing() {
        #expect(near(Geo.initialBearing(GeoPoint(47.6, -122.33), GeoPoint(47.61, -122.33)), 0, 0.5))
        #expect(near(Geo.initialBearing(GeoPoint(47.6, -122.33), GeoPoint(47.6, -122.32)), 90, 1))
        #expect(near(Geo.initialBearing(GeoPoint(47.61, -122.33), GeoPoint(47.6, -122.33)), 180, 0.5))
    }

    @Test func cumulativeDistances() {
        #expect(straight.cumulative.count == 3)
        #expect(straight.cumulative[0] == 0)
        #expect(near(straight.cumulative[1], 500, 3))
        #expect(near(straight.length, 1001, 5))
        #expect(ShapeGeometry(points: [GeoPoint(1, 1)]) == nil)
    }

    @Test func projectOnShape() {
        let p = straight.project(GeoPoint(47.6045, -122.3300), hint: nil)
        #expect(near(p.alongShape, 500, 3))
        #expect(near(p.distanceToShape, 0, 0.5))
        #expect(near(straight.bearing(at: p.alongShape), 0, 1))
    }

    @Test func projectOffShape() {
        // 0.001° longitude at 47.6° ≈ 75 m east of the line, 250 m along.
        let p = straight.project(GeoPoint(47.60225, -122.3290), hint: nil)
        #expect(near(p.alongShape, 250, 5))
        #expect(near(p.distanceToShape, 75, 3))
    }

    @Test func projectBeyondEndsClamps() {
        let before = straight.project(GeoPoint(47.5990, -122.3300), hint: nil)
        #expect(near(before.alongShape, 0, 0.01))
        #expect(near(before.distanceToShape, 111, 3))
        let after = straight.project(GeoPoint(47.6100, -122.3300), hint: nil)
        #expect(near(after.alongShape, straight.length, 0.01))
    }

    @Test func loopUsesHint() {
        // The start/end corner is equidistant from the first and last segments.
        let nearStart = GeoPoint(47.6001, -122.3299)
        #expect(loop.project(nearStart, hint: nil).alongShape < 50, "without a hint the first minimum wins")
        #expect(loop.project(nearStart, hint: loop.length - 30).alongShape > loop.length - 60, "with a late hint the last segment wins")
        let p = loop.project(nearStart, hint: 1000)
        #expect(p.alongShape < 50 || p.alongShape > loop.length - 60, "a far hint still returns a valid local minimum")
    }

    @Test func pointAtAndBearingAt() {
        let p = loop.point(at: 250)
        #expect(near(p.lat, 47.60225, 0.0001))
        #expect(near(p.lon, -122.3300, 0.0001))
        #expect(near(loop.bearing(at: 250), 0, 1))
        #expect(near(loop.bearing(at: loop.cumulative[1] + 100), 90, 2))
        #expect(near(loop.bearing(at: loop.cumulative[2] + 100), 180, 1))
        #expect(loop.point(at: -5) == loop.points[0])
        #expect(loop.point(at: loop.length + 5) == loop.points[loop.points.count - 1])
        for along in [10.0, 400, 900, 1400] {
            let q = loop.project(loop.point(at: along), hint: along)
            #expect(near(q.alongShape, along, 1), "along=\(along)")
            #expect(!q.distanceToShape.isNaN)
        }
    }

    @Test func sharedLoopPointHintPicksLastPass() {
        let shared = GeoPoint(47.6000, -122.3300)
        #expect(loop.project(shared, hint: nil).alongShape < 1)
        let hint = loop.length - 30
        #expect(loop.project(shared, hint: hint).alongShape > loop.length - 60)
        // ~5 m from the first pass and ~15 m from the last: the 30 m band keeps
        // the last pass in play for the hint.
        let nearCorner = GeoPoint(47.6001347, -122.3299334)
        #expect(loop.project(nearCorner, hint: nil).alongShape < 50)
        let offset = loop.project(nearCorner, hint: hint)
        #expect(offset.alongShape > loop.length - 60)
        #expect(near(offset.distanceToShape, 15, 2))
    }

    @Test func outAndBackPrefersForwardPass() {
        let s = ShapeGeometry(points: [
            GeoPoint(47.6000, -122.33000),
            GeoPoint(47.6090, -122.33000), // north 1 km
            GeoPoint(47.6090, -122.32987), // 10 m east
            GeoPoint(47.6000, -122.32987), // back south, parallel
        ])!
        let p = s.project(GeoPoint(47.6081, -122.32987), hint: 1001) // 100 m down the return leg
        #expect(near(p.alongShape, 1111, 15), "the return leg ahead of the match, not the outbound leg behind it")
        #expect(p.distanceToShape < 1)
        let q = s.project(GeoPoint(47.6044, -122.33000), hint: 500) // 11 m behind the match, same leg
        #expect(near(q.alongShape, 489, 15), "a small step back on this leg beats the return leg 1 km ahead")
    }
}
