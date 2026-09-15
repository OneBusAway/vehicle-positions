import Foundation
import Testing
@testable import VehicleTracker

@Suite struct ActiveTripStoreTests {
    /// The real store lives in "Application Support", whose space must not be
    /// percent-encoded on the way into `FileManager`: `URL.path()` encodes by
    /// default, and an encoded path never names a file that exists — so the
    /// trip a relaunch should find came back nil and `clear()` left the file
    /// behind.
    @Test func roundTripsThroughADirectoryWithASpaceInItsName() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = FileActiveTripStore(directory: directory)

        #expect(try store.load() == nil)

        let active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "bus-1", label: "Bus 1"),
                                trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 1))
        try store.save(active)
        #expect(try store.load() == active)

        try store.clear()
        #expect(try store.load() == nil)
    }
}
