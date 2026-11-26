import MapKit
import Testing
import UIKit
import VehiclePositionsKit
@testable import VehicleTracker

/// What is up on the real map while it follows a vehicle: the way it goes, or
/// north. The car has no framing and always has the way the vehicle goes up.
@MainActor @Suite struct RouteMapUpTests {
    static let size = CGSize(width: 402, height: 874)
    let trip = TripFixtures.t1
    let evaluator = AdherenceEvaluator(trip: TripFixtures.t1)!

    func framing(_ up: MapUp) -> FollowFraming {
        FollowFraming(mapHeight: Self.size.height, coveredTop: 223, coveredBottom: 110, up: up)
    }

    func map(_ framing: FollowFraming?) -> RouteMapViewController {
        let map = RouteMapViewController(allowsDirectInteraction: true)
        map.view.frame = CGRect(origin: .zero, size: Self.size)
        map.view.layoutIfNeeded()
        map.setTrip(trip)
        map.followFraming = framing
        return map
    }

    /// Half way along the route, going east.
    func adherence(course: Double = 90, lat: Double = 47.6045) -> Adherence {
        evaluator.evaluate(TripFixtures.fix(lat: lat, lon: -122.33, at: TripFixtures.at(8, 5), course: course), previous: nil)
    }

    func isNear(_ a: Double, _ b: Double, _ tolerance: Double) -> Bool { abs(a - b) <= tolerance }

    @Test func theWayTheVehicleGoesIsUp() {
        let map = map(framing(.heading))
        #expect(isNear(map.followCamera(for: adherenceSeen(by: map)).heading, 90, 0.5))
    }

    @Test func northIsUpOnceItIsChosen() {
        let map = map(framing(.north))
        #expect(map.followCamera(for: adherenceSeen(by: map)).heading == 0)
    }

    @Test func theCarHasTheWayTheVehicleGoesUp() {
        let map = map(nil)
        #expect(isNear(map.followCamera(for: adherenceSeen(by: map)).heading, 90, 0.5))
    }

    /// The camera centres north of the vehicle or south of it, by how far the
    /// middle of the uncovered map is from the middle of the map: not east,
    /// the way it goes.
    @Test func northUpTheMapCentresStraightUpFromTheVehicle() {
        let map = map(framing(.north))
        let adherence = adherenceSeen(by: map)
        let camera = map.followCamera(for: adherence)
        #expect(isNear(camera.centerCoordinate.longitude, adherence.fix.longitude, 1e-9))
        // The card over it is taller than the sheet under it, so the middle
        // of the rest is below the middle of the map: the map looks north.
        #expect(framing(.north).vehicleOffsetFromCentre > 0)
        #expect(camera.centerCoordinate.latitude > adherence.fix.latitude)
    }

    @Test func headingUpTheMapCentresAheadOfTheVehicle() {
        let map = map(framing(.heading))
        let adherence = adherenceSeen(by: map)
        let camera = map.followCamera(for: adherence)
        #expect(isNear(camera.centerCoordinate.latitude, adherence.fix.latitude, 1e-6))
        #expect(camera.centerCoordinate.longitude > adherence.fix.longitude)
    }

    // MARK: The map on screen

    /// In a window, so the map draws the vehicle and its camera moves. MapKit
    /// does both when it gets to them, which can take seconds on a busy
    /// machine.
    func shown(_ framing: FollowFraming) -> (map: RouteMapViewController, window: UIWindow) {
        let map = map(framing)
        let window = UIWindow(frame: CGRect(origin: .zero, size: Self.size))
        window.rootViewController = map
        window.isHidden = false
        window.layoutIfNeeded()
        return (map, window)
    }

    /// The way the vehicle's mark points on the screen, in degrees from up.
    func pointing(_ map: RouteMapViewController) -> Double? {
        guard let vehicle = map.mapView.annotations.first(where: { $0 is VehicleAnnotation }),
              let transform = map.mapView.view(for: vehicle)?.transform else { return nil }
        return atan2(transform.b, transform.a) * 180 / .pi
    }

    @Test func northUpTheMapStaysAndTheVehicleTurns() async {
        let (map, window) = shown(framing(.north))
        defer { window.isHidden = true }
        _ = adherenceSeen(by: map)
        #expect(await eventually(within: 10) { isNear(pointing(map) ?? 0, 90, 1) })
        #expect(isNear(map.mapView.camera.heading, 0, 0.5))
    }

    @Test func headingUpTheMapTurnsAndTheVehiclePointsUp() async {
        let (map, window) = shown(framing(.heading))
        defer { window.isHidden = true }
        _ = adherenceSeen(by: map)
        #expect(await eventually(within: 10) { isNear(map.mapView.camera.heading, 90, 0.5) })
        #expect(await eventually(within: 10) { isNear(pointing(map) ?? 180, 0, 1) })
    }

    /// The choice can be read, or change, after the map is up.
    @Test func aChangeOfWhatIsUpMovesTheMap() async {
        let (map, window) = shown(framing(.heading))
        defer { window.isHidden = true }
        _ = adherenceSeen(by: map)
        #expect(await eventually(within: 10) { isNear(map.mapView.camera.heading, 90, 0.5) })
        map.followFraming = framing(.north)
        #expect(await eventually(within: 10) { isNear(map.mapView.camera.heading, 0, 0.5) })
    }

    /// The map is told of the fix first, as Tracking tells it.
    func adherenceSeen(by map: RouteMapViewController) -> Adherence {
        let adherence = adherence()
        map.update(adherence, ring: .onTime)
        return adherence
    }
}
