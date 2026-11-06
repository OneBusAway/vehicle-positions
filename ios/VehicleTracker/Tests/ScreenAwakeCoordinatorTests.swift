import Testing
@testable import VehicleTracker

@Suite struct ScreenAwakeCoordinatorTests {
    let power = FakePowerSource()
    let idleTimer = FakeIdleTimer()
    /// Owners are keyed by address, so each test makes its own objects.
    let tracking = ScreenAwakeLeaseOwner()
    let other = ScreenAwakeLeaseOwner()

    func coordinator() -> ScreenAwakeCoordinator {
        ScreenAwakeCoordinator(power: power, idleTimer: idleTimer)
    }

    @Test func aLeaseHoldsTheScreenWhileCharging() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled)
        #expect(awake.activeLeases == 1)
    }

    @Test func aFullBatteryOnPowerHoldsToo() {
        power.batteryState = .full
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled)
    }

    @Test func onBatteryALeaseHoldsNothing() {
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(!idleTimer.isDisabled)
        #expect(awake.activeLeases == 1, "the lease is kept for when the phone is plugged in")
    }

    @Test func pluggingInAndUnpluggingTurnTheHoldOnAndOff() {
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(!idleTimer.isDisabled)
        power.plug()
        #expect(idleTimer.isDisabled)
        power.unplug()
        #expect(!idleTimer.isDisabled)
    }

    @Test func lowPowerModeLetsTheScreenSleep() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled)
        power.setLowPowerMode(true)
        #expect(!idleTimer.isDisabled)
        power.setLowPowerMode(false)
        #expect(idleTimer.isDisabled)
    }

    @Test func theLastReleaseRestoresWhatTheTimerWas() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        awake.acquire(owner: ObjectIdentifier(other))
        #expect(awake.activeLeases == 2)
        awake.release(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled, "the other screen still holds it")
        awake.release(owner: ObjectIdentifier(other))
        #expect(!idleTimer.isDisabled)
        #expect(awake.activeLeases == 0)
    }

    @Test func anEarlierHoldBySomeoneElseSurvives() {
        power.batteryState = .charging
        idleTimer.isDisabled = true
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        awake.release(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled, "what the timer was before the hold is what it goes back to")
    }

    /// SwiftUI can run `onAppear` more than once for one appearance.
    @Test func aRepeatAcquireIsOneLease() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(awake.activeLeases == 1)
        awake.release(owner: ObjectIdentifier(tracking))
        #expect(awake.activeLeases == 0)
        #expect(!idleTimer.isDisabled)
    }

    @Test func aStrayReleaseChangesNothing() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        awake.release(owner: ObjectIdentifier(other))
        #expect(awake.activeLeases == 1)
        #expect(idleTimer.isDisabled)
        #expect(power.isBatteryMonitoringEnabled)
    }

    @Test func batteryMonitoringIsOnOnlyWhileALeaseIsHeld() {
        let awake = coordinator()
        #expect(!power.isBatteryMonitoringEnabled)
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(power.isBatteryMonitoringEnabled)
        awake.acquire(owner: ObjectIdentifier(other))
        awake.release(owner: ObjectIdentifier(tracking))
        #expect(power.isBatteryMonitoringEnabled, "another screen still wants it")
        awake.release(owner: ObjectIdentifier(other))
        #expect(!power.isBatteryMonitoringEnabled)
    }

    /// What the modifier does on `.background` and `.active`.
    @Test func goingToTheBackgroundReleasesAndComingBackAcquires() {
        power.batteryState = .charging
        let awake = coordinator()
        awake.acquire(owner: ObjectIdentifier(tracking))
        awake.release(owner: ObjectIdentifier(tracking))
        #expect(!idleTimer.isDisabled)
        awake.acquire(owner: ObjectIdentifier(tracking))
        #expect(idleTimer.isDisabled)
    }
}

/// Stands in for a mounted screen: leases are keyed by address.
final class ScreenAwakeLeaseOwner {}
