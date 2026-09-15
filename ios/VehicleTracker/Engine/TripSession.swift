import Foundation
import Observation
import OSLog
import VehiclePositionsKit

/// Errors this session raises itself, as opposed to ones the server sent
/// (``APIError``).
nonisolated enum TripSessionError: Error, Equatable, LocalizedError {
    /// A call that needs the server was made while signed out.
    case notSignedIn
    /// The fetched trip has no usable shape or no stops: nothing to judge
    /// adherence against, so it is refused before anything is started.
    case unusableTrip
    /// `start()` was called while a trip was already starting or running.
    case busy

    var errorDescription: String? {
        switch self {
        case .notSignedIn: "Sign in first."
        case .unusableTrip: "This run has no shape or stops to follow."
        case .busy: "A trip is already starting or running."
        }
    }
}

/// The one source of truth for the driver's trip (spec §5.3): sign-in state,
/// the active trip, the location stream, adherence, and reporting status. The
/// phone screens and the CarPlay scene both observe it and nothing else.
@MainActor @Observable
final class TripSession {
    enum Phase: Equatable {
        case signedOut
        case idle
        case starting
        case active(ActiveTrip)
        case paused(ActiveTrip)
        case ending(ActiveTrip)

        /// A start is in flight, on the phone or from the car; the run list
        /// on either must not offer another until the server answers.
        var isStarting: Bool {
            if case .starting = self { return true }
            return false
        }

        /// The trip is being ended: both screens spend the End button on the
        /// first tap and say so until the server answers.
        var isEnding: Bool {
            if case .ending = self { return true }
            return false
        }
    }

    enum ReportingStatus: Equatable {
        /// Reporting normally. How many fixes have been accepted is
        /// `fixesSent`, which outlives a problem rather than being replaced
        /// by it, so it is not carried here.
        case connected
        case noNetwork
        case noGPS
        case authExpired
        case clockSkew
        /// Core Location refused a background session because the app was not
        /// in the foreground; the driver must open the app on the phone.
        case needsForeground
        /// The location stream itself ended (thrown or finished) while a trip
        /// was active. Distinct from `needsForeground`: the app does not know
        /// why, only that it must be resumed from the foreground.
        case locationLost
    }

    private(set) var phase: Phase
    private(set) var latest: Adherence?
    private(set) var reporting: ReportingStatus = .connected
    /// Fixes accepted by the server on this trip. The banner carries the
    /// status, so the footer's counter must survive a problem rather than be
    /// replaced by it.
    private(set) var fixesSent = 0
    private(set) var vehicles: [Vehicle] = []
    let settings: AppSettings

    private let tokens: any TokenStoring
    private let store: any ActiveTripStoring
    private let locations: any LocationSource
    private let makeAPI: (URL, any TokenStoring) throws -> any TrackerAPI
    private let now: () -> Date
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "trip-session")

    private var api: (any TrackerAPI)?
    private var reporter: LocationReporter?
    private var evaluator: AdherenceEvaluator?
    private var streamTask: Task<Void, Never>?
    /// Bumped by every `beginTracking`, so a superseded stream task that
    /// notices its cancellation afterwards cannot report the new stream lost.
    private var streamGeneration = 0
    private var backgroundHandle: (any BackgroundActivityHandle)?
    private var gpsAvailable = true
    private var needsForeground = false
    /// Set when the location stream itself ends while a trip is active; only
    /// `resume()` or a fresh `start()` clears it.
    private var streamEnded = false

    init(
        settings: AppSettings,
        tokens: any TokenStoring,
        store: any ActiveTripStoring,
        locations: any LocationSource,
        makeAPI: @escaping (URL, any TokenStoring) throws -> any TrackerAPI,
        now: @escaping () -> Date = Date.init
    ) {
        self.settings = settings
        self.tokens = tokens
        self.store = store
        self.locations = locations
        self.makeAPI = makeAPI
        self.now = now

        // A fresh token and a known server mean the driver stays signed in; a
        // stored trip on top of that is one iOS interrupted, shown paused until
        // the driver resumes it from the foreground.
        if let token = try? tokens.load(), token.isFresh(now: now()),
           let url = settings.serverURL, let api = try? makeAPI(url, tokens) {
            self.api = api
            phase = .idle
            adoptStoredTrip()
        } else {
            phase = .signedOut
        }
    }

    /// Takes up the stored trip as `.paused`, but only if this driver is the
    /// one who started it: phones are shared between shifts, and reporting
    /// another driver's run under this account would put the wrong bus on the
    /// map. A record belonging to anyone else is dropped.
    private func adoptStoredTrip() {
        guard let active = try? store.load() else { return }
        if active.driverEmail.caseInsensitiveCompare(settings.email) == .orderedSame {
            phase = .paused(active)
        } else {
            log.notice("dropping a stored trip started by another driver")
            try? store.clear()
        }
    }

    var activeTrip: ActiveTrip? {
        switch phase {
        case .active(let t), .paused(let t), .ending(let t): t
        default: nil
        }
    }

    // MARK: Sign-in

    func signIn(serverURL: URL, email: String, password: String) async throws {
        let api = try makeAPI(serverURL, tokens)
        let token = try await api.login(email: email, password: password)
        try tokens.save(StoredToken(token: token, issuedAt: now()))
        settings.serverURLString = serverURL.absoluteString
        settings.email = email
        self.api = api
        if case .signedOut = phase {
            phase = .idle
            adoptStoredTrip()
        }
    }

    /// Signs in again with the stored email, keeping any running trip.
    func reauthenticate(password: String) async throws {
        guard let url = settings.serverURL else { throw TripSessionError.notSignedIn }
        try await signIn(serverURL: url, email: settings.email, password: password)
        // A fresh token clears an auth problem at once; the token closure
        // passed by the app container reads the store on every request, so
        // the token itself takes effect on the next report without further
        // work here.
        reporter?.clearAuthProblem()
        refreshReporting()
    }

    /// Signs out of the account. Works from `.idle` and `.paused`: a paused
    /// trip's record is left in the store so the next sign-in returns to it.
    func signOut() {
        switch phase {
        case .idle, .paused:
            try? tokens.clear()
            api = nil
            vehicles = []
            phase = .signedOut
        default:
            return
        }
    }

    // MARK: Catalog

    func loadVehicles() async throws {
        vehicles = try await requireAPI().myVehicles()
    }

    func routes() async throws -> [RouteInfo] {
        try await requireAPI().routes()
    }

    func trips(routeID: String) async throws -> RouteTripsPage {
        try await requireAPI().trips(routeID: routeID)
    }

    // MARK: Trip lifecycle

    func start(vehicle: Vehicle, tripID: String) async throws {
        guard case .idle = phase else { throw TripSessionError.busy }
        let api = try requireAPI()
        phase = .starting
        do {
            let trip = try await api.trip(id: tripID)
            // Validate the geometry before telling the server anything: if
            // there is nothing to judge adherence against, nothing should be
            // started, so a retry does not run into "trip already active".
            guard let evaluator = AdherenceEvaluator(trip: trip) else {
                throw TripSessionError.unusableTrip
            }
            let serverID = try await api.startTrip(vehicleID: vehicle.id, routeID: trip.routeID, gtfsTripID: trip.id)
            let active = ActiveTrip(serverTripID: serverID, vehicle: vehicle, trip: trip,
                                    startedAt: now(), driverEmail: settings.email)
            do {
                try store.save(active)
            } catch {
                // The server now believes this trip is running but the phone
                // could not persist it; end it best-effort so the driver is
                // not locked out of starting again.
                try? await api.endTrip(id: serverID)
                throw error
            }
            settings.rememberRoute(trip.routeID)
            settings.lastVehicleID = vehicle.id
            try beginTracking(active, evaluator: evaluator)
        } catch {
            phase = .idle
            throw error
        }
    }

    /// Restarts location delivery: for a trip a relaunch found in the store
    /// (`.paused`), or after the location stream itself ended (`.active`
    /// with `streamEnded`). Neither case makes a server call. Must be called
    /// from the foreground, as any (re)start must.
    func resume() {
        switch phase {
        case .paused(let active):
            guard let evaluator = AdherenceEvaluator(trip: active.trip) else {
                // A trip we cannot judge adherence for is one we cannot
                // resume; end it locally rather than leaving a dead entry in
                // the store that would just fail the same way every launch.
                try? store.clear()
                phase = .idle
                return
            }
            resume(active, evaluator: evaluator)
        case .active(let active) where streamEnded:
            guard let evaluator = AdherenceEvaluator(trip: active.trip) else { return }
            resume(active, evaluator: evaluator)
        default:
            return
        }
    }

    /// Resumes tracking, or — if the account has gone in the meantime — says
    /// so: without a server there is nothing to report to, and signed out is
    /// the truthful state to show rather than a trip that silently sends
    /// nothing.
    private func resume(_ active: ActiveTrip, evaluator: AdherenceEvaluator) {
        do {
            try beginTracking(active, evaluator: evaluator)
        } catch {
            log.error("cannot resume without a signed-in account: \(String(describing: error))")
            phase = .signedOut
        }
    }

    /// Ends the trip on the server, then stops tracking. Proceeds only from
    /// `.active`/`.paused`; a second call while one is already `.ending` (or
    /// any other phase) is a no-op, so `end()` is never sent twice at once.
    /// If the server cannot be reached the trip stays active and the error is
    /// thrown; the driver can retry or `endLocally()`. If something else
    /// (namely `endLocally()`) already moved the phase on while the request
    /// was in flight, that change is left alone rather than resurrected.
    func end() async throws {
        let before: Phase
        switch phase {
        case .active, .paused:
            before = phase
        default:
            return
        }
        guard let active = activeTrip, let api else { return }
        phase = .ending(active)
        do {
            try await api.endTrip(id: active.serverTripID)
        } catch {
            if phase == .ending(active) {
                phase = before
            }
            throw error
        }
        // `endLocally()` may have torn this trip down while the request was in
        // flight, and a new one could even have been started since; only the
        // phase this call set is ours to end.
        guard phase == .ending(active) else { return }
        stopTracking()
    }

    /// Stops tracking without telling the server (it will reap the trip).
    func endLocally() {
        guard activeTrip != nil else { return }
        stopTracking()
    }

    // MARK: Tracking

    private func beginTracking(_ active: ActiveTrip, evaluator: AdherenceEvaluator) throws {
        guard let api else { throw TripSessionError.notSignedIn }
        self.evaluator = evaluator
        reporter = LocationReporter(api: api, now: now) { [weak self] in
            // A send finishes long after the fix that started it was handed
            // over, so its outcome has to be published when it lands.
            self?.refreshReporting()
        }
        gpsAvailable = true
        needsForeground = false
        streamEnded = false
        // Resuming a trip whose stream died leaves the previous session open;
        // Core Location keeps it alive until it is invalidated.
        backgroundHandle?.invalidate()
        backgroundHandle = locations.beginBackgroundActivity()
        // A second subscription must not run alongside the first: the old one
        // would keep feeding this session fixes it has already moved past.
        streamTask?.cancel()
        streamGeneration += 1
        let generation = streamGeneration
        let stream = locations.updates()
        streamTask = Task { [weak self] in
            do {
                for try await sample in stream {
                    guard let self else { return }
                    self.handle(sample, active: active)
                }
                self?.markStreamEnded(active, generation: generation)
            } catch {
                self?.markStreamEnded(active, generation: generation)
            }
        }
        phase = .active(active)
        refreshReporting()
    }

    private func stopTracking() {
        streamTask?.cancel()
        streamTask = nil
        backgroundHandle?.invalidate()
        backgroundHandle = nil
        reporter = nil
        evaluator = nil
        latest = nil
        gpsAvailable = true
        needsForeground = false
        streamEnded = false
        do {
            try store.clear()
        } catch {
            log.error("failed to clear the stored active trip: \(String(describing: error))")
        }
        phase = .idle
        refreshReporting()
    }

    private func handle(_ sample: LocationSample, active: ActiveTrip) {
        // A sample whose fix is too coarse to place the bus on a street is no
        // position at all (spec §8): the map holds the last good one and the
        // driver is told the GPS is unusable, rather than being shown — and
        // riders being sent — a guess that could be a block out.
        var dropsFix = false
        switch sample.diagnostic {
        case .authorizationDenied, .locationUnavailable:
            gpsAvailable = false
        case .insufficientlyInUse:
            needsForeground = true
        case .accuracyLimited:
            gpsAvailable = false
            dropsFix = true
        case nil:
            break
        }
        if !dropsFix, let fix = sample.fix, let evaluator, let reporter {
            gpsAvailable = true
            needsForeground = false
            latest = evaluator.evaluate(fix, previous: latest)
            // Not awaited: the send runs on its own so the fixes behind it
            // keep reaching the map (spec §8).
            reporter.report(fix, vehicleID: active.vehicle.id, gtfsTripID: active.trip.id)
        }
        refreshReporting()
    }

    /// The stream ended (thrown or finished) while this trip was still the
    /// one in hand. The phase does not matter: a stream that dies during
    /// `.ending` must still show as lost if that end then fails and the phase
    /// is restored. A trip that is already gone, or a stream a later
    /// `beginTracking` has already replaced, is ignored.
    private func markStreamEnded(_ active: ActiveTrip, generation: Int) {
        guard generation == streamGeneration, activeTrip == active else { return }
        streamEnded = true
        needsForeground = false
        refreshReporting()
    }

    private func refreshReporting() {
        if let reporter { fixesSent = reporter.fixesSent }
        if streamEnded {
            reporting = .locationLost
        } else if needsForeground {
            reporting = .needsForeground
        } else if !gpsAvailable {
            reporting = .noGPS
        } else {
            switch reporter?.problem ?? .none {
            case .none: reporting = .connected
            case .noNetwork: reporting = .noNetwork
            case .authExpired: reporting = .authExpired
            case .clockSkew: reporting = .clockSkew
            }
        }
    }

    private func requireAPI() throws -> any TrackerAPI {
        guard let api else { throw TripSessionError.notSignedIn }
        return api
    }
}
