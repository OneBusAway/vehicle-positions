import BackgroundTasks
import Foundation
import Observation
import OSLog
import UIKit

/// The reports kept on the phone for the driver signed in, as the trip card
/// says them: Android's `ReportsWaiting`.
nonisolated struct ReportsWaiting: Sendable, Equatable {
    /// Those young enough to upload: the card's number. The uploads drop
    /// older ones rather than send them, so they are left out, and the count
    /// and the uploads agree.
    var count = 0
    /// Every one kept, the ones too old to upload too, so a drop in `count`
    /// tells whether reports left the phone or only aged out on it.
    var kept = 0

    /// Whether VoiceOver reads the count out as it goes from `old` to this:
    /// as the line comes, and as the count drops because reports left the
    /// phone, as the backlog uploads. Not as each report adds one while there
    /// is no connection, nor as one ages out while the phone stays offline:
    /// after a day of that, either would be every report. Android's
    /// `speaksWaiting`.
    func isAnnounced(after old: ReportsWaiting) -> Bool {
        count > 0 && (old.count == 0 || (count < old.count && kept < old.kept))
    }
}

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
/// `POST /api/v1/locations/history` a batch at a time: after each report
/// that gets through, and with no trip running, in a drain. Android's
/// `HistoryUploader` and `BacklogDrain`: one upload at a time, because a
/// batch stays at the front of the queue until it has gone, and two uploads
/// at once would both send it. One object for the app, so that holds across
/// trips too.
///
/// It also counts them, for the trip card (`waiting`). The queue holds the
/// reports; the count is taken from it again after each keep and each
/// removal, as a drain starts, at each sign-in and sign-out, and as the
/// oldest one counted grows too old to upload.
///
/// For a phone that never comes back to the front after a trip ended
/// without a connection, each report kept, and each drain that stops with
/// some left, asks iOS for a background task that drains once there is a
/// connection: Android's `BacklogUploadJob`. iOS picks the moment, often
/// once the phone is idle, which for a driver's phone is the night after the
/// shift, still inside the day the server takes reports for. Not a
/// background `URLSession`: its uploads would carry the token they were made
/// with, and an access token lasts 15 minutes, with no way to refresh it
/// when the system sends them hours later. A drain goes through the app's
/// own client, which refreshes it.
@MainActor @Observable
final class HistoryUploader {
    /// The most reports one upload carries, about 20 KB. The server takes up
    /// to 500, but a weak 2G signal can't get that many through inside
    /// `TrackerClient`'s timeout: the batch would be kept and start over each
    /// time, and never go.
    static let batchSize = 80
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
    /// How far apart a drain sends its batches: the server takes one history
    /// upload per driver every five seconds.
    static let drainInterval: Duration = .seconds(5)
    /// The background task that drains. Info.plist's
    /// `BGTaskSchedulerPermittedIdentifiers` lists it.
    static let backgroundTaskIdentifier = "org.onebusaway.vehicletracker.send-kept-reports"

    /// The reports kept for the driver signed in, as `countWaiting()` last
    /// counted them.
    private(set) var waiting = ReportsWaiting()

    private let queue: OfflineFixQueue
    /// Who is signed in, and to which server: the reports are kept and sent
    /// under them.
    private let settings: AppSettings
    private let now: () -> Date
    private let backgroundTime: any BackgroundTime
    private let backgroundTasks: any BackgroundTaskScheduler
    private let sleep: (Duration) async throws -> Void
    /// The wait until the oldest report counted grows too old to upload.
    private let sleepUntilExpiry: (Duration) async throws -> Void
    private var isUploading = false
    private var drainTask: Task<Void, Never>?
    /// Whether iOS took the background task at launch.
    private(set) var isBackgroundTaskRegistered = false
    /// The background task was asked for since this launch, or since its
    /// last run began. iOS replaces a request still waiting with a new one
    /// for the same task, so asking again would change nothing; this keeps
    /// a report kept every five seconds from asking each time. Set whether
    /// or not iOS took the request: it refuses for good (Background App
    /// Refresh off, or the simulator), not for now.
    private var isBackgroundTaskAsked = false
    /// Bumped by each count, so a count that read the queue before a newer
    /// one can't land after it.
    private var countGeneration = 0
    /// When the oldest report counted grows too old to upload, in seconds
    /// since 1970, and the wait that counts again then.
    private var countAgainAt: Int64?
    private var expiry: Task<Void, Never>?
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "history-uploader")

    init(queue: OfflineFixQueue, settings: AppSettings, now: @escaping () -> Date = Date.init,
         backgroundTime: any BackgroundTime = ApplicationBackgroundTime(),
         backgroundTasks: any BackgroundTaskScheduler = SystemBackgroundTaskScheduler(),
         sleep: @escaping (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
         sleepUntilExpiry: @escaping (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.queue = queue
        self.settings = settings
        self.now = now
        self.backgroundTime = backgroundTime
        self.backgroundTasks = backgroundTasks
        self.sleep = sleep
        self.sleepUntilExpiry = sleepUntilExpiry
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
            return
        }
        askForBackgroundTask()
        await countWaiting()
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

    /// Counts the reports kept for the driver signed in into `waiting`. A
    /// report grows too old to upload with no keep or upload to count it
    /// out, so the count is also taken again when the oldest one counted
    /// does, as Android's `WaitingReports` does.
    func countWaiting() async {
        countGeneration += 1
        let generation = countGeneration
        var taken: [Int64] = []
        if let owner {
            do {
                taken = try await queue.timestamps(for: owner)
            } catch {
                log.error("could not count the kept reports: \(String(describing: error), privacy: .public)")
                return
            }
        }
        guard generation == countGeneration else { return }
        let seconds = Int64(now().timeIntervalSince1970)
        let young = taken.filter { !Self.isTooOld($0, at: seconds) }
        waiting = ReportsWaiting(count: young.count, kept: taken.count)

        let next = young.min().map { $0 + Self.maxAgeSeconds + 1 }
        guard next != countAgainAt else { return }
        countAgainAt = next
        expiry?.cancel()
        expiry = next.map { at in
            Task { [weak self, sleepUntilExpiry = self.sleepUntilExpiry] in
                do {
                    try await sleepUntilExpiry(.seconds(at - seconds))
                } catch {
                    return
                }
                // Counted again however the clock moved meanwhile, so a wait
                // that ends early still sets the next one.
                self?.countAgainAt = nil
                await self?.countWaiting()
            }
        }
    }

    /// Uploads the kept reports of the driver signed in batch after batch,
    /// `drainInterval` apart, with no trip needed: the server takes them
    /// after the trip has ended. Each batch goes through what `api` returns
    /// then, and nil stops the drain. It also stops once nothing is left, or
    /// at a batch that fails for now, which waits for the next drain or the
    /// next report that gets through. A call while a drain runs is left to
    /// that drain, unless it has been stopped.
    ///
    /// Under background time of its own, so a drain started as the driver
    /// ends a trip and locks the phone can finish. If iOS takes the time back
    /// first, the drain stops, and a batch it had out stays kept. A drain
    /// that stops with reports left asks for the background task. Returns
    /// the drain, or the one already running.
    @discardableResult
    func drain(through api: @escaping @MainActor () -> (any TrackerAPI)?) -> Task<Void, Never> {
        // A drain that was stopped starts no more uploads, but one waiting on
        // a token refresh, which doesn't stop for it (`TokenRefresher` says
        // why), runs on until the refresh has its answer, and the app may be
        // suspended before then. A new drain starts beside it, and
        // `uploadIfIdle` keeps the two from uploading at once.
        if let drainTask, !drainTask.isCancelled { return drainTask }
        let time = DrainTime(backgroundTime)
        let drain = Task { [weak self] in
            await self?.uploadAll(through: api)
            time.end()
            // A drain that was stopped may have been replaced by now.
            if !Task.isCancelled { self?.drainTask = nil }
            if let self, waiting.count > 0 { askForBackgroundTask() }
        }
        time.begin { [weak self] in
            self?.log.notice("out of background time while sending kept reports; the rest wait for the next drain")
            // The upload out fails before its batch is removed, so the batch
            // stays kept.
            drain.cancel()
        }
        drainTask = drain
        return drain
    }

    private func uploadAll(through api: @MainActor () -> (any TrackerAPI)?) async {
        // Whatever the uploads make of them: a drain is also how a launch,
        // and the app coming to the front, first looks at what was kept.
        await countWaiting()
        while let client = api() {
            switch await uploadIfIdle(through: client) {
            case .kept?, .nothingQueued?, .notSignedIn?:
                return
            case .batchSent?, nil:
                // nil: another upload is out, so this one tries after it.
                break
            }
            do {
                try await sleep(Self.drainInterval)
            } catch {
                return
            }
        }
    }

    /// Has iOS run the background task with `drain`, which starts a drain or
    /// hands over the one running. Before the app finishes launching, as iOS
    /// requires.
    func registerBackgroundTask(drain: @escaping @MainActor () -> Task<Void, Never>?) {
        isBackgroundTaskRegistered = backgroundTasks.register(Self.backgroundTaskIdentifier) { [weak self] run in
            self?.runBackgroundTask(run, drain: drain)
        }
        if !isBackgroundTaskRegistered {
            log.error("iOS refused the background task \(Self.backgroundTaskIdentifier, privacy: .public): Info.plist doesn't permit it")
        }
    }

    /// One run of the background task: a drain until nothing is left to
    /// send, or until iOS ends the run. Reports left have asked for the next
    /// run by the time this one completes: the drain asks as it stops, and
    /// an expiry before it completes the run.
    private func runBackgroundTask(_ run: any BackgroundTaskRun, drain: () -> Task<Void, Never>?) {
        // The request this run answers is no longer waiting.
        isBackgroundTaskAsked = false
        let completion = BackgroundRunCompletion(run)
        let draining = drain()
        run.onExpiry { [weak self] in
            // iOS called it as the run completed, and it landed after.
            guard !completion.isCompleted else { return }
            self?.log.notice("out of time for the background task while sending kept reports; the rest wait for its next run")
            if let self, waiting.count > 0 { askForBackgroundTask() }
            // At once, not as the drain stops: a drain waiting on a token
            // refresh stops only once the refresh has its answer, and iOS may
            // kill an app that hasn't completed the run by then.
            completion.complete(success: false)
            // This run's drain only, not one started since. Its upload out
            // fails before its batch is removed, so the batch stays kept. A
            // refresh it waits on is left to finish (`TokenRefresher` says
            // why) under the drain's own background time, which the drain
            // ends as it stops.
            draining?.cancel()
        }
        Task { [weak self] in
            await draining?.value
            completion.complete(success: self?.waiting.count == 0)
        }
    }

    /// Asks iOS to run the background task once there is a network
    /// connection.
    private func askForBackgroundTask() {
        guard !isBackgroundTaskAsked else { return }
        isBackgroundTaskAsked = true
        let request = BGProcessingTaskRequest(identifier: Self.backgroundTaskIdentifier)
        request.requiresNetworkConnectivity = true
        do {
            try backgroundTasks.submit(request)
            log.notice("asked for the background task that sends kept reports once there is a connection")
        } catch {
            log.warning("could not ask for the background task that sends kept reports: \(String(describing: error), privacy: .public)")
        }
    }

    /// Drops the reports kept for any other driver or server than the one
    /// signed in: only the sign-in a report was kept under can send it, and
    /// this one has replaced theirs. Android's rule at sign-in. A sign-out
    /// keeps them, so a driver who signs out and back in still sends theirs.
    /// A file that can't be removed is left: it is kept under its own owner,
    /// so it can never go as this driver's.
    func dropReportsKeptForOthers() async {
        guard let owner else { return }
        do {
            try await queue.removeAll(except: owner)
        } catch {
            log.error("could not drop the reports kept for another sign-in: \(String(describing: error), privacy: .public)")
        }
    }

    private func uploadOneBatch(through api: any TrackerAPI) async -> HistoryUpload {
        guard let owner else { return .notSignedIn }
        do {
            // The whole queue, as more reports than a batch holds can be too
            // old, and none of those is sent.
            var queued = try await queue.oldest(.max, for: owner)
            let seconds = Int64(now().timeIntervalSince1970)
            let expired = queued.prefix { Self.isTooOld($0.timestamp, at: seconds) }
            if !expired.isEmpty {
                log.notice("dropping \(expired.count) kept reports older than the server takes")
                try await queue.remove(Array(expired), for: owner)
                await countWaiting()
                queued = try await queue.oldest(Self.batchSize, for: owner)
            }
            // The endpoint takes one vehicle per upload.
            guard let vehicleID = queued.first?.vehicleID else { return .nothingQueued }
            let reports = Array(queued.prefix(Self.batchSize).prefix { $0.vehicleID == vehicleID })
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
            await countWaiting()
            return .batchSent
        } catch {
            // The upload's own failures are handled above; this is the
            // queue's file failing.
            log.error("could not read or update the kept reports: \(String(describing: error), privacy: .public)")
            return .kept
        }
    }

    /// Whether a report taken at `timestamp` is too old to upload at `now`,
    /// both in whole seconds, as a report's timestamp is. The uploads and the
    /// count both go by it, so they agree.
    private static func isTooOld(_ timestamp: Int64, at now: Int64) -> Bool {
        now - timestamp > maxAgeSeconds
    }
}

/// A drain's background time, ended once: as the drain stops, or as iOS
/// takes it back, whichever comes first. Each drain has its own, so a
/// stopped drain running on beside a new one (`drain(through:)` says when)
/// can't end the new one's time as it stops, and iOS taking back the old
/// one's time stops only the old one.
private final class DrainTime {
    private let backgroundTime: any BackgroundTime
    private var time = UIBackgroundTaskIdentifier.invalid

    init(_ backgroundTime: any BackgroundTime) {
        self.backgroundTime = backgroundTime
    }

    /// `expired` stops the drain as iOS takes the time back, and the time is
    /// ended then, as iOS requires.
    func begin(expired: @escaping @MainActor () -> Void) {
        time = backgroundTime.begin("Send kept reports") { [weak self] in
            expired()
            self?.end()
        }
    }

    func end() {
        guard time != .invalid else { return }
        backgroundTime.end(time)
        time = .invalid
    }
}

/// A run of the background task, completed once: as the drain it waits for
/// stops, or as iOS ends the run, whichever comes first.
private final class BackgroundRunCompletion {
    private let run: any BackgroundTaskRun
    private(set) var isCompleted = false

    init(_ run: any BackgroundTaskRun) {
        self.run = run
    }

    func complete(success: Bool) {
        guard !isCompleted else { return }
        isCompleted = true
        run.complete(success: success)
    }
}
