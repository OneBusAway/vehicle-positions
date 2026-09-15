import SwiftUI

struct VehiclesView: View {
    @Environment(TripSession.self) private var session
    @Binding var path: [PickerRoute]
    @State private var error: String?
    @State private var loaded = false

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
            Button("Sign out") { session.signOut() }
        }
        .refreshable { await load() }
        .task { await load() }
    }

    private func load() async {
        do {
            try await session.loadVehicles()
            error = nil
            loaded = true
            // One vehicle is no choice: go straight to the routes.
            if session.vehicles.count == 1, path.isEmpty {
                path = [.routes(session.vehicles[0])]
            }
        } catch APIError.status(401, _) {
            session.signOut()
        } catch {
            self.error = error.localizedDescription
            loaded = true
        }
    }
}
