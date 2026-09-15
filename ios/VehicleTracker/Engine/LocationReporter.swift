import Foundation
import Observation
import OSLog
import VehiclePositionsKit

/// A send-side problem the driver should see (spec §5.4). GPS availability is
/// the session's to judge; this is only about reaching the server.
nonisolated enum ReportingProblem: Sendable, Equatable {
    case none, noNetwork, authExpired, clockSkew
}

/// Sends fixes to `POST /api/v1/locations`, one at a time, no more often than
/// the server accepts, dropping what it cannot send. A copy of the Android
/// app's `TripReporter` rules so both clients behave the same.
@MainActor @Observable
final class LocationReporter {
    /// The server allows one report per five seconds per driver.
    static let minimumInterval: TimeInterval = 5
    /// Consecutive timestamp rejections before the clock is blamed.
    static let clockSkewThreshold = 3

    private(set) var fixesSent = 0
    private(set) var problem: ReportingProblem = .none

    private let api: any TrackerAPI
    private let now: () -> Date
    /// Called after every send outcome, so the session can republish its
    /// reporting status without having waited for the POST.
    private let onChange: @MainActor () -> Void
    private var lastSentAt: Date?
    private var consecutiveTimestampRejects = 0
    /// True from the moment a send is handed to its task until that task has
    /// recorded its outcome. Only one report is ever in flight.
    private var isSending = false
    private var sendTask: Task<Void, Never>?
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "reporter")

    init(api: any TrackerAPI, now: @escaping () -> Date, onChange: @escaping @MainActor () -> Void = {}) {
        self.api = api
        self.now = now
        self.onChange = onChange
    }

    /// Hands the fix to a send that runs on its own, so a slow POST never
    /// holds up the fixes behind it (spec §8). Returns whether a send was
    /// started: `false` when one went out less than five seconds ago, or when
    /// one is still in flight. The outcome lands in `problem` and `fixesSent`
    /// later, and `onChange` fires when it does.
    @discardableResult
    func report(_ fix: LocationFix, vehicleID: String, gtfsTripID: String) -> Bool {
        let at = now()
        if let last = lastSentAt, at.timeIntervalSince(last) < Self.minimumInterval {
            return false
        }
        // A send still in flight means the network, not the throttle, is the
        // limit; queueing behind it would only pile up stale positions.
        if isSending {
            return false
        }
        lastSentAt = at

        let report = LocationReport(
            vehicleID: vehicleID, tripID: gtfsTripID,
            latitude: fix.latitude, longitude: fix.longitude,
            bearing: (0...360).contains(fix.course) ? fix.course : nil,
            speed: max(fix.speed, 0),
            accuracy: fix.horizontalAccuracy >= 0 ? fix.horizontalAccuracy : nil,
            timestamp: Int64(fix.timestamp.timeIntervalSince1970)
        )
        isSending = true
        sendTask = Task { [weak self] in
            await self?.send(report)
        }
        return true
    }

    private func send(_ report: LocationReport) async {
        defer {
            isSending = false
            onChange()
        }
        do {
            try await api.postLocation(report)
            fixesSent += 1
            consecutiveTimestampRejects = 0
            problem = .none
        } catch let APIError.status(code, message) where code == 400 && message.contains("timestamp") {
            // Only a run of consecutive timestamp rejections blames the
            // clock; anything else in between (success or another error)
            // starts the count over.
            consecutiveTimestampRejects += 1
            if consecutiveTimestampRejects >= Self.clockSkewThreshold {
                problem = .clockSkew
            }
        } catch let APIError.status(code, message) {
            consecutiveTimestampRejects = 0
            switch code {
            case 401:
                problem = .authExpired
            case 429:
                break // rate-limited: drop silently, keep the current status
            default:
                log.warning("dropping location report after HTTP \(code): \(message)")
            }
        } catch {
            consecutiveTimestampRejects = 0
            log.warning("dropping location report after transport failure: \(String(describing: error))")
            problem = .noNetwork
        }
    }

    /// Awaits the send currently in flight, if any. For tests: nothing in the
    /// app waits on a report.
    func waitForInFlightSend() async {
        await sendTask?.value
    }

    /// Clears an auth problem the instant the driver signs in again, rather
    /// than waiting for the next accepted report to notice.
    func clearAuthProblem() {
        if problem == .authExpired {
            problem = .none
        }
    }
}
