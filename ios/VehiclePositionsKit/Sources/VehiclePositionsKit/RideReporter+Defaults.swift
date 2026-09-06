#if os(iOS)
import Foundation

extension RideReporter {
    /// The reporter a host app wants: `URLSession` for the network, Core
    /// Location for positions, the Keychain for credentials.
    public init(configuration: RideReporterConfiguration) {
        self.init(
            configuration: configuration,
            transport: URLSessionRideTransport(),
            locationSource: CoreLocationSource(),
            credentialStore: KeychainCredentialStore()
        )
    }
}
#endif
