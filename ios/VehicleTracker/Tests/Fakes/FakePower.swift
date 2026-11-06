import UIKit
@testable import VehicleTracker

/// A phone whose power the test plugs and unplugs. `plug`, `unplug` and
/// `setLowPowerMode` tell the coordinator, as the device's notifications do.
final class FakePowerSource: PowerSource {
    var batteryState: UIDevice.BatteryState = .unplugged
    var isLowPowerModeEnabled = false
    var isBatteryMonitoringEnabled = false
    var onChange: (() -> Void)?

    func plug(_ state: UIDevice.BatteryState = .charging) {
        batteryState = state
        onChange?()
    }

    func unplug() {
        batteryState = .unplugged
        onChange?()
    }

    func setLowPowerMode(_ on: Bool) {
        isLowPowerModeEnabled = on
        onChange?()
    }
}

final class FakeIdleTimer: IdleTimer {
    var isDisabled = false
}
