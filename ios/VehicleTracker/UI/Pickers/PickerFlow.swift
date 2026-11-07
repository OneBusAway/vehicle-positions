import SwiftUI

/// Vehicle → route → trip. One navigation stack whose path the vehicle
/// screen can push onto when there is only one vehicle to choose.
enum PickerRoute: Hashable {
    case routes(Vehicle)
    case trips(Vehicle, RouteInfo)
}

struct PickerFlow: View {
    @State private var path: [PickerRoute] = []
    /// Bumped by Change vehicle on a refused start, so the vehicle screen
    /// loads again as the path empties onto it: a 403 means the driver's
    /// vehicles changed after that screen loaded. Back leaves it as it was.
    @State private var vehicleReload = 0

    var body: some View {
        NavigationStack(path: $path) {
            VehiclesView(path: $path, reload: vehicleReload)
                .navigationDestination(for: PickerRoute.self) { route in
                    switch route {
                    case .routes(let vehicle):
                        RoutesView(vehicle: vehicle)
                    case .trips(let vehicle, let route):
                        TripsView(vehicle: vehicle, route: route, path: $path, vehicleReload: $vehicleReload)
                    }
                }
        }
    }
}
