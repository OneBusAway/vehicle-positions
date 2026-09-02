import Foundation

/// A WGS-84 position.
public struct Coordinate: Sendable, Codable, Equatable, Hashable {
    public var latitude: Double
    public var longitude: Double

    public init(latitude: Double, longitude: Double) {
        self.latitude = latitude
        self.longitude = longitude
    }
}

/// Everything a host app must supply to report rides, plus the tunables that
/// govern how long a ride may run and when it gives up.
public struct RideReporterConfiguration: Sendable {
    /// Base URL of the vehicle-positions server (for example `https://example.org`).
    public var serverURL: URL
    /// Bundle identifier of the host app, sent at registration.
    public var appID: String
    /// Marketing version of the host app, sent at registration.
    public var appVersion: String
    /// A ride is ended once it has run this long.
    public var maxRideDuration: Duration = .seconds(3 * 3600)
    /// A ride is ended once the device has not moved meaningfully for this long.
    public var stationaryTimeout: Duration = .seconds(10 * 60)
    /// How close to the destination stop counts as having arrived.
    public var arrivalRadiusMeters: Double = 75
    /// How far the rider must travel before arrival detection is armed.
    public var minimumTravelBeforeArrivalMeters: Double = 200
    /// A ride is ended once uploads have been failing for this long.
    public var uploadFailureTimeout: Duration = .seconds(5 * 60)
    /// How long unsent samples are kept before being discarded.
    public var sampleRetention: Duration = .seconds(10 * 60)

    public init(serverURL: URL, appID: String, appVersion: String) {
        self.serverURL = serverURL
        self.appID = appID
        self.appVersion = appVersion
    }
}

/// The trip a rider says they are on, plus optional detail that helps the
/// server match and score the ride.
public struct TripDescriptor: Sendable, Codable, Equatable {
    public var tripID: String
    public var startDate: String?
    public var routeID: String?
    public var vehicleID: String?
    public var boardingStopID: String?
    public var destinationStopID: String?

    public init(
        tripID: String,
        startDate: String? = nil,
        routeID: String? = nil,
        vehicleID: String? = nil,
        boardingStopID: String? = nil,
        destinationStopID: String? = nil
    ) {
        self.tripID = tripID
        self.startDate = startDate
        self.routeID = routeID
        self.vehicleID = vehicleID
        self.boardingStopID = boardingStopID
        self.destinationStopID = destinationStopID
    }
}

/// How far the server has got in believing a ride.
public enum RideState: String, Sendable, Codable {
    case pending, verified, rejected
}

/// What a trusted feed had to say about the rider's reported positions.
public enum Corroboration: String, Sendable, Codable {
    case unavailable, none, corroborated, contradicted
}

/// Why a ride stopped. The first eight are the reasons a client may report;
/// the rest are decided by the server (spec §4.4).
public enum RideEndReason: String, Sendable, Codable, CaseIterable {
    case userRequested = "user_requested", arrived, stationary, maxDuration = "max_duration"
    case locationUnavailable = "location_unavailable", authorizationDenied = "authorization_denied"
    case networkFailure = "network_failure", appTerminated = "app_terminated"
    case offRoute = "off_route", contradicted, implausible, offSchedule = "off_schedule"
    case superseded, serverRestart = "server_restart", idle

    /// Reasons the server accepts from a client (spec §4.4).
    public var isClientReportable: Bool {
        switch self {
        case .userRequested, .arrived, .stationary, .maxDuration,
             .locationUnavailable, .authorizationDenied, .networkFailure, .appTerminated:
            true
        case .offRoute, .contradicted, .implausible, .offSchedule,
             .superseded, .serverRestart, .idle:
            false
        }
    }
}

/// The server's running verdict on an in-flight ride.
public struct RideProgress: Sendable, Equatable {
    public var state: RideState
    public var published: Bool
    public var corroboration: Corroboration
    public var pointsAccepted: Int
    public var offRouteStreak: Int

    public init(
        state: RideState,
        published: Bool,
        corroboration: Corroboration,
        pointsAccepted: Int,
        offRouteStreak: Int
    ) {
        self.state = state
        self.published = published
        self.corroboration = corroboration
        self.pointsAccepted = pointsAccepted
        self.offRouteStreak = offRouteStreak
    }
}

/// What a finished ride amounted to.
public struct RideSummary: Sendable, Codable, Equatable {
    public var points: Int
    public var matched: Int
    public var corroborated: Int
    public var durationSeconds: Int

    public init(points: Int, matched: Int, corroborated: Int, durationSeconds: Int) {
        self.points = points
        self.matched = matched
        self.corroborated = corroborated
        self.durationSeconds = durationSeconds
    }

    private enum CodingKeys: String, CodingKey {
        case points, matched, corroborated
        case durationSeconds = "duration_seconds"
    }
}

/// How a trip is currently being tracked, as the server sees it.
public struct TripStatus: Sendable, Codable, Equatable {
    public var tripID: String
    public var startDate: String
    public var trusted: Bool
    public var riderReported: Bool
    public var riders: Int

    public init(tripID: String, startDate: String, trusted: Bool, riderReported: Bool, riders: Int) {
        self.tripID = tripID
        self.startDate = startDate
        self.trusted = trusted
        self.riderReported = riderReported
        self.riders = riders
    }

    private enum CodingKeys: String, CodingKey {
        case tripID = "trip_id"
        case startDate = "start_date"
        case trusted
        case riderReported = "rider_reported"
        case riders
    }
}

/// A degraded condition the ride survived; the ride keeps running.
public enum RideWarning: Sendable, Equatable {
    case uploadRetrying(attempt: Int), accuracyLimited, insufficientlyInUse
}

/// Everything a ride tells its host app about, in order.
public enum RideEvent: Sendable, Equatable {
    case registered(riderID: String)
    case started(rideID: String)
    case progress(RideProgress)
    case warning(RideWarning)
    case ended(RideEndReason, summary: RideSummary?)
}

/// Why reporting a ride failed.
public enum RideError: Error, Sendable, Equatable {
    case notAuthorized
    case server(status: Int, message: String)
    case transport(String)
    case alreadyEnded
    case decoding(String)
    case notActive
}
