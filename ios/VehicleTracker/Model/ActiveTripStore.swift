import Foundation
import OSLog

/// Where the active trip survives a relaunch (spec §5.6), along with the ends
/// of trips that were ended on this phone only and have not reached the server.
nonisolated protocol ActiveTripStoring: Sendable {
    func load() throws -> ActiveTrip?
    func save(_ trip: ActiveTrip) throws
    func clear() throws
    func loadPendingEnds() throws -> [PendingTripEnd]
    /// An empty list removes them all.
    func savePendingEnds(_ ends: [PendingTripEnd]) throws
}

/// `active-trip.json` in the app's Application Support directory, and
/// `pending-trip-ends.json` beside it.
nonisolated final class FileActiveTripStore: ActiveTripStoring, Sendable {
    private let url: URL
    private let pendingEndsURL: URL
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "active-trip-store")

    init(directory: URL? = nil) {
        let base = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        url = base.appending(path: "active-trip.json")
        pendingEndsURL = base.appending(path: "pending-trip-ends.json")
    }

    /// `URL.path()` keeps percent-encoding by default, and the Application
    /// Support directory has a space in its name — so the encoded path never
    /// names a file that exists. Every path check here goes through these.
    private var filePath: String { url.path(percentEncoded: false) }
    private var pendingEndsPath: String { pendingEndsURL.path(percentEncoded: false) }

    func load() throws -> ActiveTrip? {
        guard FileManager.default.fileExists(atPath: filePath) else { return nil }
        do {
            return try JSONCoding.decoder.decode(ActiveTrip.self, from: Data(contentsOf: url))
        } catch {
            // Written by an incompatible build; a trip we cannot read is one we
            // cannot resume, or end from this phone.
            try? clear()
            return nil
        }
    }

    func save(_ trip: ActiveTrip) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONCoding.encoder.encode(trip).write(to: url, options: .atomic)
    }

    func clear() throws {
        if FileManager.default.fileExists(atPath: filePath) {
            try FileManager.default.removeItem(at: url)
        }
    }

    func loadPendingEnds() throws -> [PendingTripEnd] {
        guard FileManager.default.fileExists(atPath: pendingEndsPath) else { return [] }
        do {
            return try JSONCoding.decoder.decode([PendingTripEnd].self, from: Data(contentsOf: pendingEndsURL))
        } catch {
            // A trip we cannot name is one this phone cannot end, so say so
            // rather than lose it quietly.
            log.error("could not read the pending trip ends: \(String(describing: error))")
            return []
        }
    }

    func savePendingEnds(_ ends: [PendingTripEnd]) throws {
        guard !ends.isEmpty else {
            if FileManager.default.fileExists(atPath: pendingEndsPath) {
                try FileManager.default.removeItem(at: pendingEndsURL)
            }
            return
        }
        try FileManager.default.createDirectory(at: pendingEndsURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONCoding.encoder.encode(ends).write(to: pendingEndsURL, options: .atomic)
    }
}

nonisolated final class InMemoryActiveTripStore: ActiveTripStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var trip: ActiveTrip?
    private var pendingEnds: [PendingTripEnd] = []

    init(_ initial: ActiveTrip? = nil) { trip = initial }

    func load() throws -> ActiveTrip? { lock.withLock { trip } }
    func save(_ trip: ActiveTrip) throws { lock.withLock { self.trip = trip } }
    func clear() throws { lock.withLock { trip = nil } }
    func loadPendingEnds() throws -> [PendingTripEnd] { lock.withLock { pendingEnds } }
    func savePendingEnds(_ ends: [PendingTripEnd]) throws { lock.withLock { pendingEnds = ends } }
}
