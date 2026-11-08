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
@MainActor final class TripActivityController {
    private let session: TripSession
    private let host: any TripActivityHost
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "live-activity")

    /// The activity showing, and the trip it is for.
    private var current: (id: String, serverTripID: Int64)?
    /// The state last handed to the host, whether or not it has landed yet.
    private var sent: TripActivityAttributes.ContentState?
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

    init(session: TripSession, host: any TripActivityHost) {
        self.session = session
        self.host = host
        adoptOrEnd()
        observe()
        sync()
    }

    isolated deinit {
        endings?.cancel()
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
            let id = try host.request(TripActivityAttributes(trip), state)
            current = (id, trip.serverTripID)
            sent = state
            watchEndings(id)
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
        pending = state
        guard updating == nil else { return }
        updating = Task { [weak self] in await self?.drain() }
    }

    private func drain() async {
        while let state = pending, let current {
            pending = nil
            sent = state
            await host.update(id: current.id, state)
        }
        updating = nil
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
        current = nil
        sent = nil
        pending = nil
    }

    private func end(_ id: String) {
        let host = host
        Task { await host.end(id: id) }
    }
}
