import SwiftUI

/// The trip under way (DESIGN-iOS §4.7): the map fills the screen, the
/// status card floats over its top, and the trip sheet rises from its
/// bottom and never goes away.
struct TrackingView: View {
    /// The safe area's size and insets, as the map's geometry reports them:
    /// the map runs under the insets, so its own height is the three added.
    private nonisolated struct MapLayout: Equatable {
        var height: CGFloat = 0
        var safeTop: CGFloat = 0
        var safeBottom: CGFloat = 0
    }

    @Environment(TripSession.self) private var session
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var showingRelogin = false
    @State private var following = MapFollowing()
    @State private var map = MapLayout()
    /// The status card's bottom edge, in the safe area. The framing is built
    /// from the card alone, so a problem line coming or going never moves
    /// the map.
    @State private var cardBottom: CGFloat = 0
    /// The sheet's peek, measured from its bar by the sheet itself.
    @State private var peek: CGFloat = TripSheet.minimumPeek
    /// The sheet's detent is view state, so nothing resizes the sheet out
    /// from under the driver's hand.
    @State private var detent: PresentationDetent = .height(TripSheet.minimumPeek)
    /// The tall detent ends 8 pt under the status area. It is re-measured
    /// only while the sheet is at the peek: a problem line coming or going
    /// under the card must not move a sheet that is up or being dragged.
    @State private var expanded: CGFloat = TripSheet.minimumPeek
    @State private var statusAreaBottom: CGFloat = 0

    var body: some View {
        ZStack(alignment: .top) {
            TrackingMap(coveredBottom: peek, framing: framing, following: following)
                .ignoresSafeArea()
                .onGeometryChange(for: MapLayout.self) {
                    MapLayout(height: $0.size.height, safeTop: $0.safeAreaInsets.top, safeBottom: $0.safeAreaInsets.bottom)
                } action: { map = $0 }
            StatusArea(showingRelogin: $showingRelogin, cardBottom: $cardBottom)
                .padding(.horizontal, 12)
                .padding(.top, 8)
                .onGeometryChange(for: CGFloat.self) { $0.frame(in: .named("tracking")).maxY } action: { statusAreaBottom = $0 }
            if !following.isFollowing {
                RecenterButton { following.follow() }
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
                    .padding(.trailing, 12)
                    .padding(.bottom, peek + 12)
                    .transition(reduceMotion ? .opacity : .opacity.combined(with: .scale(scale: 0.9)))
            }
        }
        .coordinateSpace(.named("tracking"))
        .animation(.smooth(duration: 0.2), value: following.isFollowing)
        .keepsScreenAwakeWhileCharging()
        .onDisappear { following.stop() }
        .onChange(of: wantedExpanded, initial: true) { _, wanted in
            if detent == .height(peek) { expanded = wanted }
        }
        // The bar was measured, or the text size changed it: a sheet resting
        // at the old peek moves to the new one.
        .onChange(of: peek) { old, new in
            if detent == .height(old) { detent = .height(new) }
        }
        .sheet(isPresented: .constant(true)) {
            TripSheet(showingRelogin: $showingRelogin, peek: $peek, isExpanded: detent != .height(peek))
                .presentationDetents([.height(peek), .height(expanded)], selection: $detent)
                .presentationDragIndicator(.visible)
                // The status card's buttons stay reachable at both detents,
                // and the map takes touches at the peek.
                .presentationBackgroundInteraction(.enabled)
                .presentationContentInteraction(.scrolls)
                .interactiveDismissDisabled()
        }
    }

    /// The sheet's height is measured from the bottom of the safe area, and
    /// `map.height` is the safe area's: the map itself runs under the insets.
    private var wantedExpanded: CGFloat {
        max(peek, map.height - statusAreaBottom - 8)
    }

    private var framing: FollowFraming {
        FollowFraming(mapHeight: map.height, coveredTop: map.safeTop + cardBottom, coveredBottom: peek + map.safeBottom)
    }
}

/// The map, in its own view so the fixes it follows redraw it alone.
private struct TrackingMap: View {
    @Environment(TripSession.self) private var session
    let coveredBottom: CGFloat
    let framing: FollowFraming
    let following: MapFollowing

    var body: some View {
        let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting)
        RouteMapView(trip: status?.active.trip, adherence: session.latest, ring: status?.ring ?? .neutral,
                     vehicleName: status?.vehicleName ?? "", coveredBottom: coveredBottom, framing: framing, following: following)
    }
}

/// The status card with a problem line under it, or the Signed out card in
/// their place. Reads the session itself, so each fix redraws only this.
private struct StatusArea: View {
    @Environment(TripSession.self) private var session
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding var showingRelogin: Bool
    @Binding var cardBottom: CGFloat

    var body: some View {
        if let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting) {
            VStack(spacing: 8) {
                if status.isSignedOut {
                    SignedOutCard { showingRelogin = true }
                        .modifier(CardBottom(cardBottom: $cardBottom))
                } else {
                    StatusCard(status: status)
                        .modifier(CardBottom(cardBottom: $cardBottom))
                    if let problem = status.problem {
                        ProblemLine(problem: problem) { session.resume() }
                            // It comes out from under the card and leaves the
                            // same way; with Reduce Motion it only fades.
                            .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
                    }
                }
            }
            .animation(.smooth(duration: 0.25), value: status.category)
            // A problem appearing, or giving way to another, taps the wrist;
            // one clearing does not (DESIGN-iOS §2.7).
            .sensoryFeedback(.warning, trigger: status.problem?.kind) { old, new in new != nil && old != new }
            .sensoryFeedback(.warning, trigger: status.isSignedOut) { _, signedOut in signedOut }
            // A change of kind is read out, as the problem that appeared or
            // the card; the minutes moving inside one kind are not.
            .onChange(of: status.category) { old, _ in
                AccessibilityNotification.Announcement(status.announcement(after: old)).post()
            }
        }
    }
}

/// Reports where the card ends, in the safe area, for the map's framing.
private struct CardBottom: ViewModifier {
    @Binding var cardBottom: CGFloat

    func body(content: Content) -> some View {
        content.onGeometryChange(for: CGFloat.self) { $0.frame(in: .named("tracking")).maxY } action: { cardBottom = $0 }
    }
}
