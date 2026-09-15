import SwiftUI

struct TripsView: View {
    @Environment(TripSession.self) private var session
    let vehicle: Vehicle
    let route: RouteInfo
    @State private var page: RouteTripsPage?
    @State private var error: String?
    @State private var starting = false

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if let error { Text(error).foregroundStyle(.red) }
                if let page {
                    let highlighted = TripRun.highlighted(in: page.trips, now: Date())
                    Section("\(page.serviceDate.prefix(4))-\(page.serviceDate.dropFirst(4).prefix(2))-\(page.serviceDate.suffix(2))") {
                        ForEach(page.trips) { trip in
                            Button {
                                start(trip)
                            } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text("\(Formatters.clock(trip.startsAt, timezone: page.timezone)) → \(trip.headsign)")
                                            .font(.title3)
                                        Text("\(trip.firstStop) → \(trip.lastStop)")
                                            .font(.subheadline)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if trip.id == highlighted?.id {
                                        Text(trip.startsAt <= Date() ? "Now" : "Next")
                                            .font(.caption.bold())
                                            .padding(6)
                                            .background(Color.accentColor.opacity(0.2), in: Capsule())
                                    }
                                }
                                .frame(minHeight: 64)
                            }
                            .id(trip.id)
                            .disabled(starting)
                        }
                    }
                    if page.trips.isEmpty {
                        Text("No runs on this route today.")
                    }
                }
            }
            .navigationTitle("Route \(route.shortName)")
            .overlay { if starting { ProgressView("Starting…") } }
            .task {
                do {
                    let loaded = try await session.trips(routeID: route.id)
                    page = loaded
                    if let h = TripRun.highlighted(in: loaded.trips, now: Date()) {
                        proxy.scrollTo(h.id, anchor: .center)
                    }
                } catch {
                    self.error = error.localizedDescription
                }
            }
        }
    }

    private func start(_ trip: TripSummary) {
        starting = true
        error = nil
        Task {
            defer { starting = false }
            do {
                try await session.start(vehicle: vehicle, tripID: trip.id)
            } catch APIError.status(let code, let message) {
                error = message.isEmpty ? "Server error \(code)" : message
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
