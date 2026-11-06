import Foundation
import Observation
import OSLog
import UIKit

/// Whether the phone is on power, and a way to hear when that changes.
protocol PowerSource: AnyObject {
    var batteryState: UIDevice.BatteryState { get }
    var isLowPowerModeEnabled: Bool { get }
    /// Battery monitoring has a cost, so it is on only while a lease is held.
    var isBatteryMonitoringEnabled: Bool { get set }
    /// Called whenever the battery state or Low Power Mode may have changed.
    var onChange: (() -> Void)? { get set }
}

/// The idle timer, which lets the display sleep.
protocol IdleTimer: AnyObject {
    var isDisabled: Bool { get set }
}

/// Holds the idle timer off while a screen that wants it is up and the phone
/// is charging (DESIGN-iOS §4.7.7): a phone in a dash mount is never raised,
/// so raise-to-wake never fires, and a dark screen breaks the glance. The
/// hold lets go on battery, in Low Power Mode, and when the last lease is
/// returned.
///
/// One owner with leases, as OneBusAway iOS's `ScreenAwakeCoordinator`: two
/// screens overlapping can never leave the timer switched off, and a repeat
/// acquire from the same owner is a no-op rather than a leaked lease. Unlike
/// OneBusAway's it has no ten-minute failsafe, because a trip runs longer
/// than that; unplugging is the failsafe.
@MainActor @Observable
final class ScreenAwakeCoordinator {
    @ObservationIgnored private let power: any PowerSource
    @ObservationIgnored private let idleTimer: any IdleTimer
    @ObservationIgnored private var owners: Set<ObjectIdentifier> = []
    /// What the timer was before the hold began, so releasing it cannot
    /// switch off a hold some other part of the app placed.
    @ObservationIgnored private var stateBeforeHold = false
    @ObservationIgnored private var isHolding = false
    @ObservationIgnored private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "screen-awake")

    init(power: any PowerSource, idleTimer: any IdleTimer) {
        self.power = power
        self.idleTimer = idleTimer
        power.onChange = { [weak self] in self?.apply() }
    }

    /// How many owners hold a lease: a test seam.
    var activeLeases: Int { owners.count }

    /// Takes a lease for `owner`, at most one at a time: SwiftUI can run
    /// `onAppear` more than once for one appearance.
    func acquire(owner: ObjectIdentifier) {
        guard owners.insert(owner).inserted else { return }
        if owners.count == 1 { power.isBatteryMonitoringEnabled = true }
        apply()
    }

    /// Returns `owner`'s lease, if it holds one. A release without a matching
    /// acquire is a bug worth a line in the log, not an unbalanced count.
    func release(owner: ObjectIdentifier) {
        guard owners.remove(owner) != nil else {
            log.warning("a screen released a lease it did not hold; ignored")
            return
        }
        if owners.isEmpty { power.isBatteryMonitoringEnabled = false }
        apply()
    }

    private func apply() {
        let charging = power.batteryState == .charging || power.batteryState == .full
        let wanted = !owners.isEmpty && charging && !power.isLowPowerModeEnabled
        guard wanted != isHolding else { return }
        if wanted {
            stateBeforeHold = idleTimer.isDisabled
            idleTimer.isDisabled = true
        } else {
            idleTimer.isDisabled = stateBeforeHold
        }
        isHolding = wanted
    }
}

/// `UIDevice` and `ProcessInfo`. Both notifications are taken on the main
/// queue: the power-state one posts on whatever thread flipped the mode.
@MainActor
final class DevicePowerSource: PowerSource {
    var onChange: (() -> Void)?
    private var observers: [NSObjectProtocol] = []

    init() {
        for name in [UIDevice.batteryStateDidChangeNotification, Notification.Name.NSProcessInfoPowerStateDidChange] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.onChange?() }
            })
        }
    }

    isolated deinit {
        for observer in observers { NotificationCenter.default.removeObserver(observer) }
    }

    var batteryState: UIDevice.BatteryState { UIDevice.current.batteryState }

    var isLowPowerModeEnabled: Bool { ProcessInfo.processInfo.isLowPowerModeEnabled }

    var isBatteryMonitoringEnabled: Bool {
        get { UIDevice.current.isBatteryMonitoringEnabled }
        set { UIDevice.current.isBatteryMonitoringEnabled = newValue }
    }
}

@MainActor
final class ApplicationIdleTimer: IdleTimer {
    var isDisabled: Bool {
        get { UIApplication.shared.isIdleTimerDisabled }
        set { UIApplication.shared.isIdleTimerDisabled = newValue }
    }
}
