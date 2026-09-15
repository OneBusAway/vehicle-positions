import SwiftUI

struct TrackingView: View {
    @Environment(TripSession.self) private var session
    @State private var confirmEnd = false
    @State private var endError: String?

    var body: some View {
        VStack(spacing: 16) {
            Text(session.reporting.label)
                .font(.title2.bold())
                .frame(maxWidth: .infinity, minHeight: 56)
                .background(session.reporting.isProblem ? Color.red : Color.green)
                .foregroundStyle(.white)
            if let active = session.activeTrip {
                HStack {
                    RouteBadge(shortName: active.trip.route.shortName, color: active.trip.route.color, textColor: active.trip.route.textColor)
                    Text(active.trip.headsign).font(.title2)
                }
            }
            if let a = session.latest {
                Text(a.statusLabel).font(.largeTitle.bold()).foregroundStyle(a.color)
                Text("Next: \(a.nextStop.name)")
            } else {
                Text("Waiting for GPS…").foregroundStyle(.secondary)
            }
            Spacer()
            if case .paused = session.phase {
                Button("Resume") { session.resume() }.buttonStyle(.borderedProminent)
            } else {
                Button("End Trip") { confirmEnd = true }.buttonStyle(.borderedProminent).tint(.red)
            }
        }
        .padding()
        .confirmationDialog("End this trip?", isPresented: $confirmEnd) {
            Button("End Trip", role: .destructive) { end() }
        }
        .alert("Could not end the trip", isPresented: Binding(
            get: { endError != nil },
            set: { if !$0 { endError = nil } }
        )) {
            Button("Retry") { end() }
            Button("End locally anyway", role: .destructive) { session.endLocally() }
            Button("Cancel", role: .cancel) { endError = nil }
        } message: {
            Text(endError ?? "")
        }
    }

    private func end() {
        endError = nil
        Task {
            do { try await session.end() } catch { endError = error.localizedDescription }
        }
    }
}
