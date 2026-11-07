import SwiftUI

/// Choose your vehicle (DESIGN-iOS §4.2): the driver's vehicles as big tiles
/// under Favorites, Recent and All vehicles, or under Matches while they
/// search, in Android's words and order (`VehicleScreen.kt`).
struct VehiclesView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding var path: [PickerRoute]
    /// Bumped when the list has to be loaded again on a return here; see
    /// `PickerFlow`.
    let reload: Int
    @State private var load: PickerLoad<[Vehicle]> = .loading
    /// Bumped by Check again and Try again, so the load task runs again.
    @State private var loadRequest = 0
    @State private var query = ""
    /// This visit to the pickers has skipped past the vehicle screen already.
    @State private var skippedOnce = false

    var body: some View {
        content
            .navigationTitle(PickerText.vehiclesTitle)
            .toolbar { AccountMenu() }
            .searchable(text: $query, prompt: PickerText.vehicleSearchPrompt)
            // One load per visit: Back finds the same tiles at the same place.
            // Pull to refresh and the two buttons load again on purpose.
            .task(id: loadRequest) {
                guard case .loading = load else { return }
                await load()
            }
            // Change vehicle: the vehicles changed under the list, so it is
            // loaded again, and skips again if one is all that is left, as
            // Android's bus step does when it is reached that way.
            .onChange(of: reload) {
                skippedOnce = false
                loadAgain()
            }
            .sensoryFeedback(.selection, trigger: session.settings.favoriteVehicleIDs)
    }

    @ViewBuilder private var content: some View {
        switch load {
        case .loading:
            DelayedProgress()
        case .failed:
            EmptyState(symbol: "icloud.slash", title: PickerText.vehiclesLoadFailed, detail: PickerText.checkConnection,
                       action: PickerText.tryAgain, onAction: loadAgain)
        case .loaded(let vehicles):
            if vehicles.isEmpty {
                EmptyState(symbol: "bus", title: PickerText.noVehicles, detail: PickerText.askDispatcher,
                           action: PickerText.checkAgain, onAction: loadAgain)
            } else {
                tiles(vehicles)
            }
        }
    }

    @ViewBuilder private func tiles(_ vehicles: [Vehicle]) -> some View {
        let settings = session.settings
        let groups = VehicleGroups.of(vehicles, query: query, favorites: settings.favoriteVehicleIDs, recents: settings.recentVehicleIDs)
        if groups.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: PickerText.noVehicleMatches(query), detail: PickerText.checkFleetNumber)
        } else {
            ScrollView {
                LazyVGrid(columns: columns, spacing: VehicleTile.gap) {
                    ForEach(groups) { group in
                        Section {
                            ForEach(PlacedTile.in(group)) { placed in
                                VehicleTile(tile: placed.tile) {
                                    path.append(.routes(placed.tile.vehicle))
                                } onToggleFavorite: {
                                    toggleFavorite(placed.tile.vehicle.id)
                                }
                            }
                        } header: {
                            PickerSectionHeader(title: PickerText.section(group.section))
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 24)
            }
            .background(Color(.systemGroupedBackground))
            .refreshable { await load() }
        }
    }

    /// Two columns, one at accessibility sizes so a five-digit fleet number
    /// still fits on a line.
    private var columns: [GridItem] {
        Array(repeating: GridItem(.flexible(), spacing: VehicleTile.gap), count: dynamicTypeSize.isAccessibilitySize ? 1 : 2)
    }

    private func toggleFavorite(_ id: String) {
        withAnimation(reduceMotion ? nil : .smooth(duration: 0.25)) {
            session.settings.toggleFavorite(id)
        }
    }

    private func loadAgain() {
        load = .loading
        loadRequest += 1
    }

    private func load() async {
        do {
            try await session.loadVehicles()
            load = .loaded(session.vehicles)
            if let only = VehicleAutoSkip.vehicle(toSkipTo: session.vehicles, alreadySkipped: skippedOnce,
                                                  pathIsEmpty: path.isEmpty) {
                skippedOnce = true
                path = [.routes(only)]
            }
        } catch {
            // The view went away mid-load; the next visit loads afresh.
            guard !Task.isCancelled else { return }
            guard !session.handleIfUnauthorized(error) else { return }
            load = .failed(PickerLoadFailure(error))
        }
    }
}

/// One vehicle is no choice, so the pickers go straight to its routes, but
/// only once per visit: a pull to refresh or a Check again would otherwise
/// push the routes back over the vehicle screen, so the driver could never
/// stay on it.
nonisolated enum VehicleAutoSkip {
    static func vehicle(toSkipTo vehicles: [Vehicle], alreadySkipped: Bool, pathIsEmpty: Bool) -> Vehicle? {
        guard vehicles.count == 1, !alreadySkipped, pathIsEmpty else { return nil }
        return vehicles[0]
    }
}

/// A tile under its section, with an id that says which. A starred or
/// unstarred tile changes section, and a lazy grid keeps the old view of an
/// id that merely moved, star and all; under a new id the move is a removal
/// and an insertion, so the tile is drawn as it now is.
private struct PlacedTile: Identifiable {
    let id: String
    let tile: VehicleTileModel

    static func `in`(_ group: VehicleGroup) -> [PlacedTile] {
        group.tiles.map { PlacedTile(id: "\(group.section)-\($0.id)", tile: $0) }
    }
}
