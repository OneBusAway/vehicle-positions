import Foundation
import OSLog

/// Where the active trip survives a relaunch (spec §5.6), along with how many
/// of its reports the server accepted, and the ends of trips that were ended
/// on this phone only and have not reached the server.
nonisolated protocol ActiveTripStoring: Sendable {
    func load() throws -> ActiveTrip?
    func save(_ trip: ActiveTrip) throws
    /// Removes the trip and its sent count.
    func clear() throws
    /// The reports the server accepted on trip `serverTripID`; 0 when none
    /// were kept for it.
    func loadSent(for serverTripID: Int64) -> Int
    func saveSent(_ count: Int, for serverTripID: Int64) throws
    func loadPendingEnds() throws -> [PendingTripEnd]
    /// An empty list removes them all.
    func savePendingEnds(_ ends: [PendingTripEnd]) throws
}

/// `active-trip.json` in the app's Application Support directory, and
/// `active-trip-sent.json` and `pending-trip-ends.json` beside it. The sent
/// count has a file of its own: it changes every few seconds, and the trip's
/// file carries the whole route, too much to write again for one number.
nonisolated final class FileActiveTripStore: ActiveTripStoring, Sendable {
    private let url: URL
    private let sentURL: URL
    private let pendingEndsURL: URL
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "active-trip-store")

    init(directory: URL? = nil) {
        let base = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        url = base.appending(path: "active-trip.json")
        sentURL = base.appending(path: "active-trip-sent.json")
        pendingEndsURL = base.appending(path: "pending-trip-ends.json")
    }

    /// `URL.path()` keeps percent-encoding by default, and the Application
    /// Support directory has a space in its name — so the encoded path never
    /// names a file that exists. Every path check here goes through these.
    private var filePath: String { url.path(percentEncoded: false) }
    private var sentPath: String { sentURL.path(percentEncoded: false) }
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
        if FileManager.default.fileExists(atPath: sentPath) {
            try FileManager.default.removeItem(at: sentURL)
        }
    }

    // A trip stored by a build before counts were kept has no file, and
    // reads 0, as a fresh trip does.
    func loadSent(for serverTripID: Int64) -> Int {
        guard FileManager.default.fileExists(atPath: sentPath) else { return 0 }
        do {
            let sent = try JSONCoding.decoder.decode(SentCount.self, from: Data(contentsOf: sentURL))
            return sent.serverTripID == serverTripID ? sent.count : 0
        } catch {
            log.error("could not read the sent count; it starts again from 0: \(String(describing: error), privacy: .public)")
            return 0
        }
    }

    func saveSent(_ count: Int, for serverTripID: Int64) throws {
        try FileManager.default.createDirectory(at: sentURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONCoding.encoder.encode(SentCount(serverTripID: serverTripID, count: count)).write(to: sentURL, options: .atomic)
    }

    func loadPendingEnds() throws -> [PendingTripEnd] {
        guard FileManager.default.fileExists(atPath: pendingEndsPath) else { return [] }
        do {
            return try JSONCoding.decoder.decode([PendingTripEnd].self, from: Data(contentsOf: pendingEndsURL))
        } catch {
            // A trip we cannot name is one this phone cannot end, so say so
            // rather than lose it quietly.
            log.error("could not read the pending trip ends: \(String(describing: error), privacy: .public)")
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

/// The sent count as `active-trip-sent.json` holds it: for one trip only.
private nonisolated struct SentCount: Codable {
    var serverTripID: Int64
    var count: Int

    private enum CodingKeys: String, CodingKey {
        case count
        case serverTripID = "server_trip_id"
    }
}

nonisolated final class InMemoryActiveTripStore: ActiveTripStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var trip: ActiveTrip?
    private var sent: SentCount?
    private var pendingEnds: [PendingTripEnd] = []

    init(_ initial: ActiveTrip? = nil) { trip = initial }

    func load() throws -> ActiveTrip? { lock.withLock { trip } }
    func save(_ trip: ActiveTrip) throws { lock.withLock { self.trip = trip } }
    func clear() throws {
        lock.withLock {
            trip = nil
            sent = nil
        }
    }
    func loadSent(for serverTripID: Int64) -> Int {
        lock.withLock { sent?.serverTripID == serverTripID ? sent?.count ?? 0 : 0 }
    }
    func saveSent(_ count: Int, for serverTripID: Int64) throws {
        lock.withLock { sent = SentCount(serverTripID: serverTripID, count: count) }
    }
    func loadPendingEnds() throws -> [PendingTripEnd] { lock.withLock { pendingEnds } }
    func savePendingEnds(_ ends: [PendingTripEnd]) throws { lock.withLock { pendingEnds = ends } }
}
