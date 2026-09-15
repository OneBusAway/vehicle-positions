import Foundation

/// Where the active trip survives a relaunch (spec §5.6).
nonisolated protocol ActiveTripStoring: Sendable {
    func load() throws -> ActiveTrip?
    func save(_ trip: ActiveTrip) throws
    func clear() throws
}

/// `active-trip.json` in the app's Application Support directory.
nonisolated final class FileActiveTripStore: ActiveTripStoring, Sendable {
    private let url: URL

    init(directory: URL? = nil) {
        let base = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        url = base.appending(path: "active-trip.json")
    }

    func load() throws -> ActiveTrip? {
        guard FileManager.default.fileExists(atPath: url.path()) else { return nil }
        do {
            return try JSONCoding.decoder.decode(ActiveTrip.self, from: Data(contentsOf: url))
        } catch {
            // Written by an incompatible build; a trip we cannot read is one we
            // cannot resume, and the server will reap it.
            try? clear()
            return nil
        }
    }

    func save(_ trip: ActiveTrip) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONCoding.encoder.encode(trip).write(to: url, options: .atomic)
    }

    func clear() throws {
        if FileManager.default.fileExists(atPath: url.path()) {
            try FileManager.default.removeItem(at: url)
        }
    }
}

nonisolated final class InMemoryActiveTripStore: ActiveTripStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var trip: ActiveTrip?

    init(_ initial: ActiveTrip? = nil) { trip = initial }

    func load() throws -> ActiveTrip? { lock.withLock { trip } }
    func save(_ trip: ActiveTrip) throws { lock.withLock { self.trip = trip } }
    func clear() throws { lock.withLock { trip = nil } }
}
