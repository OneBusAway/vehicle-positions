import CryptoKit
import Foundation
import OSLog

/// Reports the phone could not send for want of a connection, oldest first,
/// kept for `POST /api/v1/locations/history` once a report gets through. They
/// never go to the realtime feed: by then they say where the vehicle was, not
/// where it is. Android's `OfflineFixQueue`, with one difference: each driver
/// and server has a file of their own, so a report can only be read back for
/// the sign-in it was recorded under, and none is ever uploaded as another
/// driver's.
///
/// One JSON report per line, in `offline-fixes/` in the app's Application
/// Support directory. An add appends a line. A removal writes the file anew
/// beside the old one and moves it over, so a crash leaves one file or the
/// other, never half of either. The files can be written once the phone has
/// been unlocked after it started, so reports are kept while it sits locked in
/// its holder, even in an agency build that turns on complete protection.
actor OfflineFixQueue {
    /// A day of fixes at Android's one every ten seconds, the same number on
    /// both apps. At this app's one every five it holds twelve hours, a whole
    /// shift; a phone out of coverage for longer loses its oldest first.
    static let capacity = 8_640

    private let directory: URL
    private let capacity: Int
    /// How far a file may run past `capacity` before it is written anew. An
    /// add to a full queue drops the oldest report, and writing the whole
    /// file (about 2 MB) for each would mean one such write every five
    /// seconds for as long as the phone stays out of coverage.
    private let slack: Int
    /// Each owner's reports once their file has been read, keyed by
    /// `Owner.key`, and how many lines the file has.
    private var held: [String: Held] = [:]
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "offline-queue")

    private struct Held {
        var reports: [LocationReport]
        var linesOnDisk: Int
    }

    init(directory: URL? = nil, capacity: Int = OfflineFixQueue.capacity) {
        let base = directory ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        self.directory = base.appending(path: "offline-fixes")
        self.capacity = capacity
        slack = max(1, capacity / 10)
    }

    /// Adds `report` behind `owner`'s others, dropping their oldest when there
    /// are more than `capacity`.
    func add(_ report: LocationReport, for owner: Owner) throws {
        var held = try read(owner)
        held.reports.append(report)
        if held.reports.count > capacity {
            held.reports.removeFirst(held.reports.count - capacity)
        }
        if held.linesOnDisk < capacity + slack {
            try append(report, for: owner)
            held.linesOnDisk += 1
        } else {
            try write(held.reports, for: owner)
            held.linesOnDisk = held.reports.count
        }
        // Only once the file has it, so a report that could not be written
        // is not read back either.
        self.held[owner.key] = held
    }

    /// `owner`'s oldest reports, at most `max` of them.
    func oldest(_ max: Int, for owner: Owner) throws -> [LocationReport] {
        Array(try read(owner).reports.prefix(max))
    }

    /// Removes `reports`, a run `oldest` returned, once they have been sent or
    /// dropped. A full queue may have dropped some of them since; those are
    /// skipped, so no report added since is taken in their place.
    func remove(_ reports: [LocationReport], for owner: Owner) throws {
        var held = try read(owner)
        // A report is known by its vehicle and timestamp. The run was the
        // front of the queue when it was read, and reports have only left from
        // the front or joined at the back since, so what is left of it is
        // still the front.
        var gone = 0
        for report in reports where gone < held.reports.count {
            let front = held.reports[gone]
            if front.vehicleID == report.vehicleID && front.timestamp == report.timestamp {
                gone += 1
            }
        }
        guard gone > 0 else { return }
        held.reports.removeFirst(gone)
        try write(held.reports, for: owner)
        held.linesOnDisk = held.reports.count
        self.held[owner.key] = held
    }

    /// Removes every report kept for anyone but `owner`, files and all.
    func removeAll(except owner: Owner) throws {
        // Out of memory as well as off disk, or a driver who signs back in
        // later would have their dropped reports read back from memory.
        held = held.filter { $0.key == owner.key }
        guard FileManager.default.fileExists(atPath: directory.path(percentEncoded: false)) else { return }
        let ownFile = file(for: owner).lastPathComponent
        for other in try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
        where other.lastPathComponent != ownFile {
            try FileManager.default.removeItem(at: other)
        }
    }

    /// Reads `owner`'s file the first time it is asked for. A line that can't
    /// be read, such as one cut short by a crash mid-append, is skipped rather
    /// than allowed to cost the rest.
    private func read(_ owner: Owner) throws -> Held {
        if let held = held[owner.key] { return held }
        let url = file(for: owner)
        guard FileManager.default.fileExists(atPath: url.path(percentEncoded: false)) else {
            let empty = Held(reports: [], linesOnDisk: 0)
            held[owner.key] = empty
            return empty
        }
        let text = String(decoding: try Data(contentsOf: url), as: UTF8.self)
        let lines = text.split(separator: "\n")
        let reports = lines.compactMap { try? JSONCoding.decoder.decode(LocationReport.self, from: Data($0.utf8)) }
        var held = Held(reports: Array(reports.suffix(capacity)), linesOnDisk: lines.count)
        let skipped = lines.count - reports.count
        if skipped > 0 {
            log.warning("skipped \(skipped) unreadable queued reports")
        }
        // Left in place, a line cut short would join the next append and cost
        // that report too.
        if skipped > 0 || !(text.isEmpty || text.hasSuffix("\n")) {
            try write(held.reports, for: owner)
            held.linesOnDisk = held.reports.count
        }
        self.held[owner.key] = held
        return held
    }

    private func append(_ report: LocationReport, for owner: Owner) throws {
        var line = try JSONCoding.encoder.encode(report)
        line.append(UInt8(ascii: "\n"))
        let url = file(for: owner)
        guard FileManager.default.fileExists(atPath: url.path(percentEncoded: false)) else {
            try create(line, at: url)
            return
        }
        let handle = try FileHandle(forWritingTo: url)
        do {
            try handle.seekToEnd()
            try handle.write(contentsOf: line)
        } catch {
            try? handle.close()
            throw error
        }
        try handle.close()
    }

    /// Writes `reports` as `owner`'s whole file, or removes it when there are
    /// none.
    private func write(_ reports: [LocationReport], for owner: Owner) throws {
        let url = file(for: owner)
        guard !reports.isEmpty else {
            if FileManager.default.fileExists(atPath: url.path(percentEncoded: false)) {
                try FileManager.default.removeItem(at: url)
            }
            return
        }
        var data = Data()
        for report in reports {
            data.append(try JSONCoding.encoder.encode(report))
            data.append(UInt8(ascii: "\n"))
        }
        try create(data, at: url)
    }

    private func create(_ data: Data, at url: URL) throws {
        try FileManager.default.createDirectory(
            at: directory, withIntermediateDirectories: true,
            attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication])
        try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private func file(for owner: Owner) -> URL {
        directory.appending(path: "\(owner.key).jsonl")
    }
}

extension OfflineFixQueue {
    /// Whose reports a file holds: the driver and the server they were
    /// recorded under, as a SHA-256 hash, so the email of a driver who has
    /// handed the phone on is not in a file name. The server is read as
    /// `isSameServer` reads it, port and path included: two servers on one
    /// host are still two servers, and a trip id on one means nothing on the
    /// other. The email is read in any case, without the spaces round it.
    nonisolated struct Owner: Sendable, Hashable {
        let key: String

        /// Nil without a server or an email: there is nobody to send as.
        init?(server: String, email: String) {
            let email = email.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            let server = server.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !email.isEmpty, URL(string: server)?.host() != nil else { return nil }
            let owner = "\(serverKey(server))|\(email)"
            key = SHA256.hash(data: Data(owner.utf8)).map { String(format: "%02x", $0) }.joined()
        }
    }
}
