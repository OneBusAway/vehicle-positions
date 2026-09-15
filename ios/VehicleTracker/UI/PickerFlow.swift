import SwiftUI

/// Vehicle → route → trip. One navigation stack whose path the vehicle
/// screen can push onto when there is only one vehicle to choose.
enum PickerRoute: Hashable {
    case routes(Vehicle)
    case trips(Vehicle, RouteInfo)
}

struct PickerFlow: View {
    @State private var path: [PickerRoute] = []

    var body: some View {
        NavigationStack(path: $path) {
            VehiclesView(path: $path)
                .navigationDestination(for: PickerRoute.self) { route in
                    switch route {
                    case .routes(let vehicle):
                        RoutesView(vehicle: vehicle)
                    case .trips(let vehicle, let route):
                        TripsView(vehicle: vehicle, route: route)
                    }
                }
        }
    }
}
