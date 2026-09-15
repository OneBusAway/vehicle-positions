import Foundation
import Observation

/// What the driver typed last time, so the login screen and the route list
/// start where they left off. Nothing secret lives here; the token is in the
/// Keychain.
@MainActor @Observable
final class AppSettings {
    static let maxRecentRoutes = 5

    private let defaults: UserDefaults

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

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        serverURLString = defaults.string(forKey: Keys.serverURL) ?? ""
        email = defaults.string(forKey: Keys.email) ?? ""
        recentRouteIDs = defaults.stringArray(forKey: Keys.recentRoutes) ?? []
        lastVehicleID = defaults.string(forKey: Keys.lastVehicle)
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

    private enum Keys {
        static let serverURL = "serverURL"
        static let email = "email"
        static let recentRoutes = "recentRouteIDs"
        static let lastVehicle = "lastVehicleID"
    }
}
