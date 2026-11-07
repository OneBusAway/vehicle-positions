import SwiftUI

/// Choose a route (DESIGN-iOS §4.3): the routes the driver drove last, then
/// every route as a row with its name, or as a grid of badges when the feed
/// names none of them, in Android's words and order (`RoutesScreen.kt`).
struct RoutesView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let vehicle: Vehicle
    @State private var load: PickerLoad<[RouteInfo]> = .loading
    /// Bumped by Check again and Try again, so the load task runs again.
    @State private var loadRequest = 0
    @State private var query = ""

    var body: some View {
        content
            .navigationTitle(PickerText.routesTitle)
            .navigationSubtitle(PickerText.vehicle(vehicle.id))
            .toolbar { AccountMenu() }
            .searchable(text: $query, prompt: PickerText.routeSearchPrompt)
            // One load per visit: Back finds the same rows at the same place.
            .task(id: loadRequest) {
                guard case .loading = load else { return }
                await load()
            }
    }

    @ViewBuilder private var content: some View {
        switch load {
        case .loading:
            DelayedProgress()
        case .failed(.noSchedule):
            // Trying again will not produce a schedule, so there is no button.
            EmptyState(symbol: "calendar", title: PickerText.noSchedule, detail: PickerText.noScheduleDetail)
        case .failed(.unreachable):
            EmptyState(symbol: "icloud.slash", title: PickerText.routesLoadFailed, detail: PickerText.checkConnection,
                       action: PickerText.tryAgain, onAction: loadAgain)
        case .loaded(let routes):
            if routes.isEmpty {
                EmptyState(symbol: "calendar.badge.exclamationmark", title: PickerText.noRoutes, detail: PickerText.askAdmin,
                           action: PickerText.checkAgain, onAction: loadAgain)
            } else {
                list(RouteList.of(routes, query: query, recentIDs: session.settings.recentRouteIDs))
            }
        }
    }

    @ViewBuilder private func list(_ model: RouteListModel) -> some View {
        if model.visible.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: PickerText.noRouteMatches(query), detail: PickerText.routeSearchHint)
        } else if model.grid {
            grid(model)
        } else {
            rows(model)
        }
    }

    private func rows(_ model: RouteListModel) -> some View {
        List {
            if !model.recent.isEmpty {
                Section(PickerText.recent) {
                    ForEach(PlacedRoute.recent(model.recent)) { row($0.route) }
                }
            }
            Section(query.isEmpty ? PickerText.allRoutes : PickerText.matches) {
                ForEach(PlacedRoute.all(model.visible)) { row($0.route) }
            }
        }
        .listStyle(.insetGrouped)
    }

    private func row(_ route: RouteInfo) -> some View {
        NavigationLink(value: PickerRoute.trips(vehicle, route)) {
            HStack(spacing: 16) {
                RouteBadge(shortName: route.shortName, color: route.color, textColor: route.textColor, size: .row)
                Text(route.longName)
                    .font(.body)
                    .lineLimit(2)
            }
            .frame(minHeight: 40)
        }
        .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
        .accessibilityLabel(PickerText.routeRow(route))
    }

    private func grid(_ model: RouteListModel) -> some View {
        ScrollView {
            LazyVGrid(columns: gridColumns, spacing: 10) {
                if !model.recent.isEmpty {
                    Section {
                        badges(PlacedRoute.recent(model.recent))
                    } header: {
                        PickerSectionHeader(title: PickerText.recent)
                    }
                }
                Section {
                    badges(PlacedRoute.all(model.visible))
                } header: {
                    // A grid of bare numbers has no names to scan, so its
                    // label says how many there are.
                    PickerSectionHeader(title: query.isEmpty ? PickerText.allRoutes(count: model.total) : PickerText.matches)
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .background(Color(.systemGroupedBackground))
    }

    private func badges(_ routes: [PlacedRoute]) -> some View {
        ForEach(routes) { placed in
            NavigationLink(value: PickerRoute.trips(vehicle, placed.route)) {
                RouteBadge(shortName: placed.route.shortName, color: placed.route.color, textColor: placed.route.textColor, size: .grid)
            }
            .accessibilityLabel(PickerText.route(PickerText.name(of: placed.route)))
        }
    }

    /// Four columns, two at accessibility sizes (Android's `GRID_COLUMNS`).
    private var gridColumns: [GridItem] {
        Array(repeating: GridItem(.flexible(), spacing: 10), count: dynamicTypeSize.isAccessibilitySize ? 2 : 4)
    }

    private func loadAgain() {
        load = .loading
        loadRequest += 1
    }

    private func load() async {
        do {
            load = .loaded(try await session.routes())
        } catch {
            // The view went away mid-load; the next visit loads afresh.
            guard !Task.isCancelled else { return }
            guard !session.handleIfUnauthorized(error) else { return }
            load = .failed(PickerLoadFailure(error))
        }
    }
}

/// A route under one section, with an id that says which: a recent route is
/// in All routes as well, and a lazy container needs one id per row.
private struct PlacedRoute: Identifiable {
    let id: String
    let route: RouteInfo

    static func recent(_ routes: [RouteInfo]) -> [PlacedRoute] {
        routes.map { PlacedRoute(id: "recent-\($0.id)", route: $0) }
    }

    static func all(_ routes: [RouteInfo]) -> [PlacedRoute] {
        routes.map { PlacedRoute(id: "all-\($0.id)", route: $0) }
    }
}
