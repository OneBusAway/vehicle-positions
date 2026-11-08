import CoreLocation
import Testing
@testable import VehicleTracker

@Suite struct LocationAccessTests {
    let deadline = ManualSleep()
    /// Not `CoreLocationAccess.promptWait`, so a wait that ignored its limit
    /// would show.
    let limit: Duration = .seconds(7)

    /// With Location Services off no prompt can help, so the app reads as off
    /// whether or not it was ever asked.
    @Test(arguments: [
        (CLAuthorizationStatus.notDetermined, false, CLAccuracyAuthorization.fullAccuracy, LocationAccessState.servicesOff),
        (.notDetermined, true, .fullAccuracy, .notDetermined),
        (.denied, false, .fullAccuracy, .servicesOff),
        (.denied, true, .fullAccuracy, .denied),
        (.restricted, true, .fullAccuracy, .restricted),
        (.authorizedWhenInUse, true, .fullAccuracy, .allowed),
        (.authorizedWhenInUse, true, .reducedAccuracy, .approximateOnly),
    ])
    func locationServicesOffOutranksTheAppsAnswer(status: CLAuthorizationStatus, servicesOn: Bool,
                                                  accuracy: CLAccuracyAuthorization, expected: LocationAccessState) {
        #expect(CoreLocationAccess.state(status: status, accuracy: accuracy, servicesEnabled: { servicesOn }) == expected)
    }

    /// Apple warns the system-wide check can block the main thread, so an
    /// allowed app, the usual case, never makes it.
    @Test(arguments: [CLAuthorizationStatus.authorizedWhenInUse, .authorizedAlways])
    func anAllowedAppNeverReadsLocationServices(status: CLAuthorizationStatus) {
        var reads = 0
        let state = CoreLocationAccess.state(status: status, accuracy: .fullAccuracy) {
            reads += 1
            return true
        }
        #expect(state == .allowed)
        #expect(reads == 0)
    }

    @Test func anAnswerReleasesTheWaitAndItsDeadline() async {
        var cancelledSleeps = 0
        let prompts = PromptWait { duration in
            do {
                try await deadline.sleep(for: duration)
            } catch {
                cancelledSleeps += 1
                throw error
            }
        }
        var reads = 0
        let state = await prompts.wait(upTo: limit, read: { reads += 1; return .notDetermined }) {
            Task { prompts.answer(.allowed) }
        }
        #expect(state == .allowed)
        #expect(await eventually { cancelledSleeps == 1 }, "the answer cancels the deadline")
        #expect(deadline.asked == [limit])

        deadline.fire()
        await Task.yield()
        #expect(reads == 0, "an answered wait never reads iOS again")
    }

    /// The prompt never showed: the wait ends at its deadline with what iOS
    /// says then, read then and not before.
    @Test(.timeLimit(.minutes(1)))
    func noAnswerIsReleasedAtTheDeadlineWithWhatIOSSaysThen() async {
        let prompts = PromptWait(sleep: deadline.sleep(for:))
        var asks = 0
        var reads = 0
        let waiting = Task {
            await prompts.wait(upTo: limit, read: { reads += 1; return .notDetermined }) { asks += 1 }
        }
        #expect(await eventually { deadline.asked == [limit] })
        #expect(asks == 1)
        #expect(reads == 0)

        deadline.fire()
        #expect(await waiting.value == .notDetermined)
        #expect(reads == 1)
    }

    /// Resuming a continuation twice crashes the app, so an answer that comes
    /// after the deadline must find nothing left to resume.
    @Test(.timeLimit(.minutes(1)))
    func anAnswerAfterTheDeadlineIsNotDeliveredTwice() async {
        let prompts = PromptWait(sleep: deadline.sleep(for:))
        let first = Task { await prompts.wait(upTo: limit, read: { .notDetermined }) {} }
        #expect(await eventually { deadline.asked.count == 1 })
        deadline.fire()
        #expect(await first.value == .notDetermined)

        prompts.answer(.allowed)

        let second = Task { await prompts.wait(upTo: limit, read: { .notDetermined }) {} }
        #expect(await eventually { deadline.asked.count == 2 })
        prompts.answer(.denied)
        #expect(await second.value == .denied, "the late answer is not kept for the next wait")
    }

    @Test(.timeLimit(.minutes(1)))
    func oneAnswerReleasesEveryWaiter() async {
        let prompts = PromptWait(sleep: deadline.sleep(for:))
        let first = Task { await prompts.wait(upTo: limit, read: { .notDetermined }) {} }
        let second = Task { await prompts.wait(upTo: limit, read: { .notDetermined }) {} }
        #expect(await eventually { deadline.asked.count == 2 })

        prompts.answer(.allowed)
        #expect(await first.value == .allowed)
        #expect(await second.value == .allowed)
    }
}
