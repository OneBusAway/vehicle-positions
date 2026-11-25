import CryptoKit
import Foundation
import Observation
import OSLog
import UIKit

/// Something the app shows a driver once. A new one is a new case: the store
/// keeps each driver's by name, so its shape stays as it is.
nonisolated enum DriverFlag: String {
    case welcome
    /// The tip on "Choose a run" that says to tap a run, then Start trip.
    case runBoardTip
    /// The tip on Tracking that says what the sent count is.
    case sentCountTip
    /// The sheet after the driver's first trip that sent reports.
    case firstTripDone
}

/// Light, dark, or as the phone is, chosen in Settings. It is the phone's, not
/// the driver's: a sign-out leaves it.
nonisolated enum ThemeChoice: String, CaseIterable {
    case system, light, dark
}

/// What the driver typed last time, so the login screen and the route list
/// start where they left off, and the server address the agency sets, if any.
/// Nothing secret lives here; the token is in the Keychain.
@MainActor @Observable
final class AppSettings {
    static let maxRecentRoutes = 5
    static let maxRecentVehicles = 5
    /// Where device management (MDM) puts an app's managed configuration, and
    /// the key in it that names the agency's server.
    static let managedConfigurationKey = "com.apple.configuration.managed"
    static let managedServerURLKey = "server_url"

    private let defaults: UserDefaults
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "settings")
    @ObservationIgnored private var managedObservers: [any NSObjectProtocol] = []

    var serverURLString: String {
        didSet { defaults.set(serverURLString, forKey: Keys.serverURL) }
    }
    var email: String {
        didSet { defaults.set(email, forKey: Keys.email) }
    }
    private(set) var recentRouteIDs: [String] {
        didSet { defaults.set(recentRouteIDs, forKey: Keys.recentRoutes) }
    }
    /// The vehicles the driver starred. They belong to the phone, not the
    /// account: a sign-out leaves them, and an id the driver is no longer
    /// assigned stays until the vehicle comes back. Stored sorted, so the
    /// file does not churn.
    private(set) var favoriteVehicleIDs: Set<String> {
        didSet { defaults.set(favoriteVehicleIDs.sorted(), forKey: Keys.favoriteVehicles) }
    }
    /// The vehicles the driver drove last, newest first, at most five.
    private(set) var recentVehicleIDs: [String] {
        didSet { defaults.set(recentVehicleIDs, forKey: Keys.recentVehicles) }
    }
    /// The theme the phone's screens are shown in.
    var theme: ThemeChoice {
        didSet { defaults.set(theme.rawValue, forKey: Keys.theme) }
    }
    /// What each driver has been shown once, by `driverKey`: per driver, not
    /// per phone, so the next driver on a shared phone is shown it too. A
    /// sign-out leaves it. Stored sorted, so the file does not churn.
    private var seenByDriver: [String: Set<String>] {
        didSet { defaults.set(seenByDriver.mapValues { $0.sorted() }, forKey: Keys.seenByDriver) }
    }
    /// The agency's server from managed configuration, when it is one the app
    /// may use. Read again when the defaults change and when the app comes to
    /// the front.
    private(set) var managedServerURL: URL?
    /// The agency set a server the app may not use, so it is ignored and the
    /// driver types one instead.
    private(set) var managedServerURLIsInvalid = false
    /// The server an agency built into the app (`DEFAULT_SERVER_URL`). The
    /// build fails on one that isn't https; one the app may not use is ignored.
    let defaultServerURL: URL?

    init(
        defaults: UserDefaults = .standard,
        defaultServerURL: String = Bundle.main.object(forInfoDictionaryKey: "OBADefaultServerURL") as? String ?? ""
    ) {
        self.defaults = defaults
        self.defaultServerURL = ServerURLPolicy.presetURL(from: defaultServerURL)
        serverURLString = defaults.string(forKey: Keys.serverURL) ?? ""
        email = defaults.string(forKey: Keys.email) ?? ""
        recentRouteIDs = defaults.stringArray(forKey: Keys.recentRoutes) ?? []
        favoriteVehicleIDs = Set(defaults.stringArray(forKey: Keys.favoriteVehicles) ?? [])
        // Builds before this one kept only the last vehicle. Until a start
        // writes the list, that vehicle is the one recent vehicle, so it is
        // still offered after the update. The old key is read, never deleted.
        recentVehicleIDs = defaults.stringArray(forKey: Keys.recentVehicles)
            ?? [defaults.string(forKey: Keys.legacyLastVehicle)].compactMap { $0 }
        theme = defaults.string(forKey: Keys.theme).flatMap(ThemeChoice.init) ?? .system
        seenByDriver = (defaults.dictionary(forKey: Keys.seenByDriver) as? [String: [String]] ?? [:]).mapValues(Set.init)
        readManagedConfiguration()
        if !defaultServerURL.isEmpty && self.defaultServerURL == nil {
            log.warning("ignoring the built-in DEFAULT_SERVER_URL: not an address the app may use")
        }
        let reread: @Sendable (Notification) -> Void = { [weak self] _ in
            MainActor.assumeIsolated { self?.readManagedConfiguration() }
        }
        let center = NotificationCenter.default
        managedObservers = [
            center.addObserver(forName: UserDefaults.didChangeNotification, object: defaults, queue: .main, using: reread),
            // Device management changes the settings from outside the app,
            // which doesn't post the notification above.
            center.addObserver(forName: UIApplication.willEnterForegroundNotification, object: nil, queue: .main,
                               using: reread),
        ]
    }

    isolated deinit {
        managedObservers.forEach { NotificationCenter.default.removeObserver($0) }
    }

    /// Moves the route to the front of the recent list, keeping at most five.
    func rememberRoute(_ id: String) {
        recentRouteIDs = Self.remembering(id, in: recentRouteIDs, limit: Self.maxRecentRoutes)
    }

    /// Moves the vehicle to the front of the recent list, keeping at most five.
    func rememberVehicle(_ id: String) {
        recentVehicleIDs = Self.remembering(id, in: recentVehicleIDs, limit: Self.maxRecentVehicles)
    }

    func toggleFavorite(_ id: String) {
        if favoriteVehicleIDs.contains(id) {
            favoriteVehicleIDs.remove(id)
        } else {
            favoriteVehicleIDs.insert(id)
        }
    }

    /// The driver signed in, as the once-only flags know them: the SHA-256 of
    /// "host|email" in lower case, as hex, the email without the spaces
    /// round it, as Android's `DriverKey` makes it. A hash, so the email of
    /// a driver who signed out does not stay on the phone.
    static func driverKey(host: String, email: String) -> String {
        let driver = "\(host)|\(email.trimmingCharacters(in: .whitespacesAndNewlines))".lowercased()
        return SHA256.hash(data: Data(driver.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private var driverKey: String {
        Self.driverKey(host: serverURL?.host() ?? "", email: email)
    }

    /// The driver signed in has been shown this.
    func hasSeen(_ flag: DriverFlag) -> Bool {
        seenByDriver[driverKey]?.contains(flag.rawValue) ?? false
    }

    /// The driver signed in has now been shown this, for good.
    func markSeen(_ flag: DriverFlag) {
        seenByDriver[driverKey, default: []].insert(flag.rawValue)
    }

    /// `ids` with `id` at the front, any earlier copy of it dropped, and at
    /// most `limit` kept.
    private static func remembering(_ id: String, in ids: [String], limit: Int) -> [String] {
        var ids = ids.filter { $0 != id }
        ids.insert(id, at: 0)
        return Array(ids.prefix(limit))
    }

    var serverURL: URL? {
        let trimmed = serverURLString.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let url = URL(string: trimmed), url.host() != nil else { return nil }
        return url
    }

    var serverIsManaged: Bool { managedServerURL != nil }

    /// The address the sign-in screen starts from: the agency's, then the one
    /// this phone last used, then the one built in. Only for the next sign-in:
    /// a signed-in session keeps `serverURL`, because its token and trip
    /// belong to that server.
    var signInServerURL: URL? {
        managedServerURL ?? serverURL ?? defaultServerURL
    }

    private func readManagedConfiguration() {
        let value = defaults.dictionary(forKey: Self.managedConfigurationKey)?[Self.managedServerURLKey]
        let url = (value as? String).flatMap(ServerURLPolicy.presetURL(from:))
        let invalid = value != nil && url == nil
        if invalid && !managedServerURLIsInvalid {
            log.warning("ignoring the managed server_url: not an address the app may use")
        }
        managedServerURL = url
        managedServerURLIsInvalid = invalid
    }

    private enum Keys {
        static let serverURL = "serverURL"
        static let email = "email"
        static let recentRoutes = "recentRouteIDs"
        static let favoriteVehicles = "favoriteVehicleIDs"
        static let recentVehicles = "recentVehicleIDs"
        static let theme = "theme"
        static let seenByDriver = "seenByDriver"
        /// Written by builds before favourites and recents; read as a fallback only.
        static let legacyLastVehicle = "lastVehicleID"
    }
}
