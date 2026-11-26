import Foundation
import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

/// The real Tracking screen, with a trip under way, does what During a trip
/// says.
@MainActor @Suite struct TrackingSettingsTests {
    let power = FakePowerSource()
    let idleTimer = FakeIdleTimer()
    let locations = FakeLocationSource()
    let clock = ManualClock(start: TripFixtures.at(8, 0))

    func session() async throws -> TripSession {
        let api = FakeTrackerAPI()
        let defaults = UserDefaults(suiteName: "TrackingSettingsTests-\(UUID().uuidString)")!
        let session = TripSession(settings: AppSettings(defaults: defaults), tokens: InMemoryTokenStore(), store: InMemoryActiveTripStore(),
                                  locations: locations, access: FakeLocationAccess(), makeAPI: { _, _ in api }, now: { clock.now })
        try await session.signIn(serverURL: URL(string: "https://positions.example.org")!, email: "d@test.com", password: "pw")
        try await session.start(vehicle: Vehicle(id: "bus-1", label: "Bus 1"), tripID: "T1")
        return session
    }

    /// Tracking on the app's own screen, where its sheet can come up.
    func shown(_ session: TripSession) throws -> UIWindow {
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.rootViewController = UIHostingController(
            rootView: TrackingView()
                .environment(session)
                .environment(ServerSetup(session: session))
                .environment(ScreenAwakeCoordinator(power: power, idleTimer: idleTimer))
        )
        window.isHidden = false
        window.layoutIfNeeded()
        return window
    }

    func map(in controller: UIViewController?) -> RouteMapViewController? {
        guard let controller else { return nil }
        if let map = controller as? RouteMapViewController { return map }
        return controller.children.lazy.compactMap { map(in: $0) }.first
    }

    @Test func untilTheDriverChooses() async throws {
        let session = try await session()
        let window = try shown(session)
        defer { window.isHidden = true }
        #expect(await eventually(within: 10) { map(in: window.rootViewController)?.followFraming != nil })
        #expect(map(in: window.rootViewController)?.followFraming?.up == .heading)
        #expect(!idleTimer.isDisabled, "the phone is on battery")
        power.plug()
        #expect(idleTimer.isDisabled)
    }

    @Test func asTheDriverChose() async throws {
        let session = try await session()
        session.settings.screenOn = .always
        session.settings.mapUp = .north
        let window = try shown(session)
        defer { window.isHidden = true }
        #expect(await eventually(within: 10) { map(in: window.rootViewController)?.followFraming != nil })
        #expect(map(in: window.rootViewController)?.followFraming?.up == .north)
        #expect(await eventually(within: 10) { idleTimer.isDisabled }, "on battery")
    }
}
