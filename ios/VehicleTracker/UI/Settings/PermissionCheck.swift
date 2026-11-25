import CoreGraphics

/// Permission check as it stands, and which of its rows have ticked: those it
/// saw iOS allow. A row that has shown allowed once does not tick again, as on
/// Before you start.
nonisolated struct PermissionCheck: Equatable {
    private(set) var readiness: Readiness
    private(set) var ticked: Set<LocationChecklist.Row> = []
    private var shownAllowed: Set<LocationChecklist.Row>

    init(_ readiness: Readiness) {
        self.readiness = readiness
        shownAllowed = Self.allowed(readiness)
    }

    mutating func read(_ new: Readiness) {
        readiness = new
        let allowed = Self.allowed(new)
        ticked.formUnion(allowed.subtracting(shownAllowed))
        shownAllowed.formUnion(allowed)
    }

    private static func allowed(_ readiness: Readiness) -> Set<LocationChecklist.Row> {
        Set(LocationChecklist.Row.allCases.filter { readiness.stop(for: $0) == .allowed })
    }

    /// The row the vehicle waits over, or the riders' after the last when a
    /// trip can start. The header's line has the vehicle on the stop before.
    static func waitsOver(_ readiness: Readiness) -> Int {
        min(readiness.vehicleAt, readiness.stops.count)
    }
}

/// Where the upright line and its vehicle stand, down from the top of its
/// card, by where its rows were laid out: the riders' row last.
nonisolated struct CheckPlaces: Equatable {
    /// Over the first row, where the vehicle waits when it can't get past it.
    static let home: CGFloat = 44

    /// Over each row, where the vehicle can wait.
    let over: [CGFloat]
    /// The middle of the riders' marker, where the line ends.
    let end: CGFloat

    init(rows: [CGRect]) {
        over = rows.enumerated().map { index, row in index == 0 ? row.minY - Self.home / 2 : row.minY }
        end = rows.last?.midY ?? 0
    }

    /// Where the vehicle is when it waits over the row at `index`.
    func vehicle(over index: Int) -> CGFloat {
        over.indices.contains(index) ? over[index] : 0
    }
}
