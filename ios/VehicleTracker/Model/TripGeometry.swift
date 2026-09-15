import Foundation

/// One trip as `GET /api/v1/gtfs/trips/{trip_id}` serves it (spec §4.3): the
/// shape, the stops with absolute scheduled times, and the adherence
/// thresholds the server applies, so the phone judges by the same numbers.
nonisolated struct TripGeometry: Sendable, Codable, Equatable, Identifiable {
    var id: String
    var routeID: String
    var headsign: String
    var directionID: Int?
    var serviceDate: String
    var timezone: String
    var route: RouteSummary
    var shape: ShapePayload
    var stops: [TripStop]
    var thresholds: AdherenceThresholds

    /// The shape as coordinates; malformed pairs are dropped.
    var shapePoints: [GeoPoint] {
        shape.points.compactMap { $0.count == 2 ? GeoPoint($0[0], $0[1]) : nil }
    }

    private enum CodingKeys: String, CodingKey {
        case id, headsign, timezone, route, shape, stops, thresholds
        case routeID = "route_id"
        case directionID = "direction_id"
        case serviceDate = "service_date"
    }
}

nonisolated struct RouteSummary: Sendable, Codable, Equatable {
    var shortName: String
    var longName: String
    /// Hex without '#', as in routes.txt; empty when the feed gives none.
    var color: String
    var textColor: String

    private enum CodingKeys: String, CodingKey {
        case color
        case shortName = "short_name"
        case longName = "long_name"
        case textColor = "text_color"
    }
}

nonisolated struct ShapePayload: Sendable, Codable, Equatable {
    var lengthM: Double
    /// [lat, lon] pairs in shape order.
    var points: [[Double]]

    private enum CodingKeys: String, CodingKey {
        case points
        case lengthM = "length_m"
    }
}

nonisolated struct TripStop: Sendable, Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var sequence: Int
    var lat: Double
    var lon: Double
    var alongShapeM: Double
    var arrivalAt: Date
    var departureAt: Date

    var coordinate: GeoPoint { GeoPoint(lat, lon) }

    private enum CodingKeys: String, CodingKey {
        case id, name, sequence, lat, lon
        case alongShapeM = "along_shape_m"
        case arrivalAt = "arrival_at"
        case departureAt = "departure_at"
    }
}

nonisolated struct AdherenceThresholds: Sendable, Codable, Equatable {
    var maxShapeDistanceM: Double
    var scheduleEarlyS: Int
    var scheduleLateS: Int

    private enum CodingKeys: String, CodingKey {
        case maxShapeDistanceM = "max_shape_distance_m"
        case scheduleEarlyS = "schedule_early_s"
        case scheduleLateS = "schedule_late_s"
    }
}
