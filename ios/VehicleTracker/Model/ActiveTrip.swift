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
