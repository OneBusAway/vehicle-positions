import Foundation
import Observation
import VehiclePositionsKit

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
    }

    enum ReportingStatus: Equatable {
        case connected(fixesSent: Int)
        case noNetwork
        case noGPS
        case authExpired
        case clockSkew
        /// Core Location refused a background session because the app was not
        /// in the foreground; the driver must open the app on the phone.
        case needsForeground
    }

    private(set) var phase: Phase
    private(set) var latest: Adherence?
    private(set) var reporting: ReportingStatus = .connected(fixesSent: 0)
    private(set) var vehicles: [Vehicle] = []
    let settings: AppSettings

    private let tokens: any TokenStoring
    private let store: any ActiveTripStoring
    private let locations: any LocationSource
    private let makeAPI: (URL, any TokenStoring) throws -> any TrackerAPI
    private let now: () -> Date

    private var api: (any TrackerAPI)?
    private var reporter: LocationReporter?
    private var evaluator: AdherenceEvaluator?
    private var streamTask: Task<Void, Never>?
    private var backgroundHandle: (any BackgroundActivityHandle)?
    private var gpsAvailable = true
    private var needsForeground = false

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
            if let active = try? store.load() {
                phase = .paused(active)
            } else {
                phase = .idle
            }
        } else {
            phase = .signedOut
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
            phase = (try? store.load()).map(Phase.paused) ?? .idle
        }
    }

    /// Signs in again with the stored email, keeping any running trip.
    func reauthenticate(password: String) async throws {
        guard let url = settings.serverURL else { throw APIError.insecureURL }
        try await signIn(serverURL: url, email: settings.email, password: password)
        // The token closure passed by the app container reads the store on
        // every request, so the new token takes effect on the next report
        // without further work here.
    }

    func signOut() {
        guard case .idle = phase else { return }
        try? tokens.clear()
        api = nil
        vehicles = []
        phase = .signedOut
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
        guard case .idle = phase else { return }
        let api = try requireAPI()
        phase = .starting
        do {
            let trip = try await api.trip(id: tripID)
            let serverID = try await api.startTrip(vehicleID: vehicle.id, routeID: trip.routeID, gtfsTripID: trip.id)
            let active = ActiveTrip(serverTripID: serverID, vehicle: vehicle, trip: trip, startedAt: now())
            try store.save(active)
            settings.rememberRoute(trip.routeID)
            settings.lastVehicleID = vehicle.id
            beginTracking(active)
        } catch {
            phase = .idle
            throw error
        }
    }

    /// Restarts location delivery for a trip a relaunch found in the store.
    /// Must be called from the foreground, as any start must.
    func resume() {
        guard case .paused(let active) = phase else { return }
        beginTracking(active)
    }

    /// Ends the trip on the server, then stops tracking. If the server cannot
    /// be reached the trip stays active and the error is thrown; the driver
    /// can retry or `endLocally()`.
    func end() async throws {
        guard let active = activeTrip, let api else { return }
        let before = phase
        phase = .ending(active)
        do {
            try await api.endTrip(id: active.serverTripID)
        } catch {
            phase = before
            throw error
        }
        stopTracking()
    }

    /// Stops tracking without telling the server (it will reap the trip).
    func endLocally() {
        guard activeTrip != nil else { return }
        stopTracking()
    }

    // MARK: Tracking

    private func beginTracking(_ active: ActiveTrip) {
        guard let api, let evaluator = AdherenceEvaluator(trip: active.trip) else {
            phase = .idle
            return
        }
        self.evaluator = evaluator
        reporter = LocationReporter(api: api, now: now)
        gpsAvailable = true
        needsForeground = false
        backgroundHandle = locations.beginBackgroundActivity()
        let stream = locations.updates()
        streamTask = Task { [weak self] in
            do {
                for try await sample in stream {
                    guard let self else { return }
                    await self.handle(sample, active: active)
                }
            } catch {
                self?.gpsAvailable = false
                self?.refreshReporting()
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
        try? store.clear()
        phase = .idle
        refreshReporting()
    }

    private func handle(_ sample: LocationSample, active: ActiveTrip) async {
        switch sample.diagnostic {
        case .authorizationDenied, .locationUnavailable:
            gpsAvailable = false
        case .insufficientlyInUse:
            needsForeground = true
        case .accuracyLimited, nil:
            break
        }
        if let fix = sample.fix, let evaluator, let reporter {
            gpsAvailable = true
            needsForeground = false
            latest = evaluator.evaluate(fix, previous: latest)
            _ = await reporter.report(fix, vehicleID: active.vehicle.id, gtfsTripID: active.trip.id)
        }
        refreshReporting()
    }

    private func refreshReporting() {
        if needsForeground {
            reporting = .needsForeground
        } else if !gpsAvailable {
            reporting = .noGPS
        } else {
            switch reporter?.problem ?? .none {
            case .none: reporting = .connected(fixesSent: reporter?.fixesSent ?? 0)
            case .noNetwork: reporting = .noNetwork
            case .authExpired: reporting = .authExpired
            case .clockSkew: reporting = .clockSkew
            }
        }
    }

    private func requireAPI() throws -> any TrackerAPI {
        guard let api else { throw APIError.status(401, message: "signed out") }
        return api
    }
}
