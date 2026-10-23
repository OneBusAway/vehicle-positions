import Foundation

/// Which server URLs the app will talk to. Every request carries a bearer
/// token or the driver's position, so plain HTTP is refused except to a
/// development server on this machine or the local network.
nonisolated enum ServerURLPolicy {
    static let explanation = "Use https://. Plain http:// is allowed only for localhost, 127.0.0.1 or a .local host."

    static func isAllowed(_ url: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(), let host = url.host()?.lowercased() else { return false }
        switch scheme {
        case "https":
            return true
        case "http":
            return host == "localhost" || host == "127.0.0.1" || host.hasSuffix(".local")
        default:
            return false
        }
    }

    /// An address the driver didn't type: the agency's managed configuration,
    /// the build default or a setup link. It must pass `isAllowed`, have a
    /// host, and carry no user info: `https://tracker.example.org@evil.example`
    /// goes to `evil.example`, and nobody reading it would think so.
    static func presetURL(from raw: String) -> URL? {
        guard let url = URL(string: raw), url.host()?.isEmpty == false,
              url.user() == nil, url.password() == nil, isAllowed(url) else { return nil }
        return url
    }
}
