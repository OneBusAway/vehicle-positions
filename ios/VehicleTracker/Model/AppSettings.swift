import Foundation
import Observation
import OSLog
import UIKit

/// What the driver typed last time, so the login screen and the route list
/// start where they left off, and the server address the agency sets, if any.
/// Nothing secret lives here; the token is in the Keychain.
@MainActor @Observable
final class AppSettings {
    static let maxRecentRoutes = 5
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
    var lastVehicleID: String? {
        didSet { defaults.set(lastVehicleID, forKey: Keys.lastVehicle) }
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
        lastVehicleID = defaults.string(forKey: Keys.lastVehicle)
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
        var ids = recentRouteIDs.filter { $0 != id }
        ids.insert(id, at: 0)
        recentRouteIDs = Array(ids.prefix(Self.maxRecentRoutes))
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
        static let lastVehicle = "lastVehicleID"
    }
}
