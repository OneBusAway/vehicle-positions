import Testing
import VehiclePositionsKit
@testable import VehicleTracker

@Suite struct MapFollowingTests {
    let clock = ManualRideClock()

    func following(voiceOver: Bool = false) -> MapFollowing {
        MapFollowing(clock: clock, isVoiceOverRunning: { voiceOver })
    }

    @Test func aPanStopsFollowingAndTenSecondsAfterTheTouchEndsItFollowsAgain() async {
        let map = following()
        #expect(map.isFollowing)
        map.userMovedMap()
        #expect(!map.isFollowing)
        map.userTouchEnded()
        #expect(await clock.waitForSleepers(atLeast: 1))

        clock.advance(by: .seconds(9))
        await Task.yield()
        #expect(!map.isFollowing, "9 s is not 10")
        clock.advance(by: .seconds(1))
        #expect(await eventually { map.isFollowing })
    }

    @Test func aNewTouchRestartsTheCount() async {
        let map = following()
        map.userMovedMap()
        map.userTouchEnded()
        #expect(await clock.waitForSleepers(atLeast: 1))
        clock.advance(by: .seconds(9))

        map.userMovedMap()
        #expect(await eventually { clock.sleeperCount == 0 }, "the first count is cancelled")
        map.userTouchEnded()
        #expect(await clock.waitForSleepers(atLeast: 1))
        clock.advance(by: .seconds(9))
        await Task.yield()
        #expect(!map.isFollowing, "the count starts over at the second touch")
        clock.advance(by: .seconds(1))
        #expect(await eventually { map.isFollowing })
    }

    @Test func recenterFollowsAtOnceAndCancelsTheReturn() async {
        let map = following()
        map.userMovedMap()
        map.userTouchEnded()
        #expect(await clock.waitForSleepers(atLeast: 1))
        clock.advance(by: .seconds(3))

        map.follow()
        #expect(map.isFollowing)
        #expect(await eventually { clock.sleeperCount == 0 })
        // Nothing fires later into a map that is already following.
        clock.advance(by: .seconds(10))
        await Task.yield()
        #expect(map.isFollowing)
    }

    @Test func leavingTrackingCancelsTheReturn() async {
        let map = following()
        map.userMovedMap()
        map.userTouchEnded()
        #expect(await clock.waitForSleepers(atLeast: 1))

        map.stop()
        #expect(await eventually { clock.sleeperCount == 0 })
        clock.advance(by: .seconds(10))
        await Task.yield()
        #expect(!map.isFollowing)
    }

    /// A timer must not undo a VoiceOver user's exploring; Re-center is
    /// theirs to tap.
    @Test func withVoiceOverThereIsNoTimedReturnButRecenterWorks() async {
        let map = following(voiceOver: true)
        map.userMovedMap()
        map.userTouchEnded()
        await Task.yield()
        #expect(clock.sleeperCount == 0)
        clock.advance(by: .seconds(10))
        await Task.yield()
        #expect(!map.isFollowing)

        map.follow()
        #expect(map.isFollowing)
    }
}
