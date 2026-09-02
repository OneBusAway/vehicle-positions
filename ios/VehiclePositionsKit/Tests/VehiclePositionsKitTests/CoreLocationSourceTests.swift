#if os(iOS)
import CoreLocation
import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite struct CoreLocationSourceTests {
    @Test func backgroundActivityHandleCanBeInvalidated() {
        let source = CoreLocationSource()
        let handle = source.beginBackgroundActivity()
        handle.invalidate() // must not crash on the simulator without authorization
    }

    @Test func sampleMappingFromLocation() {
        // CLLocationUpdate cannot be constructed in tests; exercise the shared mapping helper instead.
        let loc = CLLocation(
            coordinate: CLLocationCoordinate2D(latitude: 47.6, longitude: -122.33),
            altitude: 0,
            horizontalAccuracy: 7,
            verticalAccuracy: 0,
            course: 90,
            speed: 8,
            timestamp: Date(timeIntervalSince1970: 100)
        )
        let s = LocationSample(location: loc, stationary: true, diagnostic: nil)
        #expect(s.fix?.latitude == 47.6)
        #expect(s.fix?.horizontalAccuracy == 7)
        #expect(s.fix?.course == 90)
        #expect(s.fix?.speed == 8)
        #expect(s.isStationary)
        #expect(LocationSample(location: nil, stationary: false, diagnostic: .locationUnavailable).fix == nil)
    }
}
#endif
