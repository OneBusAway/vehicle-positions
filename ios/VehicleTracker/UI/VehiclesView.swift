import SwiftUI

struct VehiclesView: View {
    @Environment(TripSession.self) private var session
    @Binding var path: [PickerRoute]
    @State private var error: String?
    @State private var loaded = false
    /// This visit to the pickers has skipped past the vehicle screen already.
    @State private var skippedOnce = false

    var body: some View {
        List {
            if let error {
                Text(error).foregroundStyle(.red)
            }
            ForEach(session.vehicles) { vehicle in
                NavigationLink(value: PickerRoute.routes(vehicle)) {
                    Text(vehicle.label.isEmpty ? vehicle.id : vehicle.label)
                        .font(.title3)
                        .frame(minHeight: 64)
                }
            }
            if loaded && session.vehicles.isEmpty && error == nil {
                Text("No vehicles are assigned to you. Ask your dispatcher.")
            }
        }
        .navigationTitle("Your vehicle")
        .toolbar {
            Button("Sign out") { Task { await session.signOut() } }
        }
        .refreshable { await load() }
        .task { await load() }
    }

    private func load() async {
        do {
            try await session.loadVehicles()
            error = nil
            loaded = true
            if let only = VehicleAutoSkip.vehicle(toSkipTo: session.vehicles, alreadySkipped: skippedOnce,
                                                  pathIsEmpty: path.isEmpty) {
                skippedOnce = true
                path = [.routes(only)]
            }
        } catch {
            guard !session.handleIfUnauthorized(error) else { return }
            self.error = error.localizedDescription
            loaded = true
        }
    }
}

/// One vehicle is no choice, so the pickers go straight to its routes, but
/// only once per visit. The vehicle screen loads again each time it
/// reappears, and skipping again then would push the routes back over every
/// Back, so the driver could never reach the vehicle screen.
nonisolated enum VehicleAutoSkip {
    static func vehicle(toSkipTo vehicles: [Vehicle], alreadySkipped: Bool, pathIsEmpty: Bool) -> Vehicle? {
        guard vehicles.count == 1, !alreadySkipped, pathIsEmpty else { return nil }
        return vehicles[0]
    }
}
