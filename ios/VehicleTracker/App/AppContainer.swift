import Foundation
import VehiclePositionsKit

/// Builds the app's long-lived objects once and wires them together. Every
/// client reads the token from the Keychain on each request, so a re-login
/// mid-trip takes effect on the next report, and every client shares one
/// `TokenRefresher`, so the app makes one refresh at a time.
@MainActor
final class AppContainer {
    /// The one container the app and the CarPlay scene share, so the car and
    /// the phone drive the same `TripSession`.
    static let shared = AppContainer()

    let settings: AppSettings
    let session: TripSession

    init() {
        let settings = AppSettings()
        let tokens = KeychainTokenStore()
        let refresher = TokenRefresher(tokens: tokens)
        self.settings = settings
        session = TripSession(
            settings: settings,
            tokens: tokens,
            store: FileActiveTripStore(),
            locations: CoreLocationSource(configuration: .automotiveNavigation),
            access: CoreLocationAccess(),
            makeAPI: { url, tokens in
                try TrackerClient(baseURL: url, tokens: tokens, refresher: refresher)
            }
        )
    }
}
