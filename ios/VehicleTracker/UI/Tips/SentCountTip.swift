import SwiftUI
import TipKit

/// "Reports sent", on the trip bar's sent count (walkthrough spec §8): on
/// the driver's first trip, and only before the vehicle moves.
nonisolated struct SentCountTip: Tip {
    /// Whether it may show: set by the trip bar from the driver's flags and
    /// the vehicle's speed, since TipKit's own record is per phone.
    @Parameter(.transient) static var wanted: Bool = false

    /// The driver's key. TipKit keeps its record by id, so a tip one driver
    /// closed is still new to the next driver on the phone.
    let driver: String

    var id: String { "sentCountTip.\(driver)" }

    var title: Text { Text(TipText.sentCountTitle) }

    var message: Text? { Text(TipText.sentCountMessage) }

    var image: Image? { Image(systemName: "checkmark.icloud.fill") }

    var rules: [Rule] {
        #Rule(Self.$wanted) { $0 == true }
    }
}

extension SentCountTip {
    /// The tip is done with for the driver signed in, in TipKit's record and
    /// in the flags.
    @MainActor static func close(_ reason: Tips.InvalidationReason, settings: AppSettings) {
        guard !settings.hasSeen(.sentCountTip) else { return }
        wanted = false
        SentCountTip(driver: DriverTips.key(of: settings)).invalidate(reason: reason)
        DriverTips.shared.seen(.sentCount, settings: settings)
    }
}

/// Puts Tracking's tip on the trip bar's sent count. The bar is inside the
/// trip sheet, so the tip presents from there: anchored to anything under
/// the sheet, it would sit under it. The tip's state and the speed it
/// watches are read here, so neither redraws the bar or the map.
struct SentCountTipAnchor: ViewModifier {
    /// The coordinate space of the trip bar's titles, the headsign over the
    /// count.
    static let titles = "tripBarTitles"

    @Environment(TripSession.self) private var session
    @State private var isPresented = false
    /// From the top of the titles to the foot of the count. The tip points
    /// at the top of the titles, over the count: pointing at the count
    /// itself, the popover would reach down over End trip, and a tap there
    /// would do nothing. TipKit's popover takes its place from the view it
    /// is on, not from an attachment anchor, so it is on a strip this tall.
    @State private var reach: CGFloat = 0
    /// The pickers have gone and the sheet has risen and settled, so the
    /// tip has a still place to point at.
    @State private var settled = false

    func body(content: Content) -> some View {
        let status = TripStatus(phase: session.phase, latest: session.latest, reporting: session.reporting)
        let moment = SentCountMoment.of(answer: status?.answer, speed: session.latest?.fix.speed)
        content
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(Self.titles)).maxY } action: { reach = $0 }
            .background(alignment: .bottom) {
                Color.clear
                    .frame(height: reach)
                    .popoverTip(SentCountTip(driver: DriverTips.key(of: session.settings)), isPresented: $isPresented)
                    .allowsHitTesting(false)
            }
            // The sheet is built again when the pickers go, as much as a few
            // seconds after a first start; a tip up by then would be left
            // behind on screen, pointing at nothing.
            .task(id: DriverTips.shared.pickersOnScreen) {
                guard !DriverTips.shared.pickersOnScreen else { return }
                do {
                    try await Task.sleep(for: .milliseconds(600))
                } catch {
                    return
                }
                settled = true
            }
            .onChange(of: settled ? moment : nil, initial: true) { _, moment in
                follow(moment)
            }
            // Its x, a tap anywhere else, or the tip closed from here: the
            // driver has seen it.
            .onChange(of: isPresented) { _, presented in
                if presented {
                    DriverTips.shared.shown(.sentCount)
                } else {
                    DriverTips.shared.hidden(.sentCount)
                    SentCountTip.close(.tipClosed, settings: session.settings)
                }
            }
            .onDisappear {
                if isPresented {
                    DriverTips.shared.hidden(.sentCount)
                    SentCountTip.close(.tipClosed, settings: session.settings)
                } else {
                    // Not shown yet: it may still come on the next visit to
                    // Tracking during this trip.
                    SentCountTip.wanted = false
                }
            }
    }

    private func follow(_ moment: SentCountMoment?) {
        switch moment {
        case .parked:
            if DriverTips.shared.mayShow(.sentCount, settings: session.settings) { SentCountTip.wanted = true }
        case .moving:
            // It never shows on the road, and never comes back.
            SentCountTip.close(.actionPerformed, settings: session.settings)
        case .unknown, nil:
            break
        }
    }
}
