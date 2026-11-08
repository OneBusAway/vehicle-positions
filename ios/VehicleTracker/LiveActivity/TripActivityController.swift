import Foundation
import Observation
import OSLog

/// Keeps the running trip's Live Activity in step with the session. It starts
/// one when the trip becomes active, updates it only when its words change,
/// and ends it at once when there is no trip any more (HIG: end a Live
/// Activity "immediately when the task or event ends"). A paused trip and one
/// whose end is being sent keep theirs. App-scoped, in `AppContainer`.
///
/// The driver's choice wins: with Live Activities off nothing is asked, and
/// an activity they swiped away, or the system ended, is not started again
/// for that trip.
///
/// Updates are coalesced as in OneBusAway iOS's `LiveActivityUpdateCoalescer`:
/// one is in flight at a time, and only the newest state waiting goes next.
///
/// The app is the activity's only updater, so every update is marked stale
/// `staleAfter` later, and while nothing changes a heartbeat sends the same
/// state again a `heartbeat` after the last send. If iOS suspends or kills the
/// app, the heartbeat stops and ActivityKit shows the stale look by itself.
@MainActor final class TripActivityController {
    /// Well inside the server's five-minute `STALENESS_THRESHOLD`, so the
    /// driver sees the warning before riders lose the vehicle.
    static let staleAfter: TimeInterval = 120
    static let heartbeat: TimeInterval = 60

    private let session: TripSession
    private let host: any TripActivityHost
    private let now: () -> Date
    private let sleep: (Duration) async throws -> Void
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "live-activity")

    /// The activity showing, and the trip it is for.
    private var current: (id: String, serverTripID: Int64)?
    /// The state last handed to the host, whether or not it has landed yet,
    /// and when.
    private var sent: TripActivityAttributes.ContentState?
    private var sentAt = Date.distantPast
    /// The newest state waiting for the update in flight.
    private var pending: TripActivityAttributes.ContentState?
    private var updating: Task<Void, Never>?
    /// A trip whose activity is not started again: the driver swiped it
    /// away, the system ended it, or its start failed.
    private var declinedTripID: Int64?
    /// A start waits for the phone app to come to the front: ActivityKit
    /// refused it from the background, or Live Activities were off.
    private var waitsForForeground = false
    private var endings: Task<Void, Never>?
    private var heartbeats: Task<Void, Never>?

    init(session: TripSession, host: any TripActivityHost, now: @escaping () -> Date = Date.init,
         sleep: @escaping (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.session = session
        self.host = host
        self.now = now
        self.sleep = sleep
        adoptOrEnd()
        observe()
        sync()
    }

    isolated deinit {
        endings?.cancel()
        heartbeats?.cancel()
    }

    /// The phone app's window came to the front: a start that was waiting
    /// for it is tried again, once.
    func sceneBecameActive() {
        guard waitsForForeground else { return }
        waitsForForeground = false
        sync()
    }

    /// After a relaunch the stored trip keeps its live activity, and any
    /// other is ended (Apple: end any Live Activity that's no longer
    /// relevant). One the driver swiped away or the system ended stays gone.
    private func adoptOrEnd() {
        let tripID = session.activeTrip?.serverTripID
        for activity in host.existing() {
            if activity.isLive {
                if activity.serverTripID == tripID, current == nil {
                    current = (activity.id, activity.serverTripID)
                    watchEndings(activity.id)
                    startHeartbeat()
                } else {
                    end(activity.id)
                }
            } else if activity.serverTripID == tripID {
                declinedTripID = tripID
            }
        }
    }

    /// `withObservationTracking` fires once; re-arm after every change, as
    /// `CarPlayController` does.
    private func observe() {
        withObservationTracking {
            _ = session.phase
            _ = session.latest
            _ = session.reporting
        } onChange: { [weak self] in
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.sync()
                self.observe()
            }
        }
    }

    private func sync() {
        guard let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) else {
            endCurrent()
            waitsForForeground = false
            return
        }
        let state = TripActivityAttributes.ContentState(status: status, latest: session.latest)
        guard let current, current.serverTripID == status.active.serverTripID else {
            endCurrent()
            if case .active = session.phase { start(status.active, state) }
            return
        }
        schedule(state)
    }

    private func start(_ trip: ActiveTrip, _ state: TripActivityAttributes.ContentState) {
        guard trip.serverTripID != declinedTripID, !waitsForForeground else { return }
        guard host.areActivitiesEnabled else {
            // Nothing is asked. Turning them on takes Settings, so the
            // driver's next return to the app tries again.
            log.notice("Live Activities are off; trip \(trip.serverTripID, privacy: .public) has none until the app is next in front")
            waitsForForeground = true
            return
        }
        do {
            let id = try host.request(TripActivityAttributes(trip), state, staleDate: now().addingTimeInterval(Self.staleAfter))
            current = (id, trip.serverTripID)
            sent = state
            sentAt = now()
            watchEndings(id)
            startHeartbeat()
        } catch TripActivityError.notInForeground {
            log.notice("the Live Activity for trip \(trip.serverTripID, privacy: .public) waits for the app to come to the front")
            waitsForForeground = true
        } catch {
            // Asking again with every fix would fail the same way; the next
            // trip asks afresh.
            declinedTripID = trip.serverTripID
            log.error("could not start the Live Activity for trip \(trip.serverTripID, privacy: .public): \(String(describing: error), privacy: .public)")
        }
    }

    /// Sends `state` unless it is what the activity already shows, or is on
    /// its way to.
    private func schedule(_ state: TripActivityAttributes.ContentState) {
        guard state != sent else {
            pending = nil
            return
        }
        enqueue(state)
    }

    private func enqueue(_ state: TripActivityAttributes.ContentState) {
        pending = state
        guard updating == nil else { return }
        updating = Task { [weak self] in await self?.drain() }
    }

    private func drain() async {
        while let state = pending, let current {
            pending = nil
            sent = state
            sentAt = now()
            await host.update(id: current.id, state, staleDate: sentAt.addingTimeInterval(Self.staleAfter))
        }
        updating = nil
    }

    private func startHeartbeat() {
        heartbeats?.cancel()
        let sleep = sleep
        heartbeats = Task { [weak self] in
            while let wait = self?.untilNextBeat() {
                do {
                    try await sleep(.seconds(wait))
                } catch {
                    return
                }
                await self?.beat()
            }
        }
    }

    /// Seconds until a `heartbeat` after the last send, and at least one.
    private func untilNextBeat() -> TimeInterval {
        max(sentAt.addingTimeInterval(Self.heartbeat).timeIntervalSince(now()), 1)
    }

    /// Nothing has gone out for a `heartbeat`: the same state again, with a
    /// fresh stale date. Returns once it has gone.
    private func beat() async {
        guard let sent, updating == nil, now() >= sentAt.addingTimeInterval(Self.heartbeat) else { return }
        enqueue(sent)
        await updating?.value
    }

    private func watchEndings(_ id: String) {
        let stream = host.endings(of: id)
        endings?.cancel()
        endings = Task { [weak self] in
            for await _ in stream {
                self?.endedElsewhere(id)
                return
            }
        }
    }

    /// Swiped away by the driver, or ended by the system (after eight
    /// hours): not started again for this trip.
    private func endedElsewhere(_ id: String) {
        guard let current, current.id == id else { return }
        declinedTripID = current.serverTripID
        forget()
    }

    private func endCurrent() {
        guard let current else { return }
        forget()
        end(current.id)
    }

    private func forget() {
        endings?.cancel()
        endings = nil
        heartbeats?.cancel()
        heartbeats = nil
        current = nil
        sent = nil
        pending = nil
    }

    private func end(_ id: String) {
        let host = host
        Task { await host.end(id: id) }
    }
}
