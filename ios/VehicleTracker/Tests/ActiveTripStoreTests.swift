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
                                trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 1),
                                driverEmail: "d@test.com")
        try store.save(active)
        #expect(try store.load() == active)

        try store.clear()
        #expect(try store.load() == nil)
        // `clear()` used the same encoded path, so it silently left the file
        // behind: check the disk, not just the reader.
        #expect(!FileManager.default.fileExists(atPath: directory.appending(path: "active-trip.json").path(percentEncoded: false)))
    }

    /// A trip stored by a build before servers were recorded has no
    /// `serverURL` in its file; it still reads back, with no server.
    @Test func aTripSavedByAnOlderBuildReadsBackWithNoServer() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = FileActiveTripStore(directory: directory)
        var active = ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "bus-1", label: "Bus 1"),
                                trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 1),
                                driverEmail: "d@test.com", serverURL: URL(string: "https://positions.example.org")!)
        try store.save(active)
        #expect(try store.load() == active, "the server goes with the trip")

        let file = directory.appending(path: "active-trip.json")
        var json = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        #expect(json.removeValue(forKey: "serverURL") != nil)
        try JSONSerialization.data(withJSONObject: json).write(to: file)

        active.serverURL = nil
        #expect(try store.load() == active)
    }

    /// P44: Tracking's count carries on after a relaunch, read by a store
    /// made afresh, as the next launch's is.
    @Test func theSentCountIsKeptForItsOwnTripAndGoesWithIt() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = FileActiveTripStore(directory: directory)
        #expect(store.loadSent(for: 7) == 0, "no file yet, as for a trip stored by an older build")

        try store.saveSent(142, for: 7)
        #expect(FileActiveTripStore(directory: directory).loadSent(for: 7) == 142)
        #expect(store.loadSent(for: 8) == 0, "another trip's count is not this one's")

        try store.clear()
        #expect(store.loadSent(for: 7) == 0)
        #expect(!FileManager.default.fileExists(atPath: directory.appending(path: "active-trip-sent.json").path(percentEncoded: false)))
    }

    @Test func anUnreadableSentCountStartsAgainFromZero() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("not json".utf8).write(to: directory.appending(path: "active-trip-sent.json"))
        #expect(FileActiveTripStore(directory: directory).loadSent(for: 7) == 0)
    }

    @Test func theInMemoryStoreKeepsTheCountTheSameWay() throws {
        let store = InMemoryActiveTripStore()
        #expect(store.loadSent(for: 7) == 0)
        try store.saveSent(142, for: 7)
        #expect(store.loadSent(for: 7) == 142)
        #expect(store.loadSent(for: 8) == 0)
        try store.clear()
        #expect(store.loadSent(for: 7) == 0)
    }

    let owed = [PendingTripEnd(serverTripID: 7, driverEmail: "a@test.com", serverURL: "https://positions.example.org"),
                PendingTripEnd(serverTripID: 9, driverEmail: "b@test.com", serverURL: "https://other.example.org")]

    /// What `TripSession` relies on, whichever store holds the pending ends.
    func checkPendingEnds(in store: any ActiveTripStoring) throws {
        #expect(try store.loadPendingEnds().isEmpty)
        try store.savePendingEnds(owed)
        #expect(try store.loadPendingEnds() == owed)

        // The active trip is saved and cleared on every start and end; the
        // pending ends must outlive it.
        try store.save(ActiveTrip(serverTripID: 11, vehicle: Vehicle(id: "bus-1", label: "Bus 1"),
                                  trip: TripFixtures.t1, startedAt: TripFixtures.at(8, 1),
                                  driverEmail: "a@test.com"))
        try store.clear()
        #expect(try store.loadPendingEnds() == owed)

        try store.savePendingEnds([])
        #expect(try store.loadPendingEnds().isEmpty)
    }

    @Test func fileStoreKeepsPendingEndsApartFromTheActiveTrip() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = FileActiveTripStore(directory: directory)

        try checkPendingEnds(in: store)
        #expect(!FileManager.default.fileExists(atPath: directory.appending(path: "pending-trip-ends.json").path(percentEncoded: false)),
                "an empty list removes the file")

        try store.savePendingEnds(owed)
        #expect(try FileActiveTripStore(directory: directory).loadPendingEnds() == owed, "a relaunch finds them")
    }

    @Test func inMemoryStoreKeepsPendingEndsApartFromTheActiveTrip() throws {
        try checkPendingEnds(in: InMemoryActiveTripStore())
    }

    @Test func anUnreadablePendingEndsFileReadsAsEmpty() throws {
        let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("not json".utf8).write(to: directory.appending(path: "pending-trip-ends.json"))

        #expect(try FileActiveTripStore(directory: directory).loadPendingEnds().isEmpty)
    }

    @Test(arguments: [
        ("https://positions.example.org", true),
        ("HTTPS://Positions.Example.org/", true),
        ("https://positions.example.org:443", true),
        (" https://positions.example.org ", true),
        ("https://positions.example.org:8443", false),
        ("http://positions.example.org", false),
        ("https://positions.example.org/agency", false),
        ("https://other.example.org", false),
    ])
    func aPendingEndIsOwedToItsServerHoweverItsAddressIsTyped(_ address: String, _ owed: Bool) {
        let end = PendingTripEnd(serverTripID: 7, driverEmail: "d@test.com", serverURL: "https://positions.example.org")
        #expect(end.isOwed(to: address) == owed)
    }
}
