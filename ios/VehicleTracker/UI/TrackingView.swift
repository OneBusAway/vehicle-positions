import SwiftUI

struct TrackingView: View {
    @Environment(TripSession.self) private var session
    @State private var confirmEnd = false
    @State private var endError: String?
    @State private var showingRelogin = false
    @State private var reloginPassword = ""
    @State private var reloginError: String?

    var body: some View {
        VStack(spacing: 0) {
            statusBanner
            if let active = session.activeTrip {
                header(active)
                adherencePanel(active)
                RouteMapView(trip: active.trip, adherence: session.latest)
                    .frame(maxHeight: .infinity)
                footer(active)
            }
        }
        // The sheet is driven by its own state rather than straight off
        // `reporting`, so "Later" can dismiss it while the status stands.
        .onChange(of: session.reporting == .authExpired, initial: true) { _, expired in
            if expired { showingRelogin = true }
        }
        .confirmationDialog("End this trip?", isPresented: $confirmEnd, titleVisibility: .visible) {
            Button("End Trip", role: .destructive) { end() }
        }
        .alert("Could not end the trip", isPresented: Binding(get: { endError != nil }, set: { if !$0 { endError = nil } })) {
            Button("Retry") { end() }
            Button("End locally anyway", role: .destructive) { session.endLocally() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(endError ?? "")
        }
        .sheet(isPresented: $showingRelogin) {
            reloginSheet
        }
    }

    // MARK: Banner

    private var bannerLabel: some View {
        Text(TrackingBanner.text(phase: session.phase, reporting: session.reporting))
            .font(.title2.bold())
            .frame(maxWidth: .infinity, minHeight: 56)
            .background(TrackingBanner.color(phase: session.phase, reporting: session.reporting))
            .foregroundStyle(.white)
    }

    /// While the sign-in is expired the banner is the way back to the sheet,
    /// so dismissing it with "Later" is not a one-way door.
    @ViewBuilder private var statusBanner: some View {
        if session.reporting == .authExpired {
            Button { showingRelogin = true } label: { bannerLabel }
                .buttonStyle(.plain)
                .accessibilityHint("Sign in again")
        } else {
            bannerLabel
        }
    }

    private func header(_ active: ActiveTrip) -> some View {
        HStack(spacing: 12) {
            RouteBadge(shortName: active.trip.route.shortName, color: active.trip.route.color, textColor: active.trip.route.textColor)
            VStack(alignment: .leading) {
                Text(active.trip.headsign).font(.title3.bold()).lineLimit(1)
                Text(active.vehicle.label.isEmpty ? active.vehicle.id : active.vehicle.label).font(.subheadline).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding()
    }

    private func adherencePanel(_ active: ActiveTrip) -> some View {
        VStack(spacing: 4) {
            if let a = session.latest {
                Text(a.statusLabel)
                    .font(.system(size: 34, weight: .heavy))
                    .foregroundStyle(a.color)
                    .minimumScaleFactor(0.5)
                    .lineLimit(1)
                HStack {
                    Text("Next: \(a.nextStop.name)").bold()
                    Spacer()
                    Text("\(Formatters.clock(a.nextStop.arrivalAt, timezone: active.trip.timezone)) · \(Formatters.distance(a.distanceToNextStop))")
                }
                .font(.body)
            } else if case .paused = session.phase {
                Text("Paused").font(.system(size: 34, weight: .heavy)).foregroundStyle(.secondary)
                Text("Tap Resume to keep reporting.")
            } else {
                Text("Waiting for GPS…").font(.system(size: 34, weight: .heavy)).foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal)
        .padding(.bottom, 8)
    }

    private func footer(_ active: ActiveTrip) -> some View {
        VStack(spacing: 12) {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                HStack {
                    // The banner carries the status; the counter is the one
                    // number the driver checks, so it never goes away.
                    Text("\(session.fixesSent) sent")
                    Spacer()
                    Text(Formatters.elapsed(context.date.timeIntervalSince(active.startedAt)))
                        .monospacedDigit()
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }
            // A paused or stalled trip still has to be endable: Resume leads,
            // End Trip stays reachable underneath it.
            if canResume {
                Button {
                    session.resume()
                } label: {
                    Text("Resume").bold().frame(maxWidth: .infinity, minHeight: 64)
                }
                .buttonStyle(.borderedProminent)
                endTripButton
            } else {
                endTripButton
            }
        }
        .padding()
    }

    private var endTripButton: some View {
        Button {
            confirmEnd = true
        } label: {
            Text("End Trip").bold().frame(maxWidth: .infinity, minHeight: 64)
        }
        .buttonStyle(.borderedProminent)
        .tint(.red)
        .disabled({ if case .ending = session.phase { true } else { false } }())
    }

    private var reloginSheet: some View {
        NavigationStack {
            Form {
                Text("Your sign-in expired. The trip keeps running; sign in again to keep reporting.")
                SecureField("Password", text: $reloginPassword)
                if let reloginError { Text(reloginError).foregroundStyle(.red) }
                Button("Sign In") {
                    Task {
                        do {
                            try await session.reauthenticate(password: reloginPassword)
                            reloginPassword = ""
                            reloginError = nil
                            showingRelogin = false
                        } catch {
                            reloginError = error.localizedDescription
                        }
                    }
                }
                .disabled(reloginPassword.isEmpty)
            }
            .navigationTitle("Sign in again")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    // Nothing is lost by putting this off: the trip keeps
                    // running locally and the banner keeps saying signed out.
                    Button("Later") {
                        reloginPassword = ""
                        reloginError = nil
                        showingRelogin = false
                    }
                }
            }
        }
    }

    /// Paused (a relaunch found a stored trip) or the location stream itself
    /// died: both need the driver to resume from the foreground rather than
    /// end the trip.
    private var canResume: Bool {
        if case .paused = session.phase { return true }
        return session.reporting == .locationLost
    }

    private func end() {
        endError = nil
        Task {
            do { try await session.end() } catch { endError = error.localizedDescription }
        }
    }
}
