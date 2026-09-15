import SwiftUI

struct RoutesView: View {
    @Environment(TripSession.self) private var session
    let vehicle: Vehicle
    @State private var routes: [RouteInfo] = []
    @State private var search = ""
    @State private var error: String?

    private var recent: [RouteInfo] {
        session.settings.recentRouteIDs.compactMap { id in routes.first { $0.id == id } }
    }

    private var filtered: [RouteInfo] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return routes }
        return routes.filter { $0.shortName.lowercased().hasPrefix(q) || $0.longName.lowercased().contains(q) }
    }

    var body: some View {
        List {
            if let error { Text(error).foregroundStyle(.red) }
            if search.isEmpty, !recent.isEmpty {
                Section("Recent") { ForEach(recent) { row($0) } }
            }
            Section(search.isEmpty ? "All routes" : "Matches") { ForEach(filtered) { row($0) } }
        }
        .searchable(text: $search, prompt: "Route number or name")
        .navigationTitle(vehicle.label.isEmpty ? vehicle.id : vehicle.label)
        .task {
            do { routes = try await session.routes() } catch { self.error = error.localizedDescription }
        }
    }

    private func row(_ route: RouteInfo) -> some View {
        NavigationLink(value: PickerRoute.trips(vehicle, route)) {
            HStack(spacing: 12) {
                RouteBadge(shortName: route.shortName, color: route.color, textColor: route.textColor)
                Text(route.longName).lineLimit(2)
            }
            .frame(minHeight: 56)
        }
    }
}
