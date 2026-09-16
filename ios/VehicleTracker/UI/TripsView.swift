import SwiftUI

struct TripsView: View {
    @Environment(TripSession.self) private var session
    let vehicle: Vehicle
    let route: RouteInfo
    @State private var page: RouteTripsPage?
    @State private var error: String?

    /// A start is in flight — this list's own, or one from the car, which
    /// must disable this list just the same.
    private var starting: Bool { session.phase.isStarting }

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
                    guard !session.handleIfUnauthorized(error) else { return }
                    self.error = error.localizedDescription
                }
            }
        }
    }

    private func start(_ trip: TripSummary) {
        error = nil
        Task {
            do {
                try await session.start(vehicle: vehicle, tripID: trip.id)
            } catch {
                guard !session.handleIfUnauthorized(error) else { return }
                self.error = error.localizedDescription
            }
        }
    }
}
