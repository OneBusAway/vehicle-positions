import Foundation

/// A vehicle the signed-in driver is assigned to (`GET /api/v1/vehicles`).
nonisolated struct Vehicle: Sendable, Codable, Equatable, Identifiable, Hashable {
    var id: String
    var label: String
}

/// One route from the catalog (`GET /api/v1/gtfs/routes`).
nonisolated struct RouteInfo: Sendable, Codable, Equatable, Identifiable, Hashable {
    var id: String
    var shortName: String
    var longName: String
    var color: String
    var textColor: String
    var type: Int

    private enum CodingKeys: String, CodingKey {
        case id, color, type
        case shortName = "short_name"
        case longName = "long_name"
        case textColor = "text_color"
    }
}

extension Array where Element == RouteInfo {
    /// The routes named by `ids`, in the order given, skipping any the
    /// catalog no longer has. Recently driven routes are remembered as bare
    /// ids, so both pickers have to resolve them against what is on offer.
    nonisolated func matching(ids: [String]) -> [RouteInfo] {
        ids.compactMap { id in first { $0.id == id } }
    }
}

/// One run of a route on a service date (`GET /api/v1/gtfs/routes/{id}/trips`).
nonisolated struct TripSummary: Sendable, Codable, Equatable, Identifiable, Hashable {
    var id: String
    var headsign: String
    var directionID: Int?
    var startsAt: Date
    var endsAt: Date
    var firstStop: String
    var lastStop: String

    private enum CodingKeys: String, CodingKey {
        case id, headsign
        case directionID = "direction_id"
        case startsAt = "starts_at"
        case endsAt = "ends_at"
        case firstStop = "first_stop"
        case lastStop = "last_stop"
    }
}

nonisolated struct RouteTripsPage: Sendable, Codable, Equatable {
    var routeID: String
    var serviceDate: String
    var timezone: String
    var trips: [TripSummary]

    private enum CodingKeys: String, CodingKey {
        case timezone, trips
        case routeID = "route_id"
        case serviceDate = "service_date"
    }
}

/// One position as `POST /api/v1/locations` takes it. Absent measurements
/// are omitted, not sent as null: the server rejects unknown fields and
/// treats a missing one as "not reported".
nonisolated struct LocationReport: Sendable, Codable, Equatable {
    var vehicleID: String
    var tripID: String
    var latitude: Double
    var longitude: Double
    var bearing: Double?
    var speed: Double?
    var accuracy: Double?
    /// Unix seconds of the fix.
    var timestamp: Int64

    private enum CodingKeys: String, CodingKey {
        case latitude, longitude, bearing, speed, accuracy, timestamp
        case vehicleID = "vehicle_id"
        case tripID = "trip_id"
    }
}

nonisolated struct LoginRequest: Sendable, Encodable {
    var email: String
    var password: String
}

nonisolated struct LoginResponse: Sendable, Decodable {
    var token: String
}

nonisolated struct StartTripRequest: Sendable, Encodable {
    var vehicleID: String
    var routeID: String
    var gtfsTripID: String

    private enum CodingKeys: String, CodingKey {
        case vehicleID = "vehicle_id"
        case routeID = "route_id"
        case gtfsTripID = "gtfs_trip_id"
    }
}

nonisolated struct StartTripResponse: Sendable, Decodable {
    var id: Int64
}

nonisolated struct EndTripRequest: Sendable, Encodable {
    var tripID: Int64

    private enum CodingKeys: String, CodingKey {
        case tripID = "trip_id"
    }
}

nonisolated struct RoutesResponse: Sendable, Decodable {
    var routes: [RouteInfo]
}

nonisolated struct ErrorBody: Sendable, Decodable {
    var error: String
}

/// Why a request failed. The descriptions are what the driver reads: the
/// screens show `error.localizedDescription` as it comes, so every case has to
/// stand on its own without the enum's name.
nonisolated enum APIError: Error, Equatable, LocalizedError {
    /// The base URL fails ``ServerURLPolicy``.
    case insecureURL
    /// A non-2xx response, with the server's `error` message when it sent one.
    case status(Int, message: String)
    case transport(String)
    case decoding(String)

    var errorDescription: String? {
        switch self {
        case .insecureURL:
            ServerURLPolicy.explanation
        case .status(let code, let message):
            // The server writes these for the driver ("driver already has an
            // active trip"); only fall back to the code when it sent nothing.
            message.isEmpty ? "Server error \(code)" : message
        case .transport(let message):
            "Could not reach the server: \(message)"
        case .decoding:
            // The underlying decoding error names Swift types, which tells a
            // driver nothing.
            "The server's reply could not be read."
        }
    }
}
