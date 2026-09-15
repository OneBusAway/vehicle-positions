import Foundation

/// Signs in and starts a trip from launch arguments, so the simulator smoke
/// tests (phone and CarPlay) can reach the tracking screen without taps.
/// Debug builds only; a release build ignores the arguments.
///
///     xcrun simctl launch <udid> org.onebusaway.vehicletracker \
///       -autoServer http://localhost:8080 -autoEmail driver@test.com \
///       -autoPassword password -autoVehicle bus-1 -autoTrip T1
enum DebugAutoStart {
    @MainActor
    static func runIfRequested(session: TripSession) {
        #if DEBUG
        let defaults = UserDefaults.standard // `-key value` launch arguments land here
        guard let server = defaults.string(forKey: "autoServer"), let url = URL(string: server),
              let email = defaults.string(forKey: "autoEmail"),
              let password = defaults.string(forKey: "autoPassword"),
              let vehicleID = defaults.string(forKey: "autoVehicle"),
              let tripID = defaults.string(forKey: "autoTrip") else { return }
        Task {
            do {
                if case .signedOut = session.phase {
                    try await session.signIn(serverURL: url, email: email, password: password)
                }
                guard case .idle = session.phase else { return }
                try await session.loadVehicles()
                let vehicle = session.vehicles.first { $0.id == vehicleID } ?? Vehicle(id: vehicleID, label: vehicleID)
                try await session.start(vehicle: vehicle, tripID: tripID)
            } catch {
                print("DebugAutoStart failed: \(error)")
            }
        }
        #endif
    }
}
