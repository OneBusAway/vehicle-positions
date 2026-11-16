import SwiftUI
import TipKit

/// "Tap your run", on Choose a run (walkthrough spec §8): on the driver's
/// first visit, pointing at the run tagged Now or Next.
nonisolated struct RunBoardTip: Tip {
    /// Whether it may show: set by the board from the driver's flags, since
    /// TipKit's own record is per phone.
    @Parameter(.transient) static var wanted: Bool = false

    /// The driver's key. TipKit keeps its record by id, so a tip one driver
    /// closed is still new to the next driver on the phone.
    let driver: String

    var id: String { "runBoardTip.\(driver)" }

    var title: Text { Text(TipText.runBoardTitle) }

    var message: Text? { Text(TipText.runBoardMessage) }

    var image: Image? { Image(systemName: "hand.tap.fill") }

    var rules: [Rule] {
        #Rule(Self.$wanted) { $0 == true }
    }
}

extension RunBoardTip {
    /// A run was tapped, whether or not the tip is up: the driver has found
    /// what it says.
    @MainActor static func runTapped(settings: AppSettings) {
        close(.actionPerformed, settings: settings)
    }

    /// The tip is done with for the driver signed in, in TipKit's record and
    /// in the flags.
    @MainActor static func close(_ reason: Tips.InvalidationReason, settings: AppSettings) {
        guard !settings.hasSeen(.runBoardTip) else { return }
        wanted = false
        RunBoardTip(driver: DriverTips.key(of: settings)).invalidate(reason: reason)
        DriverTips.shared.seen(.runBoard, settings: settings)
    }
}

/// Puts Choose a run's tip on one row: `isAnchor` on the board's tip run,
/// on the board shown. The tip's state lives here, so the tip coming and
/// going redraws neither the board nor its rows.
struct RunBoardTipAnchor: ViewModifier {
    let isAnchor: Bool
    /// The board has scrolled to its opening run and shows.
    let landed: Bool

    @Environment(TripSession.self) private var session
    @State private var isPresented = false

    func body(content: Content) -> some View {
        content
            .popoverTip(isAnchor ? RunBoardTip(driver: DriverTips.key(of: session.settings)) : nil, isPresented: $isPresented)
            .task(id: isAnchor && landed) {
                guard isAnchor && landed, DriverTips.shared.mayShow(.runBoard, settings: session.settings) else { return }
                // A moment after the list is drawn, so the tip comes to a
                // board that has stopped moving.
                do {
                    try await Task.sleep(for: .milliseconds(400))
                } catch {
                    return
                }
                RunBoardTip.wanted = true
            }
            // Its x, a tap anywhere else, or the row going away: the driver
            // has seen it.
            .onChange(of: isPresented) { _, presented in
                if presented {
                    DriverTips.shared.shown(.runBoard)
                } else {
                    DriverTips.shared.hidden(.runBoard)
                    RunBoardTip.close(.tipClosed, settings: session.settings)
                }
            }
            .onDisappear {
                guard isPresented else { return }
                DriverTips.shared.hidden(.runBoard)
                RunBoardTip.close(.tipClosed, settings: session.settings)
            }
    }
}
