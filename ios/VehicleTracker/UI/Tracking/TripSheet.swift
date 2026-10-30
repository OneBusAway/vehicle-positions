import SwiftUI

/// What the trip bar pulls up into (DESIGN-iOS §4.7.4). The bar is its top
/// row, so End trip and the sent count never leave the screen. The alerts
/// and the sign-in sheet present from here: the sheet is already up, and
/// SwiftUI cannot present over it from the screen underneath.
struct TripSheet: View {
    /// The bar's height, which is the sheet's peek. The sheet adds the
    /// bottom safe area itself.
    static let peek: CGFloat = 76

    @Environment(TripSession.self) private var session
    @Binding var showingRelogin: Bool
    @State private var confirmEnd = false
    @State private var endError: String?
    @State private var reloginPassword = ""
    @State private var reloginError: String?

    var body: some View {
        VStack(spacing: 0) {
            if let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) {
                TripBar(active: status.active, reports: status.reports, isEnding: session.phase.isEnding) { confirmEnd = true }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
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
        .serverSetupPrompt()
    }

    private var reloginSheet: some View {
        NavigationStack {
            Form {
                Text("Your sign-in expired. The trip keeps running; sign in again to keep reporting.")
                SecureField("Password", text: $reloginPassword)
                if let reloginError { Text(reloginError).foregroundStyle(.errorText) }
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
                    // running locally and the Signed out card stays.
                    Button("Later") {
                        reloginPassword = ""
                        reloginError = nil
                        showingRelogin = false
                    }
                }
            }
        }
    }

    private func end() {
        endError = nil
        Task {
            do { try await session.end() } catch { endError = error.localizedDescription }
        }
    }
}

/// Route, headsign, the trip's running time and sent count, and End trip.
/// Equatable, so a fix that changes none of these does not redraw it: only
/// an accepted report does, through `fixesSent`.
struct TripBar: View, Equatable {
    let active: ActiveTrip
    let reports: TripStatus.Reports
    let isEnding: Bool
    let onEnd: () -> Void

    /// Narrower than this (the iPhone SE), End trip leaves out its symbol so
    /// the running time and the sent count still fit beside it: Android's
    /// compact bar.
    nonisolated static let compactWidth: CGFloat = 380

    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var isCompact = false

    static func == (lhs: TripBar, rhs: TripBar) -> Bool {
        lhs.active == rhs.active && lhs.reports == rhs.reports && lhs.isEnding == rhs.isEnding
    }

    var body: some View {
        Group {
            // At accessibility sizes End trip moves under the headsign, at
            // full width, rather than squeezing it (DESIGN-iOS §2.8).
            if dynamicTypeSize.isAccessibilitySize {
                VStack(alignment: .leading, spacing: 12) {
                    HStack(spacing: 12) {
                        badge
                        titles
                    }
                    endButton
                }
            } else {
                HStack(spacing: 12) {
                    badge
                    titles
                    Spacer(minLength: 8)
                    endButton
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 16)
        .padding(.bottom, 16)
        .onGeometryChange(for: Bool.self) { $0.size.width < Self.compactWidth } action: { isCompact = $0 }
    }

    private var badge: some View {
        RouteBadge(shortName: active.trip.route.shortName, color: active.trip.route.color,
                   textColor: active.trip.route.textColor, size: .trip)
    }

    private var titles: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(active.trip.headsign)
                .font(.headline)
                .lineLimit(1)
            HStack(spacing: 6) {
                Text(active.startedAt, style: .timer)
                    .monospacedDigit()
                Text(verbatim: "·")
                Image(systemName: reports.symbol)
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(reports.isProblem ? Color.amberOnSurface : Color.secondary)
                Text(TripStatusText.sent(session.fixesSent))
                    .monospacedDigit()
                    .contentTransition(.numericText(value: Double(session.fixesSent)))
                    .animation(.smooth(duration: 0.25), value: session.fixesSent)
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Glass, never red: red already means early on this screen.
    private var endButton: some View {
        Button(action: onEnd) {
            HStack(spacing: 6) {
                if isEnding {
                    ProgressView()
                        .controlSize(.small)
                } else if !isCompact {
                    Image(systemName: "stop.circle")
                }
                Text(isEnding ? TripStatusText.ending : TripStatusText.endTrip)
            }
            .font(.headline)
            .padding(.horizontal, 6)
            .frame(maxWidth: dynamicTypeSize.isAccessibilitySize ? .infinity : nil, minHeight: 40)
        }
        .buttonStyle(.glass)
        .disabled(isEnding)
    }
}
