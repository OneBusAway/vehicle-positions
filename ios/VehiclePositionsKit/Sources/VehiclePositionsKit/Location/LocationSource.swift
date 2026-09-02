import Foundation

/// One position reading. Measurements the device could not supply are `-1`,
/// matching Core Location's own convention.
public struct LocationFix: Sendable, Equatable {
    public var latitude: Double
    public var longitude: Double
    /// Metres, or `-1` when unknown.
    public var horizontalAccuracy: Double
    /// Metres per second, or `-1` when unknown.
    public var speed: Double
    /// Degrees clockwise from true north, or `-1` when unknown.
    public var course: Double
    public var timestamp: Date

    public init(
        latitude: Double,
        longitude: Double,
        horizontalAccuracy: Double,
        speed: Double,
        course: Double,
        timestamp: Date
    ) {
        self.latitude = latitude
        self.longitude = longitude
        self.horizontalAccuracy = horizontalAccuracy
        self.speed = speed
        self.course = course
        self.timestamp = timestamp
    }

    public var coordinate: Coordinate { Coordinate(latitude: latitude, longitude: longitude) }
}

/// Something wrong with location that the ride needs to know about. The first
/// two end a ride; the rest only degrade it.
public enum LocationDiagnostic: Sendable, Equatable {
    case authorizationDenied
    case locationUnavailable
    case accuracyLimited
    case insufficientlyInUse
}

/// One update from a ``LocationSource``: a fix, a diagnostic, or both.
public struct LocationSample: Sendable, Equatable {
    public var fix: LocationFix?
    /// True when the platform reports the device is not moving meaningfully.
    public var isStationary: Bool
    public var diagnostic: LocationDiagnostic?

    public init(fix: LocationFix?, isStationary: Bool = false, diagnostic: LocationDiagnostic? = nil) {
        self.fix = fix
        self.isStationary = isStationary
        self.diagnostic = diagnostic
    }
}

/// A claim on background execution, released by ``invalidate()``.
public protocol BackgroundActivityHandle: Sendable {
    func invalidate()
}

/// Where positions come from. `CoreLocationSource` is the shipping
/// implementation; tests substitute a fake.
public protocol LocationSource: Sendable {
    func updates() -> AsyncThrowingStream<LocationSample, any Error>
    func beginBackgroundActivity() -> any BackgroundActivityHandle
}
