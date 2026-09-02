import Foundation

/// Great-circle distance. The ride reporter only ever measures a few kilometres
/// at a time, where the haversine formula on a spherical Earth is accurate to
/// well under a metre — no need for Core Location or a geodesic solver.
enum GeoDistance {
    /// Mean Earth radius (IUGG), in metres.
    private static let earthRadiusMeters = 6_371_008.8

    static func meters(from a: Coordinate, to b: Coordinate) -> Double {
        let lat1 = a.latitude * .pi / 180
        let lat2 = b.latitude * .pi / 180
        let deltaLat = (b.latitude - a.latitude) * .pi / 180
        let deltaLon = (b.longitude - a.longitude) * .pi / 180

        let h = pow(sin(deltaLat / 2), 2) + cos(lat1) * cos(lat2) * pow(sin(deltaLon / 2), 2)
        return 2 * earthRadiusMeters * asin(min(1, sqrt(h)))
    }
}
