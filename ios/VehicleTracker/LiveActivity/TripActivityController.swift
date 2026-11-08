import Foundation
import Observation
import OSLog

/// Keeps the running trip's Live Activity in step with the session. It starts
/// one when the trip becomes active, updates it only when its words change,
/// and ends it at once when there is no trip any more (HIG: end a Live
/// Activity "immediately when the task or event ends"). A paused trip and one
/// whose end is being sent keep theirs. App-scoped, in `AppContainer`.
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
    /// A trip whose activity is not started again: its start failed.
    private var declinedTripID: Int64?

    init(session: TripSession, host: any TripActivityHost) {
        self.session = session
        self.host = host
        observe()
        sync()
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
        guard trip.serverTripID != declinedTripID, host.areActivitiesEnabled else { return }
        do {
            current = (try host.request(TripActivityAttributes(trip), state), trip.serverTripID)
            sent = state
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

    private func endCurrent() {
        guard let current else { return }
        self.current = nil
        sent = nil
        pending = nil
        let host = host
        Task { await host.end(id: current.id) }
    }
}
