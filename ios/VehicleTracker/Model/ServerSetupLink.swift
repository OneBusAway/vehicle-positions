import Foundation

/// `obavehicletracker://setup?server=<percent-encoded URL>`, sent by the
/// agency as a link or a QR code so drivers don't type the server address.
/// Anyone can send a driver a link, so one only ever fills in the sign-in
/// screen's server, after the driver agrees; it never signs in.
nonisolated enum ServerSetupLink {
    static let scheme = "obavehicletracker"

    nonisolated enum Decision: Equatable {
        /// Ask "Use <host>?" and fill in the server only if the driver agrees.
        /// The title names the host, not the whole address, so the driver
        /// reads where the app would go; `host()` keeps a look-alike host
        /// percent-encoded rather than showing it as the name it imitates.
        case confirm(server: URL, host: String)
        /// A signed-in phone keeps its server: its token and trip belong there.
        case refusedSignedIn(host: String)
        case refusedManaged
        case invalid
    }

    /// The server the link names, if it is one the app may use. Only the host
    /// `setup` with the single query item `server` is a setup link.
    static func parse(_ link: URL) -> URL? {
        guard link.scheme?.lowercased() == scheme, link.host()?.lowercased() == "setup",
              let items = URLComponents(url: link, resolvingAgainstBaseURL: false)?.queryItems,
              items.count == 1, items[0].name == "server", let raw = items[0].value else { return nil }
        return ServerURLPolicy.presetURL(from: raw)
    }

    /// `signedInTo` is the session's server, or nil when signed out.
    static func decide(_ link: URL, signedInTo current: URL?, isManaged: Bool) -> Decision {
        if let current { return .refusedSignedIn(host: current.host() ?? "") }
        if isManaged { return .refusedManaged }
        guard let server = parse(link), let host = server.host() else { return .invalid }
        return .confirm(server: server, host: host)
    }
}
