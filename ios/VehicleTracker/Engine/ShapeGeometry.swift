import Foundation

/// A WGS84 coordinate in degrees.
nonisolated struct GeoPoint: Sendable, Equatable, Hashable {
    var lat: Double
    var lon: Double

    init(_ lat: Double, _ lon: Double) {
        self.lat = lat
        self.lon = lon
    }
}

/// Great-circle helpers: the same formulas and constants as the server's
/// `rider/shape.go`, so the phone's distances agree with the server's.
nonisolated enum Geo {
    static let earthRadiusM = 6_371_000.0
    /// Local equirectangular scale factor.
    static let metresPerDegree = 111_320.0

    static func distance(_ a: GeoPoint, _ b: GeoPoint) -> Double {
        let lat1 = rad(a.lat), lat2 = rad(b.lat)
        let dLat = lat2 - lat1
        let dLon = rad(b.lon - a.lon)
        let h = sin(dLat / 2) * sin(dLat / 2) + cos(lat1) * cos(lat2) * sin(dLon / 2) * sin(dLon / 2)
        return 2 * earthRadiusM * asin(min(1, sqrt(h)))
    }

    /// Initial bearing from a to b, degrees clockwise from north in [0, 360).
    static func initialBearing(_ a: GeoPoint, _ b: GeoPoint) -> Double {
        let lat1 = rad(a.lat), lat2 = rad(b.lat)
        let dLon = rad(b.lon - a.lon)
        let y = sin(dLon) * cos(lat2)
        let x = cos(lat1) * sin(lat2) - sin(lat1) * cos(lat2) * cos(dLon)
        let deg = atan2(y, x) * 180 / .pi
        return (deg + 360).truncatingRemainder(dividingBy: 360)
    }

    static func rad(_ deg: Double) -> Double { deg * .pi / 180 }
}

/// The result of projecting a point onto a shape.
nonisolated struct Projection: Sendable, Equatable {
    /// Metres from the shape start.
    var alongShape: Double
    /// Metres from the point to the closest point on the shape.
    var distanceToShape: Double
}

/// A polyline with precomputed cumulative distances; a port of the server's
/// `rider.ShapeGeom`, including its hinted projection that keeps loops and
/// out-and-backs from snapping to the wrong pass.
nonisolated struct ShapeGeometry: Sendable {
    /// Minimum width, in metres, of the band of local minima a hint may choose
    /// between. It lets a hint choose between passes of a loop that share a
    /// point, where the closest distance is near zero.
    static let hintCandidateBand = 30.0
    /// How much further behind the hint a candidate pass must be, relative to
    /// one ahead of it, before it wins: a vehicle moves forward along its trip.
    static let backwardHintWeight = 2.0

    let points: [GeoPoint]
    /// cumulative[i] = metres from points[0] to points[i].
    let cumulative: [Double]
    let length: Double

    private let origin: GeoPoint
    private let cosLat: Double
    private let localX: [Double]
    private let localY: [Double]

    /// Nil for fewer than two points, which is no shape at all.
    init?(points: [GeoPoint]) {
        guard points.count >= 2 else { return nil }
        self.points = points
        origin = points[0]
        let meanLat = points.reduce(0) { $0 + $1.lat } / Double(points.count)
        cosLat = cos(Geo.rad(meanLat))

        var cumulative = [Double](repeating: 0, count: points.count)
        for i in 1..<points.count {
            cumulative[i] = cumulative[i - 1] + Geo.distance(points[i - 1], points[i])
        }
        self.cumulative = cumulative
        length = cumulative[points.count - 1]

        var xs: [Double] = []
        var ys: [Double] = []
        xs.reserveCapacity(points.count)
        ys.reserveCapacity(points.count)
        for p in points {
            xs.append((p.lon - points[0].lon) * cosLat * Geo.metresPerDegree)
            ys.append((p.lat - points[0].lat) * Geo.metresPerDegree)
        }
        localX = xs
        localY = ys
    }

    /// The position of p along the shape. Without a hint the globally closest
    /// segment wins. With a hint (the previous along-shape distance) the local
    /// minimum nearest the hint wins instead, among those within
    /// max(2×best + 1, hintCandidateBand) metres of the best, with backwards
    /// distance weighted by backwardHintWeight.
    func project(_ p: GeoPoint, hint: Double?) -> Projection {
        let segments = points.count - 1
        let (px, py) = local(p)

        guard let hint else {
            var bestDist = Double.infinity
            var bestAlong = 0.0
            for i in 0..<segments {
                let (d, along) = projectOnto(i, px, py)
                if d < bestDist {
                    bestDist = d
                    bestAlong = along
                }
            }
            return Projection(alongShape: bestAlong, distanceToShape: bestDist)
        }

        var dists = [Double](repeating: 0, count: segments)
        var alongs = [Double](repeating: 0, count: segments)
        var best = 0
        for i in 0..<segments {
            (dists[i], alongs[i]) = projectOnto(i, px, py)
            if dists[i] < dists[best] { best = i }
        }

        var chosen = best
        let threshold = max(2 * dists[best] + 1, Self.hintCandidateBand)
        var closest = Double.infinity
        for i in 0..<segments {
            if dists[i] > threshold || !Self.isLocalMin(dists, i) { continue }
            var delta = alongs[i] - hint
            if delta < 0 { delta = -delta * Self.backwardHintWeight }
            if delta < closest {
                closest = delta
                chosen = i
            }
        }
        return Projection(alongShape: alongs[chosen], distanceToShape: dists[chosen])
    }

    /// The coordinate `along` metres into the shape, clamped to [0, length].
    func point(at along: Double) -> GeoPoint {
        let (i, t) = segment(at: along)
        let a = points[i], b = points[i + 1]
        return GeoPoint(a.lat + t * (b.lat - a.lat), a.lon + t * (b.lon - a.lon))
    }

    /// The bearing of the segment containing `along`.
    func bearing(at along: Double) -> Double {
        let (i, _) = segment(at: along)
        return Geo.initialBearing(points[i], points[i + 1])
    }

    /// Distance from the local-plane point to segment i, and the along-shape
    /// distance of the closest point on it.
    private func projectOnto(_ i: Int, _ px: Double, _ py: Double) -> (Double, Double) {
        let ax = localX[i], ay = localY[i]
        let dx = localX[i + 1] - ax, dy = localY[i + 1] - ay
        var t = 0.0
        let lenSq = dx * dx + dy * dy
        if lenSq > 0 {
            t = min(1, max(0, ((px - ax) * dx + (py - ay) * dy) / lenSq))
        }
        let dist = hypot(px - (ax + t * dx), py - (ay + t * dy))
        return (dist, cumulative[i] + t * (cumulative[i + 1] - cumulative[i]))
    }

    /// The index of the segment containing `along` and the fraction into it,
    /// clamping `along` to [0, length].
    private func segment(at along: Double) -> (Int, Double) {
        let last = points.count - 2
        if along >= length { return (last, 1) }
        if along <= 0 { return (0, 0) }
        for i in 0...last where along < cumulative[i + 1] {
            let segLen = cumulative[i + 1] - cumulative[i]
            return segLen <= 0 ? (i, 0) : (i, (along - cumulative[i]) / segLen)
        }
        return (last, 1)
    }

    /// p in metres in the shape's equirectangular projection.
    private func local(_ p: GeoPoint) -> (Double, Double) {
        ((p.lon - origin.lon) * cosLat * Geo.metresPerDegree,
         (p.lat - origin.lat) * Geo.metresPerDegree)
    }

    /// Whether dists[i] is no greater than its neighbours.
    private static func isLocalMin(_ dists: [Double], _ i: Int) -> Bool {
        if i > 0 && dists[i] > dists[i - 1] { return false }
        if i < dists.count - 1 && dists[i] > dists[i + 1] { return false }
        return true
    }
}
