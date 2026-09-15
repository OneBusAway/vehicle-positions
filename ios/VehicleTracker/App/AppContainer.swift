import Foundation
import VehiclePositionsKit

/// Builds the app's long-lived objects once and wires them together. The
/// token closure reads the Keychain on every request, so a re-login mid-trip
/// takes effect on the next report.
@MainActor
final class AppContainer {
    let settings: AppSettings
    let session: TripSession

    init() {
        let settings = AppSettings()
        let tokens = KeychainTokenStore()
        self.settings = settings
        session = TripSession(
            settings: settings,
            tokens: tokens,
            store: FileActiveTripStore(),
            locations: CoreLocationSource(configuration: .automotiveNavigation),
            makeAPI: { url, tokens in
                try TrackerClient(baseURL: url, token: { (try? tokens.load())?.token })
            }
        )
    }
}
