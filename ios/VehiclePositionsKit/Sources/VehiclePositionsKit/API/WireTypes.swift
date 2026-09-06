import Foundation

// The rider API speaks snake_case JSON (spec §4.3–4.4). Every key that is not
// already its property's name is spelled out here rather than left to a
// key-encoding strategy, so the wire format is readable in the type and cannot
// drift with encoder configuration.

/// `POST /api/v1/rider/register` body.
public struct RegisterRequest: Codable, Sendable, Equatable {
    public var installationID: String
    public var platform: String
    public var appID: String
    public var appVersion: String
    /// Always `nil` in v1, but encoded as an explicit `null` the server can see.
    public var attestation: String?

    public init(installationID: String, platform: String, appID: String, appVersion: String, attestation: String? = nil) {
        self.installationID = installationID
        self.platform = platform
        self.appID = appID
        self.appVersion = appVersion
        self.attestation = attestation
    }

    private enum CodingKeys: String, CodingKey {
        case installationID = "installation_id"
        case platform
        case appID = "app_id"
        case appVersion = "app_version"
        case attestation
    }

    public func encode(to encoder: any Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(installationID, forKey: .installationID)
        try container.encode(platform, forKey: .platform)
        try container.encode(appID, forKey: .appID)
        try container.encode(appVersion, forKey: .appVersion)
        if let attestation {
            try container.encode(attestation, forKey: .attestation)
        } else {
            try container.encodeNil(forKey: .attestation)
        }
    }
}

/// `POST /api/v1/rider/register` response.
public struct RegisterResponse: Codable, Sendable, Equatable {
    public var riderID: String
    public var token: String
    public var reportIntervalSeconds: Int
    public var maxBatchSize: Int

    public init(riderID: String, token: String, reportIntervalSeconds: Int, maxBatchSize: Int) {
        self.riderID = riderID
        self.token = token
        self.reportIntervalSeconds = reportIntervalSeconds
        self.maxBatchSize = maxBatchSize
    }

    private enum CodingKeys: String, CodingKey {
        case riderID = "rider_id"
        case token
        case reportIntervalSeconds = "report_interval_seconds"
        case maxBatchSize = "max_batch_size"
    }
}

/// The destination stop the server resolved for a ride, when it knows one.
public struct DestinationInfo: Codable, Sendable, Equatable {
    public var stopID: String
    public var latitude: Double
    public var longitude: Double

    public init(stopID: String, latitude: Double, longitude: Double) {
        self.stopID = stopID
        self.latitude = latitude
        self.longitude = longitude
    }

    public var coordinate: Coordinate { Coordinate(latitude: latitude, longitude: longitude) }

    private enum CodingKeys: String, CodingKey {
        case stopID = "stop_id"
        case latitude, longitude
    }
}

/// `POST /api/v1/rider/rides` response.
public struct StartRideResponse: Codable, Sendable, Equatable {
    public var rideID: String
    public var state: RideState
    public var reportIntervalSeconds: Int
    public var maxBatchSize: Int
    public var destination: DestinationInfo?

    public init(
        rideID: String,
        state: RideState,
        reportIntervalSeconds: Int,
        maxBatchSize: Int,
        destination: DestinationInfo? = nil
    ) {
        self.rideID = rideID
        self.state = state
        self.reportIntervalSeconds = reportIntervalSeconds
        self.maxBatchSize = maxBatchSize
        self.destination = destination
    }

    private enum CodingKeys: String, CodingKey {
        case rideID = "ride_id"
        case state
        case reportIntervalSeconds = "report_interval_seconds"
        case maxBatchSize = "max_batch_size"
        case destination
    }
}

/// One reported position. Measurements the device could not supply are omitted.
public struct PositionUpload: Codable, Sendable, Equatable {
    public var latitude: Double
    public var longitude: Double
    public var accuracy: Double?
    public var speed: Double?
    public var bearing: Double?
    /// Seconds since the Unix epoch.
    public var timestamp: Int

    public init(
        latitude: Double,
        longitude: Double,
        accuracy: Double? = nil,
        speed: Double? = nil,
        bearing: Double? = nil,
        timestamp: Int
    ) {
        self.latitude = latitude
        self.longitude = longitude
        self.accuracy = accuracy
        self.speed = speed
        self.bearing = bearing
        self.timestamp = timestamp
    }
}

/// `POST /api/v1/rider/rides/{id}/positions` body.
public struct PositionsRequest: Codable, Sendable, Equatable {
    public var positions: [PositionUpload]

    public init(positions: [PositionUpload]) {
        self.positions = positions
    }
}

/// `POST /api/v1/rider/rides/{id}/positions` response.
public struct PositionsResponse: Codable, Sendable, Equatable {
    public var state: RideState
    public var published: Bool
    public var corroboration: Corroboration
    public var accepted: Int
    public var ignored: Int
    public var offRouteStreak: Int
    public var ended: Bool
    /// Empty while the ride is still running.
    public var endReason: String

    public init(
        state: RideState,
        published: Bool,
        corroboration: Corroboration,
        accepted: Int,
        ignored: Int,
        offRouteStreak: Int,
        ended: Bool,
        endReason: String
    ) {
        self.state = state
        self.published = published
        self.corroboration = corroboration
        self.accepted = accepted
        self.ignored = ignored
        self.offRouteStreak = offRouteStreak
        self.ended = ended
        self.endReason = endReason
    }

    /// The parsed end reason, or `nil` when the server sent none.
    public var endReasonValue: RideEndReason? {
        endReason.isEmpty ? nil : RideEndReason(rawValue: endReason)
    }

    private enum CodingKeys: String, CodingKey {
        case state, published, corroboration, accepted, ignored
        case offRouteStreak = "off_route_streak"
        case ended
        case endReason = "end_reason"
    }
}

/// `POST /api/v1/rider/rides/{id}/end` body.
public struct EndRideRequest: Codable, Sendable, Equatable {
    public var reason: RideEndReason

    public init(reason: RideEndReason) {
        self.reason = reason
    }
}

/// `POST /api/v1/rider/rides/{id}/end` response.
public struct EndRideResponse: Codable, Sendable, Equatable {
    public var status: String
    public var summary: RideSummary

    public init(status: String, summary: RideSummary) {
        self.status = status
        self.summary = summary
    }
}

/// The body the server returns with a non-2xx status.
public struct ServerErrorBody: Codable, Sendable {
    public var error: String

    public init(error: String) {
        self.error = error
    }
}

/// JSON for the rider API. Keys are sorted so encoded bodies are stable.
public enum RiderAPICodec {
    public static func encode<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return try encoder.encode(value)
    }

    public static func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        try JSONDecoder().decode(type, from: data)
    }
}
