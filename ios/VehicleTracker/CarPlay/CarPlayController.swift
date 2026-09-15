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
    private var offRouteAlerted = false
    private var offRouteSubtitle: String?
    private var details: CPInformationTemplate?
    private var isPanning = false
    private var lastPanTranslation = CGPoint.zero

    /// The car screen's controls are rebuilt only when what they say changes;
    /// reassigning them on every fix makes CarPlay redraw the bar.
    private var lastActiveChrome: (isPanning: Bool, ending: Bool)?

    private lazy var endBarButton = CPBarButton(title: String(localized: "End")) { [weak self] _ in self?.confirmEnd() }
    private lazy var detailsBarButton = CPBarButton(title: String(localized: "Details")) { [weak self] _ in self?.showDetails() }
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
        tearDownGuidance(reason: .cancelled)
    }

    func contentStyleDidChange(_ style: UIUserInterfaceStyle) {
        map.overrideUserInterfaceStyle = style
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
        dismissDetails()
        map.setTrip(nil)
        map.update(nil)
        map.showsPhoneLocation = true
        let buttons = CarPlayTemplates.idleBarButtons(phase: session.phase) { [weak self] in self?.startFlow() }
        mapTemplate.leadingNavigationBarButtons = buttons.leading
        mapTemplate.trailingNavigationBarButtons = buttons.trailing
        mapTemplate.mapButtons = []
    }

    private func renderPaused(_ active: ActiveTrip) {
        tearDownGuidance(reason: .cancelled)
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
        if navigationTripID != active.trip.id {
            startGuidance(active)
        }
        updateGuidance(active, session.latest)
        updateOffRouteAlert(session.latest)

        let ending: Bool = { if case .ending = session.phase { return true } else { return false } }()
        let chrome = (isPanning: isPanning, ending: ending)
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
                mapTemplate.trailingNavigationBarButtons = [detailsBarButton]
                mapTemplate.mapButtons = activeMapButtons
            }
        }

        if let details {
            if !interface.templates.contains(where: { $0 === details }) {
                // The driver popped it with the car's own back control.
                self.details = nil
            } else if interface.topTemplate === details {
                details.items = CarPlayTemplates.detailItems(active: active, adherence: session.latest, reporting: session.reporting)
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
        if offRouteAlerted {
            offRouteAlerted = false
            offRouteSubtitle = nil
            mapTemplate.dismissNavigationAlert(animated: false) { _ in }
        }
    }

    private func dismissDetails() {
        if let details, interface.topTemplate === details {
            interface.popTemplate(animated: true, completion: nil)
        }
        details = nil
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
            if offRouteAlerted {
                offRouteAlerted = false
                offRouteSubtitle = nil
                mapTemplate.dismissNavigationAlert(animated: true) { _ in }
            }
            return
        }
        let subtitle = CarPlayTemplates.offRouteSubtitle(adherence: adherence)
        if offRouteAlerted {
            // Still off route: keep the distance on the standing banner honest
            // rather than presenting a second one.
            if let alert = mapTemplate.currentNavigationAlert, subtitle != offRouteSubtitle {
                offRouteSubtitle = subtitle
                alert.updateTitleVariants([CarPlayTemplates.offRouteTitle], subtitleVariants: [subtitle])
            }
            return
        }
        // Something else may own the banner; never stack ours on top of it.
        guard mapTemplate.currentNavigationAlert == nil else { return }
        offRouteAlerted = true
        offRouteSubtitle = subtitle
        mapTemplate.present(navigationAlert: CarPlayTemplates.offRouteAlert(adherence: adherence, onOK: {}), animated: true)
    }

    private func showDetails() {
        guard let active = session.activeTrip else { return }
        if let details, interface.topTemplate === details { return }
        let template = CPInformationTemplate(title: String(localized: "Trip"), layout: .leading,
                                             items: CarPlayTemplates.detailItems(active: active, adherence: session.latest, reporting: session.reporting),
                                             actions: [])
        details = template
        interface.pushTemplate(template, animated: true, completion: nil)
    }

    // MARK: CPMapTemplateDelegate

    func mapTemplateDidCancelNavigation(_ mapTemplate: CPMapTemplate) {
        // The *system* cancelled navigation: the car's built-in navigation
        // started, and only one of us may guide at a time. The driver's trip
        // and its reporting carry on — only the car-screen guidance stops.
        // Clearing `navigationTripID` lets a later render offer it again.
        tearDownGuidance(reason: .cancelled)
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
        // The driver dismissed it: stay quiet until the next off-route episode.
    }

    /// Vehicle → route → run, on list templates (spec §6.4). Sign-in stays on
    /// the phone; everything after it happens here.
    private func startFlow() {
        Task { [weak self] in
            guard let self else { return }
            do {
                try await session.loadVehicles()
            } catch {
                presentError(String(localized: "Could not load your vehicles"))
                return
            }
            if session.vehicles.count == 1 {
                pickRoute(for: session.vehicles[0])
            } else {
                let items = CarPlayTemplates.vehicleItems(session.vehicles) { [weak self] vehicle in self?.pickRoute(for: vehicle) }
                interface.pushTemplate(CarPlayTemplates.list(title: String(localized: "Your vehicle"), sections: [(nil, items)], maxItems: CPListTemplate.maximumItemCount), animated: true, completion: nil)
            }
        }
    }

    private func pickRoute(for vehicle: Vehicle) {
        Task { [weak self] in
            guard let self else { return }
            do {
                let routes = try await session.routes()
                let sections = CarPlayTemplates.routeSections(routes, recentIDs: session.settings.recentRouteIDs) { [weak self] route in
                    self?.pickTrip(vehicle: vehicle, route: route)
                }
                interface.pushTemplate(CarPlayTemplates.list(title: vehicle.label.isEmpty ? vehicle.id : vehicle.label, sections: sections, maxItems: CPListTemplate.maximumItemCount), animated: true, completion: nil)
            } catch {
                presentError(String(localized: "Could not load routes"))
            }
        }
    }

    private func pickTrip(vehicle: Vehicle, route: RouteInfo) {
        Task { [weak self] in
            guard let self else { return }
            do {
                let page = try await session.trips(routeID: route.id)
                let items = CarPlayTemplates.tripItems(page, now: Date()) { [weak self] trip in
                    self?.confirmStart(vehicle: vehicle, route: route, trip: trip, timezone: page.timezone)
                }
                let list = CarPlayTemplates.list(title: String(localized: "Route \(route.shortName)"), sections: [(nil, items)], maxItems: CPListTemplate.maximumItemCount)
                list.emptyViewTitleVariants = [String(localized: "No runs today")]
                interface.pushTemplate(list, animated: true, completion: nil)
            } catch {
                presentError(String(localized: "Could not load runs"))
            }
        }
    }

    private func confirmStart(vehicle: Vehicle, route: RouteInfo, trip: TripSummary, timezone: String) {
        let start = CPAlertAction(title: String(localized: "Start"), style: .default) { [weak self] _ in
            guard let self else { return }
            interface.dismissTemplate(animated: true, completion: nil)
            Task { [weak self] in
                guard let self else { return }
                do {
                    try await session.start(vehicle: vehicle, tripID: trip.id)
                    interface.popToRootTemplate(animated: true, completion: nil)
                } catch APIError.status(_, let message) where !message.isEmpty {
                    presentError(message)
                } catch {
                    presentError(String(localized: "Could not start the trip"))
                }
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
