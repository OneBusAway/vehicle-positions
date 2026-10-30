import OSLog
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
    @Environment(\.colorScheme) private var colorScheme
    @Binding var showingRelogin: Bool
    /// Whether the sheet is pulled up: the list goes back to its top each
    /// time it is.
    let isExpanded: Bool
    @State private var confirmEnd = false
    @State private var unfolded = false
    /// The server did not confirm the end. The error itself is logged; the
    /// driver gets one sentence and three ways on.
    @State private var endNotConfirmed = false
    @State private var reloginPassword = ""
    @State private var reloginError: String?
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "tracking")

    var body: some View {
        VStack(spacing: 0) {
            if let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) {
                TripBar(active: status.active, reports: status.reports, isEnding: session.phase.isEnding) { confirmEnd = true }
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(spacing: 0) {
                            FactRow(symbol: "bus.fill", label: TripStatusText.run,
                                    value: status.runTime.map { TripStatusText.pair($0, status.vehicleName) } ?? status.vehicleName,
                                    isProblem: false)
                                .id(ScrollAnchor.top)
                            FactRow(symbol: status.reports.symbol, label: TripStatusText.reports,
                                    value: status.reports.text, isProblem: status.reports.isProblem)
                            Divider()
                                .padding(.horizontal, 20)
                                .padding(.vertical, 8)
                            StopList(trip: status.active.trip,
                                     rows: StopRows.rows(stops: status.active.trip.stops, nextStopIndex: status.nextStopIndex, unfolded: unfolded),
                                     vehicleName: status.vehicleName, vehicleStatus: status.vehicleRowStatus,
                                     vehicleTone: status.ring, strip: stripColors(for: status.active.trip.route),
                                     unfolded: $unfolded)
                        }
                        .padding(.bottom, 16)
                    }
                    .onChange(of: isExpanded) { _, expanded in
                        if expanded { proxy.scrollTo(ScrollAnchor.top, anchor: .top) }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        // The sheet is driven by its own state rather than straight off
        // `reporting`, so "Later" can dismiss it while the status stands.
        .onChange(of: session.reporting == .authExpired, initial: true) { _, expired in
            if expired { showingRelogin = true }
        }
        .alert(TripStatusText.endTripTitle, isPresented: $confirmEnd) {
            Button(TripStatusText.endTrip, role: .destructive) { end() }
            Button(TripStatusText.keepGoing, role: .cancel) {}
        } message: {
            Text(endMessage)
        }
        .alert(TripStatusText.notEndedTitle, isPresented: $endNotConfirmed) {
            Button(TripStatusText.tryAgain) { end() }
            Button(TripStatusText.endOnThisPhone, role: .destructive) { session.endLocally() }
            Button(TripStatusText.keepGoing, role: .cancel) {}
        } message: {
            Text(TripStatusText.notEndedMessage)
        }
        .sensoryFeedback(.error, trigger: endNotConfirmed) { _, failed in failed }
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

    /// What ending the trip will do: the run's name when the route has one,
    /// and how many reports went out.
    private var endMessage: String {
        guard let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) else { return "" }
        let route = status.active.trip.route.shortName
        if let runTime = status.runTime, !route.isEmpty {
            return TripStatusText.endTripMessage(route: route, time: runTime, count: session.fixesSent)
        }
        return TripStatusText.endTripMessage(count: session.fixesSent)
    }

    private func end() {
        Task {
            do {
                try await session.end()
            } catch {
                log.error("the server did not confirm the end of the trip: \(String(describing: error))")
                endNotConfirmed = true
            }
        }
    }

    private enum ScrollAnchor {
        case top
    }

    /// The strip's colours are the map's, in the same theme.
    private func stripColors(for route: RouteSummary) -> RouteStripColors {
        let dark = colorScheme == .dark
        let line = RouteLineColors.line(hex: route.color, dark: dark)
        return RouteStripColors(line: Color(uiColor: line.uiColor), casing: Color(uiColor: RouteLineColors.casing(line: line, dark: dark).uiColor))
    }
}

/// One fact about the trip: a symbol, what it is, and its value. A problem
/// shows in an amber symbol and heavier text.
private struct FactRow: View {
    let symbol: String
    let label: String
    let value: String
    let isProblem: Bool

    var body: some View {
        HStack(spacing: 16) {
            Image(systemName: symbol)
                .font(.system(size: 22))
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(isProblem ? Color.amberOnSurface : Color.secondary)
                .frame(width: 28)
            Text(label)
                .font(.body)
            Spacer(minLength: 8)
            Text(value)
                .font(isProblem ? .headline : .body)
                .foregroundStyle(isProblem ? .primary : .secondary)
                .multilineTextAlignment(.trailing)
        }
        .padding(.horizontal, 20)
        .frame(minHeight: 52)
        .accessibilityElement(children: .combine)
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
