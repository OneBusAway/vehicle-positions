import CarPlay
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
        mapTemplate.leadingNavigationBarButtons = [endButton()]
        mapTemplate.trailingNavigationBarButtons = [CarPlayTemplates.titleButton(String(localized: "Resume on iPhone"))]
        mapTemplate.mapButtons = []
    }

    /// Filled in by the active-trip task; until then a paused-style map.
    private func renderActive(_ active: ActiveTrip) {
        map.setTrip(active.trip)
        map.update(session.latest)
        mapTemplate.leadingNavigationBarButtons = [endButton()]
        mapTemplate.trailingNavigationBarButtons = []
        mapTemplate.mapButtons = []
    }

    private func tearDownGuidance() {}

    /// Until the in-car picker exists, point at the phone.
    private func startFlow() {
        let info = CPInformationTemplate(title: String(localized: "Start trip"), layout: .leading,
                                         items: [CPInformationItem(title: String(localized: "Pick a trip on iPhone"), detail: nil)], actions: [])
        interface.pushTemplate(info, animated: true, completion: nil)
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
