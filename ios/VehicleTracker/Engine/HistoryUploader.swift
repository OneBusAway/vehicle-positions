import Foundation
import OSLog

/// What one `HistoryUploader.uploadIfIdle(through:)` call did with the queue.
nonisolated enum HistoryUpload: Sendable, Equatable {
    /// Nothing was queued for the driver signed in, once reports too old to
    /// upload were dropped.
    case nothingQueued
    /// One batch left the queue: the server took it, or refused it for good.
    case batchSent
    /// The batch failed for now and stays queued.
    case kept
    /// Nobody is signed in to send them as.
    case notSignedIn
}

/// Keeps the reports `LocationReporter` could not send for want of a
/// connection, under the driver and server signed in, and uploads them to
/// `POST /api/v1/locations/history` a batch at a time. Android's
/// `HistoryUploader`: one upload at a time, because a batch stays at the
/// front of the queue until it has gone, and two uploads at once would both
/// send it. One object for the app, so that holds across trips too.
@MainActor
final class HistoryUploader {
    /// The most reports one upload carries; the server takes at most 500.
    static let batchSize = 500
    /// Refusals an upload would get again however often it were retried: bad
    /// data, a vehicle no longer assigned to the driver, a server without
    /// the endpoint. The batch is dropped. Anything else (no connection, a
    /// sign-in the refresh could not save, the rate limit, a server error)
    /// fails for now, and the batch stays for the next try.
    static let refusedForGood: Set<Int> = [400, 403, 404]
    /// How many seconds old a queued report may be and still go: the server
    /// takes a day, less a margin for the phone's clock and the time the
    /// upload takes.
    static let maxAgeSeconds: Int64 = 24 * 3600 - 600

    private let queue: OfflineFixQueue
    /// Who is signed in, and to which server: the reports are kept and sent
    /// under them.
    private let settings: AppSettings
    private let now: () -> Date
    private var isUploading = false
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "history-uploader")

    init(queue: OfflineFixQueue, settings: AppSettings, now: @escaping () -> Date = Date.init) {
        self.queue = queue
        self.settings = settings
        self.now = now
    }

    private var owner: OfflineFixQueue.Owner? {
        OfflineFixQueue.Owner(server: settings.serverURLString, email: settings.email)
    }

    /// Keeps `report`, which could not be sent for want of a connection, for
    /// the driver signed in. One that can't be kept (a full disk, say) is
    /// lost, and the trip goes on.
    func keep(_ report: LocationReport) async {
        guard let owner else {
            log.warning("not keeping a location report for history: no driver and server to keep it for")
            return
        }
        do {
            try await queue.add(report, for: owner)
        } catch {
            log.error("could not keep a location report for history: \(String(describing: error), privacy: .public)")
        }
    }

    /// Uploads the oldest kept reports of one vehicle through `api`, unless
    /// an upload is already on its way: nil then.
    @discardableResult
    func uploadIfIdle(through api: any TrackerAPI) async -> HistoryUpload? {
        // Checked and set with no await between, so two callers can't both
        // find no upload out.
        guard !isUploading else { return nil }
        isUploading = true
        defer { isUploading = false }
        return await uploadOneBatch(through: api)
    }

    private func uploadOneBatch(through api: any TrackerAPI) async -> HistoryUpload {
        guard let owner else { return .notSignedIn }
        do {
            var batch = try await queue.oldest(Self.batchSize, for: owner)
            let expired = batch.prefix { isTooOld($0) }
            if !expired.isEmpty {
                log.notice("dropping \(expired.count) kept reports older than the server takes")
                try await queue.remove(Array(expired), for: owner)
                batch = try await queue.oldest(Self.batchSize, for: owner)
            }
            // The endpoint takes one vehicle per upload.
            guard let vehicleID = batch.first?.vehicleID else { return .nothingQueued }
            let reports = Array(batch.prefix { $0.vehicleID == vehicleID })
            do {
                try await api.postLocationHistory(LocationHistoryRequest(vehicleID: vehicleID, reports: reports))
                log.notice("uploaded \(reports.count) kept reports for location history")
            } catch let APIError.status(code, message) where Self.refusedForGood.contains(code) {
                log.warning("dropping \(reports.count) kept reports after HTTP \(code): \(message, privacy: .public)")
            } catch {
                log.warning("keeping \(reports.count) reports for the next upload: \(String(describing: error), privacy: .public)")
                return .kept
            }
            try await queue.remove(reports, for: owner)
            return .batchSent
        } catch {
            // The upload's own failures are handled above; this is the
            // queue's file failing.
            log.error("could not read or update the kept reports: \(String(describing: error), privacy: .public)")
            return .kept
        }
    }

    /// In whole seconds, as the report's timestamp is.
    private func isTooOld(_ report: LocationReport) -> Bool {
        Int64(now().timeIntervalSince1970) - report.timestamp > Self.maxAgeSeconds
    }
}
