import SwiftUI

/// The trip under way (DESIGN-iOS §4.7): the map fills the screen, the
/// status card floats over its top, and the trip sheet rises from its
/// bottom and never goes away.
struct TrackingView: View {
    @Environment(TripSession.self) private var session
    @State private var showingRelogin = false
    /// The sheet's detent is view state, so nothing resizes the sheet out
    /// from under the driver's hand.
    @State private var detent: PresentationDetent = .height(TripSheet.peek)
    /// The tall detent ends 8 pt under the status area. It is re-measured
    /// only while the sheet is at the peek: a problem line coming or going
    /// under the card must not move a sheet that is up or being dragged.
    @State private var expanded: CGFloat = TripSheet.peek
    @State private var height: CGFloat = 0
    @State private var statusAreaBottom: CGFloat = 0

    var body: some View {
        ZStack(alignment: .top) {
            TrackingMap()
                .ignoresSafeArea()
            StatusArea(showingRelogin: $showingRelogin)
                .padding(.horizontal, 12)
                .padding(.top, 8)
                .onGeometryChange(for: CGFloat.self) { $0.frame(in: .named("tracking")).maxY } action: { statusAreaBottom = $0 }
        }
        .coordinateSpace(.named("tracking"))
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        .onChange(of: wantedExpanded, initial: true) { _, wanted in
            if detent == .height(TripSheet.peek) { expanded = wanted }
        }
        .sheet(isPresented: .constant(true)) {
            TripSheet(showingRelogin: $showingRelogin)
                .presentationDetents([.height(TripSheet.peek), .height(expanded)], selection: $detent)
                .presentationDragIndicator(.visible)
                // The status card's buttons stay reachable at both detents,
                // and the map takes touches at the peek.
                .presentationBackgroundInteraction(.enabled)
                .presentationContentInteraction(.scrolls)
                .interactiveDismissDisabled()
        }
    }

    private var wantedExpanded: CGFloat {
        max(TripSheet.peek, height - statusAreaBottom - 8)
    }
}

/// The map, in its own view so the fixes it follows redraw it alone.
private struct TrackingMap: View {
    @Environment(TripSession.self) private var session

    var body: some View {
        RouteMapView(trip: session.activeTrip?.trip, adherence: session.latest, coveredBottom: TripSheet.peek)
    }
}

/// The status card with a problem line under it, or the Signed out card in
/// their place. Reads the session itself, so each fix redraws only this.
private struct StatusArea: View {
    @Environment(TripSession.self) private var session
    @Binding var showingRelogin: Bool

    var body: some View {
        if let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) {
            VStack(spacing: 8) {
                if status.isSignedOut {
                    SignedOutCard { showingRelogin = true }
                } else {
                    StatusCard(status: status)
                    if let problem = status.problem {
                        ProblemLine(problem: problem) { session.resume() }
                    }
                }
            }
        }
    }
}
