import CarPlay
import CoreLocation
import Observation
import UIKit

/// Everything the car screen shows, driven by `TripSession` (spec §6). One
/// per CarPlay connection; it owns the map template, the map view controller
/// in the CarPlay window, and whatever navigation session is running.
@MainActor
final class CarPlayController: NSObject, CPMapTemplateDelegate {
    let session: TripSession
    let interface: CPInterfaceController
    let window: CPWindow
    let map = RouteMapViewController()
    let mapTemplate = CPMapTemplate()

    private var navigation: CPNavigationSession?
    private var navigationTripID: String?
    private var trip: CPTrip?
    /// One reusable maneuver per stop sequence. CarPlay tracks maneuvers by
    /// identity, so a new deviation edits the card the driver is reading
    /// instead of replacing it.
    private var maneuversByStop: [Int: CPManeuver] = [:]
    /// The stop sequences currently in `upcomingManeuvers`, and every sequence
    /// already handed to the session: maneuvers may only be added once, in
    /// chronological order.
    private var upcomingSequences: [Int] = []
    private var addedSequences: Set<Int> = []
    /// The banner we presented and the subtitle it is showing: the alert so a
    /// dismissal of somebody else's banner is not mistaken for the driver
    /// dismissing ours, the subtitle so a standing banner is only rewritten
    /// when the distance on it has actually changed. Nil means none of ours
    /// is up.
    private var offRouteBanner: (alert: CPNavigationAlert, subtitle: String)?
    /// Whether this off-route stretch has already had its banner. The driver
    /// dismissing one must not bring it straight back on the next fix: the
    /// vehicle has to be back on route before another may be presented.
    private var offRouteEpisodeSeen = false
    private var details: CPInformationTemplate?
    /// The six detail strings the information template is showing, so its
    /// items are only rebuilt when one of them changes.
    private var lastDetailItems: [String]?
    private var isPanning = false
    private var lastPanTranslation = CGPoint.zero
    /// Set for the span of a `session.start` call, so a second tap on the
    /// alert's Start button — or on an earlier list, still on screen behind
    /// it — cannot fire a second start while the first is in flight.
    private var startInFlight = false
    /// Cleared when the car goes away. Anything resumed after an `await` has
    /// to check it: pushing a template onto a dead interface controller is at
    /// best wasted work, and the templates would outlive the scene.
    private var connected = true
    /// The trip the car's built-in navigation cancelled our session for, or
    /// nil when guidance is ours to run. Guidance stays off until the driver
    /// asks for it back, or the trip changes: a different trip starts fresh.
    private var guidanceSuspendedForTripID: String?

    /// The car screen's controls are rebuilt only when what they say changes;
    /// reassigning them on every fix makes CarPlay redraw the bar.
    private var lastActiveChrome: (isPanning: Bool, ending: Bool, guidanceSuspended: Bool)?

    private lazy var endBarButton = CPBarButton(title: String(localized: "End")) { [weak self] _ in self?.confirmEnd() }
    private lazy var detailsBarButton = CPBarButton(title: String(localized: "Details")) { [weak self] _ in self?.showDetails() }
    /// Offered in place of Details while the car's own navigation has taken
    /// guidance away: tapping it hands the car screen back to us.
    private lazy var guidanceBarButton = CPBarButton(title: String(localized: "Guidance")) { [weak self] _ in
        guard let self else { return }
        resumeGuidance()
        render()
    }
    private lazy var donePanningBarButton = CPBarButton(title: String(localized: "Done")) { [weak self] _ in
        self?.mapTemplate.dismissPanningInterface(animated: true)
    }
    private lazy var activeMapButtons = CarPlayTemplates.mapButtons(
        onPan: { [weak self] in self?.mapTemplate.showPanningInterface(animated: true) },
        onZoomIn: { [weak self] in self?.map.zoom(by: 2) },
        onZoomOut: { [weak self] in self?.map.zoom(by: 0.5) }
    )

    init(session: TripSession, interface: CPInterfaceController, window: CPWindow) {
        self.session = session
        self.interface = interface
        self.window = window
        super.init()
    }

    func start() {
        window.rootViewController = map
        mapTemplate.mapDelegate = self
        mapTemplate.automaticallyHidesNavigationBar = false
        interface.setRootTemplate(mapTemplate, animated: false, completion: nil)
        render()
        observe()
    }

    func stop() {
        // The car is going away, not the trip: the phone keeps reporting.
        connected = false
        tearDownGuidance(reason: .cancelled)
    }

    func contentStyleDidChange(_ style: UIUserInterfaceStyle) {
        map.overrideUserInterfaceStyle = style
        // The annotation images bake `UIColor.label` and `.systemBackground`
        // in when they are drawn, so they have to be drawn again.
        map.refreshGlyphs()
    }

    // MARK: Observation

    /// `withObservationTracking` fires once; re-arm after every change, on
    /// the main actor, where every render happens.
    private func observe() {
        withObservationTracking {
            _ = session.phase
            _ = session.latest
            _ = session.reporting
        } onChange: { [weak self] in
            Task { @MainActor [weak self] in
                guard let self else { return }
                self.render()
                self.observe()
            }
        }
    }

    // MARK: Rendering

    func render() {
        switch session.phase {
        case .signedOut, .idle, .starting:
            renderIdle()
        case .paused(let active):
            renderPaused(active)
        case .active(let active), .ending(let active):
            renderActive(active)
        }
    }

    private func renderIdle() {
        // Only `.idle` means the trip itself finished; `.signedOut` and
        // `.starting` are this session going away, not the run completing.
        tearDownGuidance(reason: session.phase == .idle ? .finished : .cancelled)
        dismissPanning()
        resumeGuidance()
        dismissDetails()
        map.setTrip(nil)
        map.update(nil)
        // Its setter re-arms user tracking, which fights the driver's own
        // panning; the setter itself ignores anything but a genuine change.
        map.showsPhoneLocation = true
        let buttons = CarPlayTemplates.idleBarButtons(phase: session.phase) { [weak self] in self?.startFlow() }
        mapTemplate.leadingNavigationBarButtons = buttons.leading
        mapTemplate.trailingNavigationBarButtons = buttons.trailing
        mapTemplate.mapButtons = []
    }

    private func renderPaused(_ active: ActiveTrip) {
        tearDownGuidance(reason: .cancelled)
        dismissPanning()
        resumeGuidance()
        dismissDetails()
        map.setTrip(active.trip)
        map.update(nil)
        let buttons = CarPlayTemplates.pausedBarButtons { [weak self] in self?.confirmEnd() }
        mapTemplate.leadingNavigationBarButtons = buttons.leading
        mapTemplate.trailingNavigationBarButtons = buttons.trailing
        mapTemplate.mapButtons = []
    }

    private func renderActive(_ active: ActiveTrip) {
        map.setTrip(active.trip)
        map.update(session.latest)
        // A different run is a fresh start: whatever the car's navigation took
        // over for is over with the trip it was suspended on.
        let suspended = guidanceSuspendedForTripID == active.trip.id
        if !suspended {
            resumeGuidance()
            if navigationTripID != active.trip.id { startGuidance(active) }
        }
        updateGuidance(active, session.latest)
        updateOffRouteAlert(session.latest)

        let ending = session.phase.isEnding
        let chrome = (isPanning: isPanning, ending: ending, guidanceSuspended: suspended)
        if lastActiveChrome.map({ $0 == chrome }) != true {
            lastActiveChrome = chrome
            endBarButton.isEnabled = !ending
            mapTemplate.leadingNavigationBarButtons = [endBarButton]
            if isPanning {
                // The panning interface keeps up to two map buttons on screen;
                // none of ours make sense while the driver is dragging the map.
                mapTemplate.trailingNavigationBarButtons = [donePanningBarButton]
                mapTemplate.mapButtons = []
            } else {
                mapTemplate.trailingNavigationBarButtons = [suspended ? guidanceBarButton : detailsBarButton]
                mapTemplate.mapButtons = activeMapButtons
            }
        }

        if let details {
            if !interface.templates.contains(where: { $0 === details }) {
                // The driver popped it with the car's own back control.
                self.details = nil
                lastDetailItems = nil
            } else if interface.topTemplate === details {
                let items = detailItems(active)
                // Every fix rebuilds the same six strings; only a changed one
                // is worth making CarPlay redraw the template.
                let strings = items.map { $0.detail ?? "" }
                if strings != lastDetailItems {
                    lastDetailItems = strings
                    details.items = items
                }
            }
        }
    }

    /// Why a navigation session is being torn down. CarPlay distinguishes a
    /// trip that finished from one that was called off, and so does the car's
    /// own navigation.
    private enum GuidanceEnd {
        /// The driver's run is over.
        case finished
        /// The run carries on without car-screen guidance: paused, the car
        /// disconnected, or the car's built-in navigation took over.
        case cancelled
    }

    private func tearDownGuidance(reason: GuidanceEnd) {
        switch reason {
        case .finished: navigation?.finishTrip()
        case .cancelled: navigation?.cancelTrip()
        }
        navigation = nil
        navigationTripID = nil
        trip = nil
        maneuversByStop = [:]
        upcomingSequences = []
        addedSequences = []
        lastActiveChrome = nil
        offRouteEpisodeSeen = false
        if offRouteBanner != nil {
            offRouteBanner = nil
            mapTemplate.dismissNavigationAlert(animated: false) { _ in }
        }
    }

    /// Takes the panning interface down before the chrome that carries its
    /// Done button is replaced, so the driver is never left dragging a map
    /// with no way out — and `isPanning` never leaks into the next trip.
    private func dismissPanning() {
        guard isPanning else { return }
        mapTemplate.dismissPanningInterface(animated: false)
        isPanning = false
        lastPanTranslation = .zero
    }

    private func resumeGuidance() {
        guidanceSuspendedForTripID = nil
    }

    private func dismissDetails() {
        if let details, interface.topTemplate === details {
            interface.popTemplate(animated: true, completion: nil)
        }
        details = nil
        lastDetailItems = nil
    }

    // MARK: Guidance

    /// One CPTrip from the first stop to the last, with the stops as the
    /// maneuvers CarPlay shows one at a time.
    private func startGuidance(_ active: ActiveTrip) {
        tearDownGuidance(reason: .cancelled)
        guard let first = active.trip.stops.first, let last = active.trip.stops.last else { return }
        let tz = TimeZone(identifier: active.trip.timezone)
        func waypoint(_ stop: TripStop) -> CPNavigationWaypoint {
            CPNavigationWaypoint(centerPoint: CPLocationCoordinate3D(latitude: stop.lat, longitude: stop.lon, altitude: CLLocationDistanceMax),
                                 locationThreshold: nil, name: stop.name, address: nil, entryPoints: [], timeZone: tz)
        }
        let choice = CPRouteChoice(summaryVariants: [active.trip.headsign], additionalInformationVariants: [active.trip.route.longName], selectionSummaryVariants: [active.trip.headsign])
        let trip = CPTrip(originWaypoint: waypoint(first), destinationWaypoint: waypoint(last), routeChoices: [choice])
        self.trip = trip
        navigation = mapTemplate.startNavigationSession(for: trip)
        navigationTripID = active.trip.id
    }

    private func updateGuidance(_ active: ActiveTrip, _ adherence: Adherence?) {
        guard let navigation else { return }
        let tz = active.trip.timezone
        let stops = active.trip.stops
        let next = adherence?.nextStop ?? stops[0]

        func maneuver(for stop: TripStop, adherence: Adherence?) -> CPManeuver {
            let variants = CarPlayTemplates.maneuverVariants(stop: stop, adherence: adherence, timezone: tz)
            if let existing = maneuversByStop[stop.sequence] {
                if existing.instructionVariants != variants { existing.instructionVariants = variants }
                return existing
            }
            let made = CarPlayTemplates.maneuver(stop: stop, adherence: adherence, timezone: tz)
            maneuversByStop[stop.sequence] = made
            return made
        }

        // Never empty: the next stop is always one maneuver, with the stop
        // after it as the follower CarPlay may show alongside.
        var upcoming = [maneuver(for: next, adherence: adherence)]
        var sequences = [next.sequence]
        if let index = stops.firstIndex(where: { $0.sequence == next.sequence }), index + 1 < stops.count {
            let following = stops[index + 1]
            upcoming.append(maneuver(for: following, adherence: nil))
            sequences.append(following.sequence)
        }
        if sequences != upcomingSequences {
            upcomingSequences = sequences
            let fresh = zip(sequences, upcoming).filter { !addedSequences.contains($0.0) }.map(\.1)
            if !fresh.isEmpty {
                // A maneuver must be handed to the session, once and in
                // chronological order, before it may appear in `upcomingManeuvers`.
                navigation.add(fresh)
                addedSequences.formUnion(sequences)
            }
            navigation.upcomingManeuvers = upcoming
        }

        guard let adherence, let head = upcoming.first, let trip else { return }
        navigation.updateEstimates(CarPlayTemplates.stopEstimates(adherence: adherence), for: head)
        mapTemplate.update(CarPlayTemplates.tripEstimates(active: active, adherence: adherence), for: trip,
                           with: CarPlayTemplates.timeRemainingColor(for: adherence))
    }

    private func updateOffRouteAlert(_ adherence: Adherence?) {
        guard let adherence else { return }
        guard !adherence.isOnRoute else {
            if offRouteBanner != nil {
                offRouteBanner = nil
                mapTemplate.dismissNavigationAlert(animated: true) { _ in }
            }
            // Back on route: the next stretch off it earns a fresh banner.
            offRouteEpisodeSeen = false
            return
        }
        let subtitle = CarPlayTemplates.offRouteSubtitle(adherence: adherence)
        if let banner = offRouteBanner {
            // Still off route: keep the distance on the standing banner honest
            // rather than presenting a second one.
            if let alert = mapTemplate.currentNavigationAlert, subtitle != banner.subtitle {
                offRouteBanner = (banner.alert, subtitle)
                alert.updateTitleVariants([CarPlayTemplates.offRouteTitle], subtitleVariants: [subtitle])
            }
            return
        }
        // The driver has already seen — and dismissed — this stretch's banner.
        guard !offRouteEpisodeSeen else { return }
        // Something else may own the banner; never stack ours on top of it.
        guard mapTemplate.currentNavigationAlert == nil else { return }
        let alert = CarPlayTemplates.offRouteAlert(adherence: adherence)
        offRouteBanner = (alert, subtitle)
        offRouteEpisodeSeen = true
        mapTemplate.present(navigationAlert: alert, animated: true)
    }

    private func detailItems(_ active: ActiveTrip) -> [CPInformationItem] {
        CarPlayTemplates.detailItems(active: active, adherence: session.latest,
                                     reporting: session.reporting, fixesSent: session.fixesSent)
    }

    private func showDetails() {
        guard let active = session.activeTrip else { return }
        if let details, interface.topTemplate === details { return }
        let items = detailItems(active)
        let template = CPInformationTemplate(title: String(localized: "Trip"), layout: .leading,
                                             items: items, actions: [])
        details = template
        lastDetailItems = items.map { $0.detail ?? "" }
        interface.pushTemplate(template, animated: true, completion: nil)
    }

    // MARK: CPMapTemplateDelegate

    func mapTemplateDidCancelNavigation(_ mapTemplate: CPMapTemplate) {
        // The *system* cancelled navigation: the car's built-in navigation
        // started, and only one of us may guide at a time. The driver's trip
        // and its reporting carry on — only the car-screen guidance stops,
        // and it stays stopped: starting a fresh session on the next fix
        // would fight the car for the screen, over and over. The trailing bar
        // button becomes "Guidance", so the driver can ask for it back.
        tearDownGuidance(reason: .cancelled)
        guidanceSuspendedForTripID = session.activeTrip?.trip.id
        render()
    }

    func mapTemplateDidShowPanningInterface(_ mapTemplate: CPMapTemplate) {
        isPanning = true
        lastPanTranslation = .zero
        map.followsVehicle = false
        render()
    }

    func mapTemplateDidDismissPanningInterface(_ mapTemplate: CPMapTemplate) {
        isPanning = false
        // Follow the vehicle again at whatever zoom the driver chose, rather
        // than `recentre()`, which would also throw their zoom away.
        map.followsVehicle = true
        render()
    }

    func mapTemplate(_ mapTemplate: CPMapTemplate, panWith direction: CPMapTemplate.PanDirection) {
        var t = CGPoint.zero
        if direction.contains(.up) { t.y = 120 }
        if direction.contains(.down) { t.y = -120 }
        if direction.contains(.left) { t.x = 120 }
        if direction.contains(.right) { t.x = -120 }
        map.pan(by: t)
    }

    func mapTemplateDidBeginPanGesture(_ mapTemplate: CPMapTemplate) {
        lastPanTranslation = .zero
    }

    func mapTemplate(_ mapTemplate: CPMapTemplate, didUpdatePanGestureWithTranslation translation: CGPoint, velocity: CGPoint) {
        let delta = CGPoint(x: translation.x - lastPanTranslation.x, y: translation.y - lastPanTranslation.y)
        lastPanTranslation = translation
        map.pan(by: delta)
    }

    func mapTemplate(_ mapTemplate: CPMapTemplate, didDismiss navigationAlert: CPNavigationAlert, dismissalContext: CPNavigationAlert.DismissalContext) {
        // Only ours; another app's banner going away says nothing about the
        // driver. `offRouteEpisodeSeen` deliberately stays set: the banner is
        // gone, but it is not offered again until the vehicle has been back
        // on route and left it once more.
        guard navigationAlert === offRouteBanner?.alert else { return }
        offRouteBanner = nil
    }

    /// Runs `work`, then hands what it produced to `then` on the car screen.
    /// Every flow that fetches something wants the same guards around that:
    /// nothing may touch the interface controller once the car has gone,
    /// an expired sign-in pops to the root, and anything else is an alert.
    /// `serverMessage` shows what the server said in place of
    /// `failureMessage` when it said anything — which starting a trip wants
    /// and the pickers do not.
    private func load<T: Sendable>(_ failureMessage: String, serverMessage: Bool = false,
                                   _ work: @escaping @MainActor () async throws -> T,
                                   then: @escaping @MainActor (T) -> Void) {
        Task { [weak self] in
            guard let self else { return }
            do {
                let value = try await work()
                guard connected else { return }
                then(value)
            } catch {
                guard connected else { return }
                if session.handleIfUnauthorized(error) {
                    interface.popToRootTemplate(animated: true, completion: nil)
                    return
                }
                if serverMessage, case APIError.status(_, let message) = error, !message.isEmpty {
                    presentError(message)
                } else {
                    presentError(failureMessage)
                }
            }
        }
    }

    /// Vehicle → route → run, on list templates (spec §6.4). Sign-in stays on
    /// the phone; everything after it happens here.
    private func startFlow() {
        load(String(localized: "Could not load your vehicles")) {
            try await self.session.loadVehicles()
        } then: { [weak self] _ in
            guard let self else { return }
            if session.vehicles.count == 1 {
                pickRoute(for: session.vehicles[0])
            } else {
                let items = CarPlayTemplates.vehicleItems(session.vehicles) { [weak self] vehicle in
                    guard let self, !startInFlight else { return }
                    pickRoute(for: vehicle)
                }
                interface.pushTemplate(CarPlayTemplates.list(title: String(localized: "Your vehicle"), sections: [(nil, items)], maxItems: CPListTemplate.maximumItemCount, maxSections: CPListTemplate.maximumSectionCount), animated: true, completion: nil)
            }
        }
    }

    private func pickRoute(for vehicle: Vehicle) {
        load(String(localized: "Could not load routes")) {
            try await self.session.routes()
        } then: { [weak self] routes in
            guard let self else { return }
            let sections = CarPlayTemplates.routeSections(routes, recentIDs: session.settings.recentRouteIDs) { [weak self] route in
                guard let self, !startInFlight else { return }
                pickTrip(vehicle: vehicle, route: route)
            }
            interface.pushTemplate(CarPlayTemplates.list(title: vehicle.label.isEmpty ? vehicle.id : vehicle.label, sections: sections, maxItems: CPListTemplate.maximumItemCount, maxSections: CPListTemplate.maximumSectionCount), animated: true, completion: nil)
        }
    }

    private func pickTrip(vehicle: Vehicle, route: RouteInfo) {
        load(String(localized: "Could not load runs")) {
            try await self.session.trips(routeID: route.id)
        } then: { [weak self] page in
            guard let self else { return }
            let items = CarPlayTemplates.tripItems(page, now: Date()) { [weak self] trip in
                guard let self, !startInFlight else { return }
                confirmStart(vehicle: vehicle, route: route, trip: trip, timezone: page.timezone)
            }
            let list = CarPlayTemplates.list(title: String(localized: "Route \(route.shortName)"), sections: [(nil, items)], maxItems: CPListTemplate.maximumItemCount, maxSections: CPListTemplate.maximumSectionCount)
            list.emptyViewTitleVariants = [String(localized: "No runs today")]
            interface.pushTemplate(list, animated: true, completion: nil)
        }
    }

    private func confirmStart(vehicle: Vehicle, route: RouteInfo, trip: TripSummary, timezone: String) {
        let start = CPAlertAction(title: String(localized: "Start"), style: .default) { [weak self] _ in
            guard let self, !startInFlight else { return }
            guard case .idle = session.phase else {
                // A trip is already starting or running — started on the
                // phone, most likely, while this alert sat on the car screen.
                // Say so rather than letting the button do nothing. The error
                // waits for the dismissal: only one template may be presented.
                interface.dismissTemplate(animated: true) { [weak self] _, _ in
                    self?.presentError(TripSessionError.busy.localizedDescription)
                }
                return
            }
            interface.dismissTemplate(animated: true, completion: nil)
            startInFlight = true
            load(String(localized: "Could not start the trip"), serverMessage: true) {
                defer { self.startInFlight = false }
                try await self.session.start(vehicle: vehicle, tripID: trip.id)
            } then: { [weak self] _ in
                self?.interface.popToRootTemplate(animated: true, completion: nil)
            }
        }
        let cancel = CPAlertAction(title: String(localized: "Cancel"), style: .cancel) { [weak self] _ in
            self?.interface.dismissTemplate(animated: true, completion: nil)
        }
        let title = "\(Formatters.clock(trip.startsAt, timezone: timezone)) → \(trip.headsign)"
        interface.presentTemplate(CPAlertTemplate(titleVariants: [String(localized: "Start \(title)?"), String(localized: "Start this run?")], actions: [start, cancel]), animated: true, completion: nil)
    }

    private func presentError(_ message: String) {
        let ok = CPAlertAction(title: String(localized: "OK"), style: .cancel) { [weak self] _ in
            self?.interface.dismissTemplate(animated: true, completion: nil)
        }
        interface.presentTemplate(CPAlertTemplate(titleVariants: [message], actions: [ok]), animated: true, completion: nil)
    }

    // MARK: End

    private func confirmEnd() {
        let end = CPAlertAction(title: String(localized: "End Trip"), style: .destructive) { [weak self] _ in
            self?.interface.dismissTemplate(animated: true, completion: nil)
            self?.endTrip()
        }
        let cancel = CPAlertAction(title: String(localized: "Cancel"), style: .cancel) { [weak self] _ in
            self?.interface.dismissTemplate(animated: true, completion: nil)
        }
        interface.presentTemplate(CPAlertTemplate(titleVariants: [String(localized: "End this trip?")], actions: [end, cancel]), animated: true, completion: nil)
    }

    private func endTrip() {
        Task { [weak self] in
            guard let self else { return }
            do {
                try await session.end()
            } catch {
                let retry = CPAlertAction(title: String(localized: "Retry"), style: .default) { [weak self] _ in
                    self?.interface.dismissTemplate(animated: true, completion: nil)
                    self?.endTrip()
                }
                let local = CPAlertAction(title: String(localized: "End locally"), style: .destructive) { [weak self] _ in
                    self?.interface.dismissTemplate(animated: true, completion: nil)
                    self?.session.endLocally()
                }
                let keep = CPAlertAction(title: String(localized: "Keep going"), style: .cancel) { [weak self] _ in
                    self?.interface.dismissTemplate(animated: true, completion: nil)
                }
                interface.presentTemplate(CPAlertTemplate(titleVariants: [String(localized: "Could not end the trip"), String(localized: "Could not end")], actions: [retry, local, keep]), animated: true, completion: nil)
            }
        }
    }
}
