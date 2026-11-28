import Darwin
import Foundation
import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

/// The occupancy row as assistive technologies find it. With VoiceOver it is
/// one adjustable element; without, as Voice Control, Switch Control and Full
/// Keyboard Access use it, four buttons named by their words.
@MainActor @Suite(.serialized) struct OccupancyRowAccessibilityTests {
    let trip = TripFixtures.t1

    func choice() -> OccupancyChoice {
        let defaults = UserDefaults(suiteName: "OccupancyRowAccessibilityTests-\(UUID().uuidString)")!
        return OccupancyChoice(defaults: defaults, settings: .signedIn(defaults))
    }

    func shown(_ choice: OccupancyChoice, voiceOver: Bool) throws -> UIWindow {
        AccessibilityClient.turnOn()
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.rootViewController = UIHostingController(rootView: OccupancyRow(trip: trip, choice: choice, voiceOver: voiceOver))
        window.isHidden = false
        window.layoutIfNeeded()
        return window
    }

    func elements(in window: UIWindow) -> [NSObject] {
        AccessibilityClient.elements(in: window)
    }

    func element(_ label: String, in window: UIWindow) -> NSObject? {
        elements(in: window).first { $0.accessibilityLabel == label }
    }

    @Test func withoutVoiceOverEachChoiceIsAButtonNamedByItsWords() async throws {
        let choice = choice()
        let window = try shown(choice, voiceOver: false)
        defer { window.isHidden = true }
        #expect(await eventually(within: 5) { element("Full", in: window) != nil })

        let buttons = elements(in: window).filter { $0.accessibilityTraits.contains(.button) }
        #expect(buttons.map(\.accessibilityLabel) == ["Seats free", "Few seats", "Standing", "Full"])
        #expect(buttons.allSatisfy { !$0.accessibilityTraits.contains(.selected) })
        #expect(element(OccupancyText.title, in: window)?.accessibilityTraits.contains(.adjustable) != true)
    }

    /// As Voice Control's "Tap Full", or Switch Control selecting it.
    @Test func aChoicePickedByNameIsChosen_andMarked() async throws {
        let choice = choice()
        let window = try shown(choice, voiceOver: false)
        defer { window.isHidden = true }
        #expect(await eventually(within: 5) { element("Full", in: window) != nil })

        #expect(element("Full", in: window)?.accessibilityActivate() == true)

        #expect(choice.occupancy(tripID: trip.id, serviceDate: trip.serviceDate) == .full)
        #expect(await eventually(within: 5) { element("Full", in: window)?.accessibilityTraits.contains(.selected) == true })
        #expect(element("Standing", in: window)?.accessibilityTraits.contains(.selected) == false)
    }

    @Test func withVoiceOverTheRowIsOneChoice_steppedBySwiping() async throws {
        let choice = choice()
        let window = try shown(choice, voiceOver: true)
        defer { window.isHidden = true }
        #expect(await eventually(within: 5) { element(OccupancyText.title, in: window) != nil })

        let row = try #require(element(OccupancyText.title, in: window))
        #expect(elements(in: window).count == 1, "the buttons are inside it")
        #expect(row.accessibilityTraits.contains(.adjustable))
        #expect(row.accessibilityValue == "Not set")

        row.accessibilityIncrement()

        #expect(choice.occupancy(tripID: trip.id, serviceDate: trip.serviceDate) == .manySeatsAvailable)
        #expect(await eventually(within: 5) { element(OccupancyText.title, in: window)?.accessibilityValue == "Seats free" })
    }
}

/// A missing library or switch fails the test at once, saying which.
@MainActor @Suite struct AccessibilityClientTests {
    @Test func aLibraryThatWontOpenFailsTheTest_namingIt() {
        withKnownIssue {
            #expect(AccessibilityClient.automationSwitch(library: "/usr/lib/libNoSuchLibrary.dylib") == nil)
        } matching: { issue in
            issue.comments.contains { $0.rawValue.hasPrefix("could not open /usr/lib/libNoSuchLibrary.dylib: ") }
        }
    }

    @Test func aMissingSwitchFailsTheTest_namingIt() {
        withKnownIssue {
            #expect(AccessibilityClient.automationSwitch(symbol: "_AXSNoSuchSwitch") == nil)
        } matching: { issue in
            issue.comments.contains { $0.rawValue.hasPrefix("/usr/lib/libAccessibility.dylib has no _AXSNoSuchSwitch: ") }
        }
    }
}

/// SwiftUI builds its accessibility tree only for an assistive client.
/// libAccessibility's automation switch, which UI testing tools such as
/// EarlGrey throw, makes the test process one, so a test can read what
/// VoiceOver, Voice Control and Switch Control are given.
@MainActor enum AccessibilityClient {
    private static var isOn = false

    static func turnOn() {
        guard !isOn, let automation = automationSwitch() else { return }
        automation(1)
        isOn = true
    }

    /// The switch, or nil once the test has failed saying why it can't be
    /// had, rather than waiting out elements that never come.
    static func automationSwitch(library path: String = "/usr/lib/libAccessibility.dylib",
                                 symbol name: String = "_AXSSetAutomationEnabled") -> (@convention(c) (Int32) -> Void)? {
        guard let library = dlopen(path, RTLD_NOW) else {
            Issue.record("could not open \(path): \(lastLoadError)")
            return nil
        }
        guard let symbol = dlsym(library, name) else {
            Issue.record("\(path) has no \(name): \(lastLoadError)")
            return nil
        }
        return unsafeBitCast(symbol, to: (@convention(c) (Int32) -> Void).self)
    }

    private static var lastLoadError: String {
        dlerror().map { String(cString: $0) } ?? "no reason given"
    }

    /// The elements under `object`, in order, as an assistive client walks them.
    static func elements(in object: NSObject) -> [NSObject] {
        if object.isAccessibilityElement { return [object] }
        if let children = object.accessibilityElements as? [NSObject] { return children.flatMap { elements(in: $0) } }
        if let view = object as? UIView { return view.subviews.flatMap { elements(in: $0) } }
        return []
    }
}
