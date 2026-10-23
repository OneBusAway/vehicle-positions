import Foundation

/// The trip in progress: what the server knows it as, and everything the
/// phone needs to keep judging adherence without asking again.
nonisolated struct ActiveTrip: Sendable, Codable, Equatable {
    var serverTripID: Int64
    var vehicle: Vehicle
    var trip: TripGeometry
    var startedAt: Date
    /// The driver who started it. Phones are handed on between shifts, so a
    /// relaunch only takes up a trip the driver now signed in started; see
    /// `TripSession.adoptStoredTrip`.
    var driverEmail: String
}

/// The end of a trip that was ended on this phone only, kept until the server
/// confirms it. `driverEmail` is the driver who started the trip, so only
/// their next start sends it.
nonisolated struct PendingTripEnd: Sendable, Codable, Equatable {
    var serverTripID: Int64
    var driverEmail: String
    /// The server signed in when the trip was ended locally, which is where
    /// End Trip would have sent it. Trip ids are numbered per server, so the
    /// end is sent there and nowhere else.
    var serverURL: String

    /// Whether `address` names the server this end is owed to. Case, a
    /// trailing slash or a written-out default port don't make it another
    /// server: a retyped address must not hold the end back, or the next start
    /// is refused again.
    func isOwed(to address: String) -> Bool {
        Self.serverKey(serverURL) == Self.serverKey(address)
    }

    private static func serverKey(_ address: String) -> String {
        guard let url = URL(string: address.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = url.scheme?.lowercased(), let host = url.host()?.lowercased() else { return address }
        let port = url.port ?? (scheme == "http" ? 80 : 443)
        let path = url.path(percentEncoded: false).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        return "\(host):\(port)/\(path)"
    }
}
