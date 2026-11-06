import Foundation

/// Where the vehicle sits on the map while following, from Android's
/// `FollowPadding.kt` and `FollowZoom.kt`: a third of the way up the part of
/// the map that the status card and the trip sheet leave uncovered, with
/// 500 m of road between it and the top of that part. Points here; the map
/// turns them into a camera by measuring what a point covers.
struct FollowFraming: Equatable {
    /// How much road the map keeps between the vehicle and the top of the
    /// uncovered part.
    static let roadAheadMetres = 500.0

    let mapHeight: Double
    /// Points at the top of the map the card draws over.
    let coveredTop: Double
    /// Points at the bottom the sheet draws over.
    let coveredBottom: Double

    var uncoveredHeight: Double {
        max(0, mapHeight - coveredTop - coveredBottom)
    }

    /// Points from the vehicle to the top of the uncovered part: two thirds of it.
    var pointsAhead: Double {
        uncoveredHeight * 2 / 3
    }

    /// The vehicle's distance from the top of the map.
    var vehicleY: Double {
        coveredTop + pointsAhead
    }

    /// How far below the map's centre the vehicle sits. The camera centres
    /// that far ahead of it, so the road ahead fills the uncovered part.
    var vehicleOffsetFromCentre: Double {
        vehicleY - mapHeight / 2
    }

    /// The scale that puts the road ahead across the points ahead, for the
    /// first follow of a trip. A map not yet measured asks for the widest
    /// view rather than dividing by zero.
    var metresPerPoint: Double {
        Self.roadAheadMetres / max(pointsAhead, 1)
    }
}
