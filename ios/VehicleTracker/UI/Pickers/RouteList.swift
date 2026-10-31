import Foundation

/// The route list as Choose a route draws it: a port of Android's
/// `RoutesViewModel`, so both apps offer the same routes for the same search.
nonisolated struct RouteListModel: Equatable {
    /// The catalog narrowed by the query, in catalog order.
    let visible: [RouteInfo]
    /// Recently driven routes, newest first; empty while the driver searches,
    /// because recents are a shortcut past the search.
    let recent: [RouteInfo]
    /// Whether the routes are drawn as a grid of badges rather than rows.
    let grid: Bool
    /// How many routes the catalog has, for the grid's "All routes · 33".
    let total: Int
}

nonisolated enum RouteList {
    static func of(_ routes: [RouteInfo], query: String, recentIDs: [String]) -> RouteListModel {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return RouteListModel(
            visible: routes.filter { $0.matches(trimmed) },
            recent: query.isEmpty ? routes.matching(ids: recentIDs) : [],
            grid: showsAsGrid(routes),
            total: routes.count
        )
    }

    /// A feed that names none of its routes, as King County's names none, is
    /// a list of bare numbers, which reads better as a grid of badges. A feed
    /// with any long names keeps its rows, so a few unnamed routes do not
    /// make it uneven.
    static func showsAsGrid(_ routes: [RouteInfo]) -> Bool {
        routes.allSatisfy { $0.longName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }
}

private extension RouteInfo {
    /// A prefix on the number and a substring on the name, so a driver typing
    /// "5" is offered route 5 and not route 15. `query` arrives trimmed and
    /// lowercased; an empty one matches everything.
    nonisolated func matches(_ query: String) -> Bool {
        query.isEmpty || shortName.lowercased().hasPrefix(query) || longName.lowercased().contains(query)
    }
}
