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
    private var maneuvers: [CPManeuver] = []
    private var maneuverKey = ""
    private var offRouteAlerted = false
    private var details: CPInformationTemplate?
    private var isPanning = false
    private var lastPanTranslation = CGPoint.zero

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
        tearDownGuidance()
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
        tearDownGuidance()
        map.setTrip(nil)
        map.update(nil)
        map.showsPhoneLocation = true
        let buttons = CarPlayTemplates.idleBarButtons(phase: session.phase) { [weak self] in self?.startFlow() }
        mapTemplate.leadingNavigationBarButtons = buttons.leading
        mapTemplate.trailingNavigationBarButtons = buttons.trailing
        mapTemplate.mapButtons = []
    }

    private func renderPaused(_ active: ActiveTrip) {
        tearDownGuidance()
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
        let end = endButton()
        end.isEnabled = !ending
        mapTemplate.leadingNavigationBarButtons = [end]
        if isPanning {
            mapTemplate.trailingNavigationBarButtons = [CPBarButton(title: String(localized: "Done")) { [weak self] _ in
                self?.mapTemplate.dismissPanningInterface(animated: true)
            }]
        } else {
            mapTemplate.trailingNavigationBarButtons = [CPBarButton(title: String(localized: "Details")) { [weak self] _ in self?.showDetails() }]
            mapTemplate.mapButtons = CarPlayTemplates.mapButtons(
                onPan: { [weak self] in self?.mapTemplate.showPanningInterface(animated: true) },
                onZoomIn: { [weak self] in self?.map.zoom(by: 2) },
                onZoomOut: { [weak self] in self?.map.zoom(by: 0.5) }
            )
        }

        if let details, interface.topTemplate === details {
            details.items = CarPlayTemplates.detailItems(active: active, adherence: session.latest, reporting: session.reporting)
        }
    }

    private func tearDownGuidance() {
        navigation?.finishTrip()
        navigation = nil
        navigationTripID = nil
        trip = nil
        maneuvers = []
        maneuverKey = ""
        if offRouteAlerted {
            mapTemplate.dismissNavigationAlert(animated: false) { _ in }
            offRouteAlerted = false
        }
        if let details, interface.topTemplate === details {
            interface.popTemplate(animated: true, completion: nil)
        }
        details = nil
    }

    // MARK: Guidance

    /// One CPTrip from the first stop to the last, with the stops as the
    /// maneuvers CarPlay shows one at a time.
    private func startGuidance(_ active: ActiveTrip) {
        tearDownGuidance()
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
        let next = adherence?.nextStop ?? active.trip.stops[0]
        let key = "\(next.sequence)|\(adherence.map { Formatters.deviation($0.scheduleDeviation) } ?? "")"
        if key != maneuverKey {
            maneuverKey = key
            var list = [CarPlayTemplates.maneuver(stop: next, adherence: adherence, timezone: tz)]
            if let index = active.trip.stops.firstIndex(where: { $0.sequence == next.sequence }), index + 1 < active.trip.stops.count {
                list.append(CarPlayTemplates.maneuver(stop: active.trip.stops[index + 1], adherence: nil, timezone: tz))
            }
            maneuvers = list
            // CarPlay requires every maneuver to be added to the session
            // before it may appear in `upcomingManeuvers`.
            navigation.add(list)
            navigation.upcomingManeuvers = list
        }
        guard let adherence, let first = maneuvers.first, let trip else { return }
        navigation.updateEstimates(CarPlayTemplates.stopEstimates(adherence: adherence), for: first)
        mapTemplate.update(CarPlayTemplates.tripEstimates(active: active, adherence: adherence), for: trip,
                           with: CarPlayTemplates.timeRemainingColor(for: adherence))
    }

    private func updateOffRouteAlert(_ adherence: Adherence?) {
        guard let adherence else { return }
        if !adherence.isOnRoute, !offRouteAlerted {
            offRouteAlerted = true
            mapTemplate.present(navigationAlert: CarPlayTemplates.offRouteAlert(adherence: adherence, onOK: {}), animated: true)
        } else if adherence.isOnRoute, offRouteAlerted {
            offRouteAlerted = false
            mapTemplate.dismissNavigationAlert(animated: true) { _ in }
        }
    }

    private func showDetails() {
        guard let active = session.activeTrip else { return }
        let template = CPInformationTemplate(title: String(localized: "Trip"), layout: .leading,
                                             items: CarPlayTemplates.detailItems(active: active, adherence: session.latest, reporting: session.reporting),
                                             actions: [])
        details = template
        interface.pushTemplate(template, animated: true, completion: nil)
    }

    // MARK: CPMapTemplateDelegate

    func mapTemplateDidCancelNavigation(_ mapTemplate: CPMapTemplate) {
        // The car's own "end route" control: same as the driver tapping End.
        endTrip()
    }

    func mapTemplateDidShowPanningInterface(_ mapTemplate: CPMapTemplate) {
        isPanning = true
        lastPanTranslation = .zero
        map.followsVehicle = false
        render()
    }

    func mapTemplateDidDismissPanningInterface(_ mapTemplate: CPMapTemplate) {
        isPanning = false
        map.recentre()
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

    private func endButton() -> CPBarButton {
        CPBarButton(title: String(localized: "End")) { [weak self] _ in self?.confirmEnd() }
    }

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
