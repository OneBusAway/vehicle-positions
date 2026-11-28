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
    /// The run or train number (GTFS's `trip_short_name`), "" when the feed gives none.
    var shortName: String = ""
    /// The block the run belongs to (GTFS's `block_id`), "" when the feed gives none.
    var blockID: String = ""

    private enum CodingKeys: String, CodingKey {
        case id, headsign
        case directionID = "direction_id"
        case startsAt = "starts_at"
        case endsAt = "ends_at"
        case firstStop = "first_stop"
        case lastStop = "last_stop"
        case shortName = "short_name"
        case blockID = "block_id"
    }
}

extension TripSummary {
    /// In an extension, so the memberwise initializer stays. A server older
    /// than #232 sends neither `short_name` nor `block_id`, and its runs
    /// decode with both "".
    nonisolated init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        headsign = try container.decode(String.self, forKey: .headsign)
        directionID = try container.decodeIfPresent(Int.self, forKey: .directionID)
        startsAt = try container.decode(Date.self, forKey: .startsAt)
        endsAt = try container.decode(Date.self, forKey: .endsAt)
        firstStop = try container.decode(String.self, forKey: .firstStop)
        lastStop = try container.decode(String.self, forKey: .lastStop)
        shortName = try container.decodeIfPresent(String.self, forKey: .shortName) ?? ""
        blockID = try container.decodeIfPresent(String.self, forKey: .blockID) ?? ""
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
    /// The GTFS route_id. With `startDate` it lets a consumer match the trip
    /// by route, and tell apart a trip id that repeats on another day.
    var routeID: String? = nil
    /// The trip's service date, YYYYMMDD in the agency's local time.
    var startDate: String? = nil
    /// How full the driver says the vehicle is. The feed takes it from the
    /// latest report alone, so a report without it clears it there.
    var occupancyStatus: Occupancy? = nil

    private enum CodingKeys: String, CodingKey {
        case latitude, longitude, bearing, speed, accuracy, timestamp
        case vehicleID = "vehicle_id"
        case tripID = "trip_id"
        case routeID = "route_id"
        case startDate = "start_date"
        case occupancyStatus = "occupancy_status"
    }
}

/// `POST /api/v1/locations/history`: reports the phone kept while it could
/// not reach the server, stored for location history only.
nonisolated struct LocationHistoryRequest: Sendable, Encodable, Equatable {
    var vehicleID: String
    var points: [HistoryPoint]

    private enum CodingKeys: String, CodingKey {
        case points
        case vehicleID = "vehicle_id"
    }
}

extension LocationHistoryRequest {
    /// `reports`, every one of them for `vehicleID`, as one upload.
    nonisolated init(vehicleID: String, reports: [LocationReport]) {
        self.init(vehicleID: vehicleID, points: reports.map(HistoryPoint.init))
    }
}

/// One report in a history upload: a `LocationReport` without the vehicle,
/// which the upload names once, and without the occupancy, which the
/// endpoint does not take. It refuses a field it does not know with a 400,
/// and the whole batch would be dropped.
nonisolated struct HistoryPoint: Sendable, Encodable, Equatable {
    var tripID: String
    var routeID: String?
    var startDate: String?
    var latitude: Double
    var longitude: Double
    var bearing: Double?
    var speed: Double?
    var accuracy: Double?
    var timestamp: Int64

    private enum CodingKeys: String, CodingKey {
        case latitude, longitude, bearing, speed, accuracy, timestamp
        case tripID = "trip_id"
        case routeID = "route_id"
        case startDate = "start_date"
    }
}

extension HistoryPoint {
    nonisolated init(_ report: LocationReport) {
        self.init(tripID: report.tripID, routeID: report.routeID, startDate: report.startDate,
                  latitude: report.latitude, longitude: report.longitude, bearing: report.bearing,
                  speed: report.speed, accuracy: report.accuracy, timestamp: report.timestamp)
    }
}

nonisolated struct LoginRequest: Sendable, Encodable {
    var email: String
    var password: String
}

/// What the server hands out at sign-in and at every refresh. The reply also
/// carries `token`, a deprecated copy of `access_token` that is not read.
nonisolated struct AuthTokens: Sendable, Decodable, Equatable {
    var accessToken: String
    var refreshToken: String
    /// Seconds from the reply until `accessToken` expires.
    var expiresIn: Int

    private enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case expiresIn = "expires_in"
    }
}

/// `POST /api/v1/auth/password`. The refresh token is this phone's own: the
/// server keeps it good while it ends every other sign-in of the driver.
nonisolated struct ChangePasswordRequest: Sendable, Encodable {
    var currentPassword: String
    var newPassword: String
    var refreshToken: String

    private enum CodingKeys: String, CodingKey {
        case currentPassword = "current_password"
        case newPassword = "new_password"
        case refreshToken = "refresh_token"
    }
}

nonisolated struct RefreshRequest: Sendable, Encodable {
    var refreshToken: String

    private enum CodingKeys: String, CodingKey {
        case refreshToken = "refresh_token"
    }
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

/// What the agency sets for its drivers (`GET /api/v1/agency`). The privacy
/// policy link is nil when it has none.
nonisolated struct AgencyInfo: Sendable, Decodable, Equatable {
    var privacyPolicyURL: String?

    private enum CodingKeys: String, CodingKey {
        case privacyPolicyURL = "privacy_policy_url"
    }
}

/// A page of the driver's own trips (`GET /api/v1/trips`), newest first.
nonisolated struct MyTripsPage: Sendable, Decodable, Equatable {
    var hasMore: Bool
    var trips: [MyTrip]

    private enum CodingKeys: String, CodingKey {
        case trips
        case hasMore = "has_more"
    }
}

/// One of the driver's own trips, as much of it as My trips shows. `route`
/// and `headsign` are the schedule's as it is now, so a trip keeps them only
/// while the schedule still has its route and its trip.
nonisolated struct MyTrip: Sendable, Decodable, Equatable, Identifiable {
    var id: Int64
    var vehicleID: String
    /// Empty for a trip started with its GTFS trip alone.
    var routeID: String
    var route: Route?
    /// Empty when the schedule gives none.
    var headsign: String
    var startTime: Date
    /// Nil while the trip runs.
    var endTime: Date?

    nonisolated struct Route: Sendable, Decodable, Equatable {
        var shortName: String
        var longName: String
        var color: String
        var textColor: String

        private enum CodingKeys: String, CodingKey {
            case color
            case shortName = "short_name"
            case longName = "long_name"
            case textColor = "text_color"
        }
    }

    private enum CodingKeys: String, CodingKey {
        case id, route, headsign
        case vehicleID = "vehicle_id"
        case routeID = "route_id"
        case startTime = "start_time"
        case endTime = "end_time"
    }
}

/// Why a request failed. The screens say each failure in their own sentence
/// (`SignInForm`, `PickerLoad`, `StartRefusal`); the description is the
/// app's words for any other place, in the driver's language. It never
/// carries the server's `error` message, which is written for the API and in
/// English, nor the system's or Swift's.
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
            SignInText.useHTTPS
        case .status(let code, _):
            String(localized: "Server error \(String(code))", comment: "Shown only where no screen has its own words for a request the server turned down. The HTTP status code it answered with, shown as it is, e.g. 'Server error 502'.")
        case .transport:
            PickerText.cannotReachServer
        case .decoding:
            String(localized: "The server's reply could not be read.", comment: "Shown only where no screen has its own words for a reply the app could not read, such as a Wi-Fi sign-in page answering in place of the server.")
        }
    }
}
