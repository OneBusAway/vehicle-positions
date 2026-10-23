import Foundation
import Testing
import UIKit
@testable import VehicleTracker

/// Real `UserDefaults` suites rather than a fake, so the settings read and
/// write the store the way the app does.
@Suite struct AppSettingsTests {
    nonisolated struct Sources: CustomTestStringConvertible {
        var managed: String?
        var saved: String?
        var builtIn = ""
        var expected: String?
        var managedIsInvalid = false

        var testDescription: String { "managed \(managed ?? "-"), saved \(saved ?? "-"), built in \(builtIn)" }
    }

    static let managed = "https://managed.example.org"

    func withDefaults(_ body: (UserDefaults) async throws -> Void) async rethrows {
        let name = "AppSettingsTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        try await body(defaults)
    }

    func setManaged(_ value: Any, in defaults: UserDefaults) {
        defaults.set([AppSettings.managedServerURLKey: value], forKey: AppSettings.managedConfigurationKey)
    }

    @Test(arguments: [
        Sources(managed: "https://managed.example.org", saved: "https://saved.example.org",
                builtIn: "https://default.example.org", expected: "https://managed.example.org"),
        Sources(managed: nil, saved: "https://saved.example.org", builtIn: "https://default.example.org",
                expected: "https://saved.example.org"),
        Sources(managed: nil, saved: nil, builtIn: "https://default.example.org", expected: "https://default.example.org"),
        Sources(managed: nil, saved: nil, expected: nil),
        Sources(managed: nil, saved: nil, builtIn: "http://default.example.org", expected: nil),
        Sources(managed: "http://managed.example.org", saved: nil, builtIn: "https://default.example.org",
                expected: "https://default.example.org", managedIsInvalid: true),
        Sources(managed: "http://managed.example.org", saved: "https://saved.example.org",
                expected: "https://saved.example.org", managedIsInvalid: true),
        Sources(managed: "https://user@managed.example.org", saved: "https://saved.example.org",
                expected: "https://saved.example.org", managedIsInvalid: true),
        Sources(managed: "https://:8080", saved: "https://saved.example.org",
                expected: "https://saved.example.org", managedIsInvalid: true),
        Sources(managed: "", saved: "https://saved.example.org",
                expected: "https://saved.example.org", managedIsInvalid: true),
    ])
    func signInServerURLOrder(_ sources: Sources) async {
        await withDefaults { defaults in
            if let managed = sources.managed { setManaged(managed, in: defaults) }
            if let saved = sources.saved { defaults.set(saved, forKey: "serverURL") }
            let settings = AppSettings(defaults: defaults, defaultServerURL: sources.builtIn)
            #expect(settings.signInServerURL?.absoluteString == sources.expected)
            #expect(settings.managedServerURLIsInvalid == sources.managedIsInvalid)
        }
    }

    @Test func aManagedValueThatIsNotAStringIsInvalid() async {
        await withDefaults { defaults in
            setManaged(42, in: defaults)
            let settings = AppSettings(defaults: defaults)
            #expect(settings.managedServerURL == nil)
            #expect(settings.managedServerURLIsInvalid)
        }
    }

    @Test func serverIsManagedOnlyForAValidManagedValue() async {
        await withDefaults { defaults in
            #expect(!AppSettings(defaults: defaults).serverIsManaged)
            setManaged("http://managed.example.org", in: defaults)
            #expect(!AppSettings(defaults: defaults).serverIsManaged)
            setManaged(Self.managed, in: defaults)
            #expect(AppSettings(defaults: defaults).serverIsManaged)
        }
    }

    @Test func aManagedValueSetAfterLaunchIsPickedUp() async {
        await withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            #expect(!settings.serverIsManaged)
            setManaged(Self.managed, in: defaults)
            #expect(await eventually { settings.managedServerURL?.absoluteString == Self.managed })
            defaults.removeObject(forKey: AppSettings.managedConfigurationKey)
            #expect(await eventually { !settings.serverIsManaged })
        }
    }

    /// Device management changes the settings from outside the app, which
    /// doesn't post `didChangeNotification` to it. A second `UserDefaults` on
    /// the same suite stands in for that.
    @Test func aManagedValueSetFromOutsideIsReadWhenTheAppComesToTheFront() async {
        let name = "AppSettingsTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let settings = AppSettings(defaults: defaults)
        UserDefaults(suiteName: name)!.set([AppSettings.managedServerURLKey: Self.managed],
                                           forKey: AppSettings.managedConfigurationKey)
        NotificationCenter.default.post(name: UIApplication.willEnterForegroundNotification, object: nil)
        #expect(await eventually { settings.managedServerURL?.absoluteString == Self.managed })
    }
}
