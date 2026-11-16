import Foundation
import Testing
@testable import VehicleTracker

/// The welcome's three timelines, driven by a clock the test moves: when
/// each beat lands, where a picture rests, and what Next and back do to a
/// picture that is still playing.
@Suite struct WelcomeFlowTests {
    let clock = StepClock()

    /// A welcome that has just come onto the screen.
    func started() -> WelcomeFlow {
        let flow = WelcomeFlow(sleep: clock.sleep(for:))
        flow.start()
        return flow
    }

    func picture(_ flow: WelcomeFlow) -> WelcomePicture {
        flow.picture(still: false)
    }

    @Test func theWelcomeOpensOnItsFirstScreenWithNothingPlayedYet() {
        let flow = started()
        #expect(flow.step == .riders)
        #expect(flow.isFirst && !flow.isLast)
        #expect(picture(flow) == .riders(.start))
    }

    /// Walkthrough spec §4, screen 1, by the millisecond.
    @Test func theRidersPictureReachesEachBeatAtItsTime() async {
        let flow = started()
        let beats: [(Int, RidersBeat)] = [
            (499, .start), (500, .live), (999, .live), (1000, .secondRing), (1200, .ridersNotice), (1300, .firstWave),
            (1500, .firstReport), (1620, .secondWave), (1940, .thirdWave), (2200, .secondReport), (2899, .secondReport),
            (2900, .thirdReport), (3399, .thirdReport), (3400, .rest),
        ]
        for (time, beat) in beats {
            await clock.advance(to: .milliseconds(time))
            #expect(picture(flow) == .riders(beat), "at \(time) ms")
        }
    }

    @Test func theChoicesPictureReachesEachBeatAtItsTime() async {
        let flow = started()
        flow.next()
        let beats: [(Int, ChoicesBeat)] = [
            (0, .start), (50, .cardsRise), (599, .cardsRise), (600, .vehicleChosen), (1100, .routeChosen), (1600, .runChosen),
            (2100, .ridersSeeYou), (2800, .secondReport), (3150, .thirdReport), (3399, .thirdReport), (3400, .rest),
        ]
        for (time, beat) in beats {
            await clock.advance(to: .milliseconds(time))
            #expect(picture(flow) == .choices(beat), "at \(time) ms")
        }
    }

    @Test func theReportingPictureReachesEachBeatAtItsTime() async {
        let flow = started()
        flow.next()
        flow.next()
        let beats: [(Int, ReportingBeat)] = [
            (149, .start), (150, .locationRow), (400, .chargeRow), (650, .appRow), (900, .appIcon), (1000, .label),
            (1099, .label), (1100, .rest),
        ]
        for (time, beat) in beats {
            await clock.advance(to: .milliseconds(time))
            #expect(picture(flow) == .reporting(beat), "at \(time) ms")
        }
    }

    /// A picture plays once: it stays at rest and asks for no more time. It
    /// also ends well inside the 5 seconds past which the accessibility
    /// standard asks for a pause control.
    @Test(arguments: WelcomeStep.allCases)
    func aPictureRestsWhereItEndsAndDoesNotLoop(step: WelcomeStep) async throws {
        let flow = started()
        for _ in 0..<step.rawValue { flow.next() }
        let rest = try #require(step.timeline.last)
        #expect(rest.at <= .seconds(4))
        await clock.advance(to: rest.at)
        #expect(picture(flow) == rest.picture)
        #expect(clock.sleepers == 0, "nothing more is waited for")
        await clock.advance(to: .seconds(60))
        #expect(picture(flow) == rest.picture)
    }

    /// Reduce Motion: every screen opens finished, whatever the timeline
    /// under it has reached.
    @Test func theStillVersionIsAtRestFromTheFirstFrame() async {
        let flow = started()
        #expect(flow.picture(still: true) == .riders(.rest))
        await clock.advance(to: .milliseconds(1200))
        #expect(flow.picture(still: true) == .riders(.rest))
        flow.next()
        #expect(flow.picture(still: true) == .choices(.rest))
        flow.next()
        #expect(flow.picture(still: true) == .reporting(.rest))
        flow.back()
        #expect(flow.picture(still: true) == .choices(.rest))
    }

    @Test func nextGoesForwardAndBackGoesBackOneScreenAtATime() {
        let flow = started()
        flow.next()
        #expect(flow.step == .choices)
        #expect(flow.isForward)
        #expect(!flow.isFirst && !flow.isLast)
        flow.next()
        #expect(flow.step == .reporting)
        #expect(flow.isLast)
        flow.back()
        #expect(flow.step == .choices)
        #expect(!flow.isForward)
        flow.back()
        #expect(flow.step == .riders)
        #expect(flow.isFirst)
    }

    /// The last screen's button ends the welcome, and the first screen has
    /// no back: neither moves, or starts a picture again.
    @Test func thereIsNothingPastTheLastScreenOrBeforeTheFirst() async {
        let flow = started()
        await clock.advance(to: .milliseconds(500))
        flow.back()
        #expect(flow.step == .riders)
        #expect(flow.showing == 0)
        #expect(picture(flow) == .riders(.live), "the picture plays on")
        flow.next()
        flow.next()
        let showing = flow.showing
        await clock.advance(by: .milliseconds(900))
        flow.next()
        #expect(flow.step == .reporting)
        #expect(flow.showing == showing)
        #expect(picture(flow) == .reporting(.appIcon), "the picture plays on")
    }

    /// Each showing of a screen is a new one, so the view for it is new and
    /// the one it replaces is left as it was while it fades.
    @Test func everyMoveIsANewShowing() {
        let flow = started()
        var seen = [flow.showing]
        flow.next()
        seen.append(flow.showing)
        flow.back()
        seen.append(flow.showing)
        flow.next()
        seen.append(flow.showing)
        #expect(Set(seen).count == 4)
    }

    @Test func aScreenComeBackToPlaysItsPictureAgainFromTheStart() async {
        let flow = started()
        await clock.advance(to: .milliseconds(3400))
        #expect(picture(flow) == .riders(.rest))
        flow.next()
        #expect(picture(flow) == .choices(.start))
        await clock.advance(by: .milliseconds(700))
        #expect(picture(flow) == .choices(.vehicleChosen))
        flow.back()
        #expect(picture(flow) == .riders(.start), "not where it rested")
        await clock.advance(by: .milliseconds(500))
        #expect(picture(flow) == .riders(.live))
    }

    /// A picture cut off by Next is done with: its remaining beats never
    /// land on the screen that replaced it.
    @Test func aPictureCutOffByNextNeverPlaysIntoTheNextScreen() async {
        let flow = started()
        await clock.advance(to: .milliseconds(1250))
        #expect(picture(flow) == .riders(.ridersNotice))
        flow.next()
        for _ in 0..<80 {
            await clock.advance(by: .milliseconds(50))
            #expect(picture(flow).step == .choices)
        }
        #expect(picture(flow) == .choices(.rest))
        #expect(clock.sleepers == 0)
    }

    /// A beat whose wait ends as the driver taps Next. The wait comes back
    /// without an error, the tap is handled first, and the beat must not
    /// land on the screen that has just appeared.
    @Test func aBeatWhoseWaitEndsAsTheScreenChangesDoesNotLand() async {
        var waiting: CheckedContinuation<Void, Never>?
        let flow = WelcomeFlow(sleep: { _ in await withCheckedContinuation { waiting = $0 } })
        flow.start()
        await settle()
        waiting?.resume()
        flow.next()
        await settle()
        #expect(flow.picture(still: false) == .choices(.start))
        flow.stop()
        waiting?.resume()
        await settle()
        #expect(flow.picture(still: false) == .choices(.start))
    }

    func settle() async {
        for _ in 0..<4 { await Task.yield() }
    }

    /// Next, Next, back, as fast as a thumb: one picture is playing at the
    /// end, the showing one, from its start.
    @Test func fastNextAndBackLeaveOnePicturePlayingFromItsStart() async {
        let flow = started()
        flow.next()
        flow.next()
        flow.back()
        #expect(flow.step == .choices)
        #expect(!flow.isForward)
        #expect(picture(flow) == .choices(.start))
        await clock.advance(by: .milliseconds(50))
        #expect(picture(flow) == .choices(.cardsRise))
        #expect(clock.sleepers == 1, "the pictures left behind wait for nothing")
        await clock.advance(by: .milliseconds(550))
        #expect(picture(flow) == .choices(.vehicleChosen))
    }

    /// Taps that land while a picture is mid-beat, at every point in it.
    @Test func aMoveAtAnyMomentStartsTheNextPictureClean() async {
        for cut in stride(from: 0, through: 3500, by: 250) {
            let clock = StepClock()
            let flow = WelcomeFlow(sleep: clock.sleep(for:))
            flow.start()
            await clock.advance(to: .milliseconds(cut))
            flow.next()
            #expect(flow.picture(still: false) == .choices(.start), "cut at \(cut) ms")
            await clock.advance(by: .milliseconds(3400))
            #expect(flow.picture(still: false) == .choices(.rest), "cut at \(cut) ms")
        }
    }

    @Test func aWelcomeThatLeftTheScreenStopsPlaying() async {
        let flow = started()
        await clock.advance(to: .milliseconds(500))
        flow.stop()
        await clock.advance(to: .seconds(10))
        #expect(picture(flow) == .riders(.live))
        #expect(clock.sleepers == 0)
    }

    /// Next brings the new screen in from the trailing side while the old
    /// one leaves towards the leading side; back is the other way round.
    @Test func aScreenSlidesInAheadOfTheMoveAndOutBehindIt() {
        #expect(WelcomeStepTransition.shift(.willAppear, isForward: true) == 1)
        #expect(WelcomeStepTransition.shift(.didDisappear, isForward: true) == -1)
        #expect(WelcomeStepTransition.shift(.willAppear, isForward: false) == -1)
        #expect(WelcomeStepTransition.shift(.didDisappear, isForward: false) == 1)
        #expect(WelcomeStepTransition.shift(.identity, isForward: true) == 0)
        #expect(WelcomeStepTransition.shift(.identity, isForward: false) == 0)
    }

    // MARK: What a beat shows

    @Test func theRidersPictureCountsItsRingsWavesAndReports() {
        #expect(RidersBeat.start.ringsLeft == 0)
        #expect(RidersBeat.live.ringsLeft == 1)
        #expect(RidersBeat.secondRing.ringsLeft == 2)
        #expect(RidersBeat.firstReport.ringsLeft == 3)
        #expect(RidersBeat.ridersNotice.ridersWaving == 0)
        #expect(RidersBeat.firstWave.ridersWaving == 1)
        #expect(RidersBeat.secondWave.ridersWaving == 2)
        #expect(RidersBeat.thirdWave.ridersWaving == 3)
        #expect(RidersBeat.firstWave.reportsSent == 0)
        #expect(RidersBeat.firstReport.reportsSent == 1)
        #expect(RidersBeat.secondReport.reportsSent == 2)
        #expect(RidersBeat.thirdReport.reportsSent == 3)
        #expect((RidersBeat.rest.ringsLeft, RidersBeat.rest.ridersWaving, RidersBeat.rest.reportsSent) == (3, 3, 3))
    }

    @Test func theChoicesPictureCountsItsChoicesAndReports() {
        #expect(ChoicesBeat.cardsRise.choicesMade == 0)
        #expect(ChoicesBeat.vehicleChosen.choicesMade == 1)
        #expect(ChoicesBeat.routeChosen.choicesMade == 2)
        #expect(ChoicesBeat.runChosen.choicesMade == 3)
        #expect(ChoicesBeat.runChosen.reportsSent == 0)
        #expect(ChoicesBeat.ridersSeeYou.reportsSent == 1)
        #expect(ChoicesBeat.secondReport.reportsSent == 2)
        #expect(ChoicesBeat.thirdReport.reportsSent == 3)
        #expect((ChoicesBeat.rest.choicesMade, ChoicesBeat.rest.reportsSent) == (3, 3))
    }

    /// Each row's symbol moves as the row after it comes in.
    @Test func theReportingPictureCountsItsRowsAndTheirSymbols() {
        #expect((ReportingBeat.start.rowsIn, ReportingBeat.start.iconsMoved) == (0, 0))
        #expect((ReportingBeat.locationRow.rowsIn, ReportingBeat.locationRow.iconsMoved) == (1, 0))
        #expect((ReportingBeat.chargeRow.rowsIn, ReportingBeat.chargeRow.iconsMoved) == (2, 1))
        #expect((ReportingBeat.appRow.rowsIn, ReportingBeat.appRow.iconsMoved) == (3, 2))
        #expect((ReportingBeat.appIcon.rowsIn, ReportingBeat.appIcon.iconsMoved) == (3, 3))
        #expect((ReportingBeat.rest.rowsIn, ReportingBeat.rest.iconsMoved) == (3, 3))
    }

    /// Every timeline starts with its first frame, at no time at all, and
    /// its beats come in order.
    @Test(arguments: WelcomeStep.allCases)
    func aTimelineStartsAtZeroAndRunsInOrder(step: WelcomeStep) {
        let times = step.timeline.map(\.at)
        #expect(times.first == .zero)
        #expect(zip(times, times.dropFirst()).allSatisfy { $0 < $1 })
        #expect(step.timeline.allSatisfy { $0.picture.step == step })
    }
}

/// Time that moves when a test says so. A sleeper wakes when the clock
/// reaches its time, one at a time and in order, and gets to sleep again or
/// finish before the clock moves on, so a picture's beats land as they would.
@MainActor final class StepClock {
    private var now = Duration.zero
    private var parked: [Int: (wakeAt: Duration, continuation: CheckedContinuation<Void, any Error>)] = [:]
    private var nextID = 0

    var sleepers: Int { parked.count }

    func sleep(for duration: Duration) async throws {
        let id = nextID
        nextID += 1
        let wakeAt = now + duration
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
                if Task.isCancelled {
                    continuation.resume(throwing: CancellationError())
                } else {
                    parked[id] = (wakeAt, continuation)
                }
            }
        } onCancel: {
            Task { @MainActor in wake(id, throwing: CancellationError()) }
        }
    }

    func advance(by duration: Duration) async {
        await advance(to: now + duration)
    }

    /// Moves time on to `time`, counted from the clock's start.
    func advance(to time: Duration) async {
        await settle()
        while let next = parked.min(by: { $0.value.wakeAt < $1.value.wakeAt }), next.value.wakeAt <= time {
            now = next.value.wakeAt
            wake(next.key, throwing: nil)
            await settle()
        }
        now = max(now, time)
    }

    private func wake(_ id: Int, throwing error: (any Error)?) {
        guard let sleeper = parked.removeValue(forKey: id) else { return }
        if let error {
            sleeper.continuation.resume(throwing: error)
        } else {
            sleeper.continuation.resume()
        }
    }

    /// Lets whatever was woken or cancelled run until it waits again.
    private func settle() async {
        for _ in 0..<4 { await Task.yield() }
    }
}
