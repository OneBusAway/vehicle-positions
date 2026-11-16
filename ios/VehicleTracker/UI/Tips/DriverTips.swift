import Foundation
import Observation
import OSLog
import TipKit

/// The two tips on the real screens and First trip done (walkthrough spec
/// §8): each once per driver, by the welcome's flags, and one at a time.
/// TipKit draws the tips and keeps a record of each, but per phone: so each
/// tip's id carries the driver's key, and whether it may show at all is a
/// parameter the screen sets from here.
@MainActor @Observable
final class DriverTips {
    nonisolated enum Kind: CaseIterable {
        case runBoard, sentCount, firstTripDone

        var flag: DriverFlag {
            switch self {
            case .runBoard: .runBoardTip
            case .sentCount: .sentCountTip
            case .firstTripDone: .firstTripDone
            }
        }
    }

    /// The trip First trip done is about.
    nonisolated struct FinishedTrip: Identifiable, Equatable {
        let id = UUID()
        let sent: Int
    }

    /// The app's one. TipKit is configured as it is made, by the first
    /// screen that can show a tip, before that tip is drawn.
    static let shared: DriverTips = {
        do {
            try Tips.configure()
        } catch {
            Logger(subsystem: "org.onebusaway.vehicletracker", category: "tips")
                .error("TipKit did not start, so no tip will show: \(String(describing: error), privacy: .public)")
        }
        return DriverTips()
    }()

    /// The tip or the sheet that is up.
    private(set) var showing: Kind?
    /// The trip First trip done is waiting to show, or showing.
    private(set) var finishedTrip: FinishedTrip?
    /// The pickers are on screen. Until they have gone, Tracking's sheet can
    /// be built again from scratch, and a tip shown from it would be left
    /// behind.
    private(set) var pickersOnScreen = false

    /// The driver signed in, by the key the flags keep them under.
    static func key(of settings: AppSettings) -> String {
        AppSettings.driverKey(host: settings.serverURL?.host() ?? "", email: settings.email)
    }

    /// Whether `kind` may come up for the driver signed in: not seen yet,
    /// and nothing else up. First trip done, once it waits, goes first.
    func mayShow(_ kind: Kind, settings: AppSettings) -> Bool {
        guard !settings.hasSeen(kind.flag) else { return false }
        if kind != .firstTripDone && finishedTrip != nil { return false }
        return showing == nil || showing == kind
    }

    func pickers(onScreen: Bool) {
        pickersOnScreen = onScreen
    }

    /// TipKit put the tip up.
    func shown(_ kind: Kind) {
        showing = kind
    }

    /// The tip is off the screen: TipKit took it down, or the view it
    /// pointed at went.
    func hidden(_ kind: Kind) {
        if showing == kind { showing = nil }
    }

    /// The driver closed the tip or did what it says: it never comes back
    /// for this driver. It is up until it is `hidden`.
    func seen(_ kind: Kind, settings: AppSettings) {
        settings.markSeen(kind.flag)
    }

    /// The server confirmed the end of a trip. Tracking's tip is for the
    /// first trip only, so it is done with whatever the count, and gone with
    /// Tracking; First trip done follows the first trip that sent a report.
    func tripEnded(sent: Int, settings: AppSettings) {
        seen(.sentCount, settings: settings)
        hidden(.sentCount)
        guard sent > 0, mayShow(.firstTripDone, settings: settings) else { return }
        finishedTrip = FinishedTrip(sent: sent)
    }

    /// First trip done is up: it is shown once, however it closes.
    func finishedTripShown(settings: AppSettings) {
        showing = .firstTripDone
        settings.markSeen(.firstTripDone)
    }

    func finishedTripClosed() {
        finishedTrip = nil
        if showing == .firstTripDone { showing = nil }
    }
}
