import Observation
import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

@Suite struct PhoneWindowCameToFrontTests {
    @Observable final class Window {
        var phase: ScenePhase
        /// The phase the view was last drawn with; nil until it appears.
        var drawn: ScenePhase?
        var runs = 0

        init(_ phase: ScenePhase) {
            self.phase = phase
        }
    }

    struct Content: View {
        let window: Window

        var body: some View {
            Color.clear
                .onChange(of: window.phase, initial: true) { _, phase in window.drawn = phase }
                .modifier(PhoneWindowCameToFront { window.runs += 1 })
                .environment(\.scenePhase, window.phase)
        }
    }

    /// Puts the modifier on screen under `window.phase`, as the app's window
    /// would have it, and waits until it has appeared.
    func show(_ window: Window) async throws -> UIWindow {
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let shown = UIWindow(windowScene: scene)
        shown.rootViewController = UIHostingController(rootView: Content(window: window))
        shown.isHidden = false
        try #require(await eventually { window.drawn != nil })
        return shown
    }

    /// After a start from the car the phone's window can come up already in
    /// front: no change follows, so the first appearance has to count.
    @Test func runsWhenTheWindowFirstAppearsAlreadyInFront() async throws {
        let window = Window(.active)
        let shown = try await show(window)
        #expect(await eventually { window.runs == 1 })
        withExtendedLifetime(shown) {}
    }

    @Test func runsEachTimeTheWindowComesToTheFrontAndNotBefore() async throws {
        let window = Window(.background)
        let shown = try await show(window)
        #expect(window.runs == 0, "a window that appears behind hasn't come to the front")

        window.phase = .active
        #expect(await eventually { window.runs == 1 })
        window.phase = .background
        try #require(await eventually { window.drawn == .background })
        window.phase = .active
        #expect(await eventually { window.runs == 2 })
        withExtendedLifetime(shown) {}
    }
}
