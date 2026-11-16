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

    @Test func aFavoriteTogglesOnAndOffAndIsThereOnTheNextLaunch() async {
        await withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            #expect(settings.favoriteVehicleIDs.isEmpty)
            settings.toggleFavorite("4317")
            settings.toggleFavorite("7034")
            #expect(settings.favoriteVehicleIDs == ["4317", "7034"])
            #expect(AppSettings(defaults: defaults).favoriteVehicleIDs == ["4317", "7034"], "a relaunch finds them")
            settings.toggleFavorite("4317")
            #expect(settings.favoriteVehicleIDs == ["7034"])
            #expect(AppSettings(defaults: defaults).favoriteVehicleIDs == ["7034"])
        }
    }

    @Test func recentVehiclesKeepTheFiveNewestFirst() async {
        await withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            #expect(settings.recentVehicleIDs.isEmpty)
            for id in ["1", "2", "3", "4", "5"] { settings.rememberVehicle(id) }
            #expect(settings.recentVehicleIDs == ["5", "4", "3", "2", "1"])
            settings.rememberVehicle("6")
            #expect(settings.recentVehicleIDs == ["6", "5", "4", "3", "2"], "the sixth drops the oldest")
            settings.rememberVehicle("4")
            #expect(settings.recentVehicleIDs == ["4", "6", "5", "3", "2"], "a repeat moves to the front")
            #expect(AppSettings(defaults: defaults).recentVehicleIDs == ["4", "6", "5", "3", "2"], "a relaunch finds them")
        }
    }

    /// Builds before favourites kept only the last vehicle. It shows under
    /// Recent after the update, and its key is left where it was.
    @Test func theOldLastVehicleIsTheOneRecentVehicleUntilAStartWritesTheList() async {
        await withDefaults { defaults in
            defaults.set("4317", forKey: "lastVehicleID")
            let settings = AppSettings(defaults: defaults)
            #expect(settings.recentVehicleIDs == ["4317"])
            settings.rememberVehicle("7034")
            #expect(settings.recentVehicleIDs == ["7034", "4317"])
            #expect(AppSettings(defaults: defaults).recentVehicleIDs == ["7034", "4317"])
            #expect(defaults.string(forKey: "lastVehicleID") == "4317", "the old key is never deleted")
        }
    }

    func signIn(_ settings: AppSettings, to server: String = "https://tracker.example.org", as email: String) {
        settings.serverURLString = server
        settings.email = email
    }

    /// The once-only flags name a driver by a hash, so the phone keeps no
    /// email once the driver has signed out.
    @Test func aDriverIsKnownByTheHashOfServerHostAndEmail() {
        let key = AppSettings.driverKey(host: "tracker.example.org", email: "maria@metro.example")
        #expect(key == "475edb74202a73b4664cf7aee5f3124923431382b443aff4616442b60bc477f0", "SHA-256 of host|email")
        #expect(AppSettings.driverKey(host: "Tracker.Example.org", email: "Maria@Metro.example") == key, "in lower case")
        #expect(AppSettings.driverKey(host: "tracker.example.org", email: " maria@metro.example\n") == key, "without the spaces round it")
        #expect(AppSettings.driverKey(host: "tracker.example.org", email: "omar@metro.example") != key)
        #expect(AppSettings.driverKey(host: "other.example.org", email: "maria@metro.example") != key)
    }

    @Test func aFlagIsSeenByTheDriverWhoSawItAndNobodyElse() async {
        await withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            signIn(settings, as: "maria@metro.example")
            #expect(!settings.hasSeen(.welcome))
            settings.markSeen(.welcome)
            #expect(settings.hasSeen(.welcome))
            signIn(settings, as: "omar@metro.example")
            #expect(!settings.hasSeen(.welcome), "the next driver on a shared phone")
            signIn(settings, to: "https://other.example.org", as: "maria@metro.example")
            #expect(!settings.hasSeen(.welcome), "the same email on another server is another driver")
            signIn(settings, as: "Maria@Metro.example")
            #expect(settings.hasSeen(.welcome), "however the email is typed")
        }
    }

    /// Sign out clears the email. The flags stay, under a key that does not
    /// hold it.
    @Test func aFlagOutlivesSignOutAndARelaunchWithoutKeepingTheEmail() async throws {
        try await withDefaults { defaults in
            let settings = AppSettings(defaults: defaults)
            signIn(settings, as: "maria@metro.example")
            settings.markSeen(.welcome)
            settings.email = ""
            #expect(!settings.hasSeen(.welcome), "nobody is signed in")
            #expect(defaults.string(forKey: "email") == "")
            let stored = try #require(defaults.dictionary(forKey: "seenByDriver"))
            #expect(!String(describing: stored).contains("maria"), "only the hash is kept")
            let relaunched = AppSettings(defaults: defaults)
            signIn(relaunched, as: "maria@metro.example")
            #expect(relaunched.hasSeen(.welcome))
        }
    }

    /// The store keeps each driver's flags by name, so a flag this build
    /// does not know, from a later one, is left as it is.
    @Test func aNewFlagNeedsNoChangeToWhatIsStored() async {
        await withDefaults { defaults in
            let key = AppSettings.driverKey(host: "tracker.example.org", email: "maria@metro.example")
            defaults.set([key: ["runBoardTip", "firstTripDone"]], forKey: "seenByDriver")
            let settings = AppSettings(defaults: defaults)
            signIn(settings, as: "maria@metro.example")
            #expect(!settings.hasSeen(.welcome))
            settings.markSeen(.welcome)
            settings.markSeen(.welcome)
            #expect(defaults.dictionary(forKey: "seenByDriver") as? [String: [String]] == [key: ["firstTripDone", "runBoardTip", "welcome"]])
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
