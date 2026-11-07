import Foundation

/// Where a vehicle sits on Choose your vehicle. While the driver searches,
/// every match is under `matches`.
nonisolated enum VehicleSection: Equatable {
    case favorites, recent, all, matches
}

/// One vehicle as its tile draws it: the fleet number big, and `label` under
/// it, or nil when the label is blank or only repeats the number.
nonisolated struct VehicleTileModel: Equatable, Identifiable {
    let vehicle: Vehicle
    let label: String?
    let favorite: Bool

    var id: String { vehicle.id }
}

nonisolated struct VehicleGroup: Equatable, Identifiable {
    let section: VehicleSection
    let tiles: [VehicleTileModel]

    var id: VehicleSection { section }
}

/// A port of Android's `groupsOf` and `orderForDisplay` (`VehicleViewModel.kt`),
/// so the two apps put the same vehicle in the same place.
nonisolated enum VehicleGroups {
    /// Favorites, then the recently used ones that are not favourites, then
    /// the rest, each vehicle in one section only and empty sections left out.
    /// While the driver searches, the matches go under one section in the
    /// same order.
    static func of(_ vehicles: [Vehicle], query: String, favorites: Set<String>, recents: [String]) -> [VehicleGroup] {
        let ordered = orderForDisplay(vehicles.filter { $0.matches(query) }, favorites: favorites, recents: recents)
        func group(_ section: VehicleSection, _ list: [Vehicle]) -> VehicleGroup? {
            guard !list.isEmpty else { return nil }
            return VehicleGroup(section: section, tiles: list.map { $0.tile(favorite: favorites.contains($0.id)) })
        }
        if !query.isEmpty { return [group(.matches, ordered)].compactMap { $0 } }
        let favorite = ordered.filter { favorites.contains($0.id) }
        let rest = ordered.filter { !favorites.contains($0.id) }
        let recent = rest.filter { recents.contains($0.id) }
        let others = rest.filter { !recents.contains($0.id) }
        return [group(.favorites, favorite), group(.recent, recent), group(.all, others)].compactMap { $0 }
    }

    /// Favourites first, then recently used newest first, then everything
    /// else, with label and then id breaking ties. A total order, so the grid
    /// never reshuffles between two draws of the same list.
    private static func orderForDisplay(_ vehicles: [Vehicle], favorites: Set<String>, recents: [String]) -> [Vehicle] {
        func rank(_ vehicle: Vehicle) -> (Int, Int, String, String) {
            let group = favorites.contains(vehicle.id) ? 0 : recents.contains(vehicle.id) ? 1 : 2
            // recents is newest first, so a vehicle's index in it is its
            // recency rank; outside that group the key is constant.
            let recency = group == 1 ? recents.firstIndex(of: vehicle.id) ?? 0 : 0
            return (group, recency, vehicle.label, vehicle.id)
        }
        return vehicles.sorted { rank($0) < rank($1) }
    }
}

private extension Vehicle {
    /// Case-insensitive substring on id or label, as the admin vehicle list
    /// searches server-side. An empty query matches everything.
    nonisolated func matches(_ query: String) -> Bool {
        query.isEmpty || id.range(of: query, options: .caseInsensitive) != nil
            || label.range(of: query, options: .caseInsensitive) != nil
    }

    nonisolated func tile(favorite: Bool) -> VehicleTileModel {
        let says = !label.trimmingCharacters(in: .whitespaces).isEmpty && label != id
        return VehicleTileModel(vehicle: self, label: says ? label : nil, favorite: favorite)
    }
}
