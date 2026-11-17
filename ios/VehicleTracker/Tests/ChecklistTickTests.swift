import SwiftUI
import Testing
@testable import VehicleTracker

/// The tick on Before you start: a row ticks when the sheet sees iOS allow
/// it, never for a row already allowed when the sheet opens, never for a
/// refusal and only once per row, as on Android. VoiceOver says the new
/// state once, and with Reduce Motion the check shows with no animation.
@Suite struct ChecklistTickTests {
    func ticked(_ checklist: LocationChecklist) -> [Bool] {
        LocationChecklist.Row.allCases.map(checklist.hasTicked)
    }

    @Test func aRowTicksWhenTheSheetSeesIOSAllowIt() {
        // The driver allows location but turns Precise off in the prompt.
        var walk = LocationChecklist(.notDetermined)
        #expect(ticked(walk) == [false, false, false])
        walk.read(.approximateOnly)
        #expect(ticked(walk) == [true, false, false])
        walk.answeredPrecise(.allowed)
        #expect(ticked(walk) == [true, true, true])
    }

    @Test func comingBackFromSettingsWithLocationAllowedTicksIt() {
        var refused = LocationChecklist(.denied)
        refused.read(.approximateOnly)
        #expect(ticked(refused) == [true, false, false])

        var servicesOff = LocationChecklist(.servicesOff)
        servicesOff.read(.allowed)
        #expect(ticked(servicesOff) == [true, true, true])
    }

    @Test func aRowAllowedWhenTheSheetOpensNeverTicks() {
        var opened = LocationChecklist(.approximateOnly)
        #expect(opened.state(of: .location) == .allowed)
        // The read on coming back to the app sees the same answer.
        opened.read(.approximateOnly)
        #expect(ticked(opened) == [false, false, false])
        // Turned off in Settings and on again: the check shows, with no tick.
        opened.read(.denied)
        opened.read(.approximateOnly)
        #expect(opened.state(of: .location) == .allowed)
        #expect(ticked(opened) == [false, false, false])
    }

    @Test func aRefusalNeverTicks() {
        var location = LocationChecklist(.notDetermined)
        location.read(.denied)
        #expect(ticked(location) == [false, false, false])

        var precise = LocationChecklist(.approximateOnly)
        precise.answeredPrecise(.approximateOnly)
        #expect(precise.state(of: .precise) == .approximate)
        #expect(ticked(precise) == [false, false, false])

        var services = LocationChecklist(.notDetermined)
        services.read(.servicesOff)
        #expect(ticked(services) == [false, false, false])
    }

    /// The prompt's answer and the read when the app comes back to the
    /// front can both see the same grant, and a row refused in Settings can
    /// be allowed again: either way the row has ticked once, so the view's
    /// trigger does not change a second time.
    @Test func aRowTicksOnlyOnce() {
        var walk = LocationChecklist(.notDetermined)
        walk.read(.approximateOnly)
        let once = walk
        walk.read(.approximateOnly)
        #expect(walk == once)

        walk.read(.denied)
        #expect(ticked(walk) == [true, false, false])
        walk.read(.approximateOnly)
        #expect(walk == once)
    }

    @Test func voiceOverSaysTheRowIsAllowedOnce() {
        var walk = LocationChecklist(.notDetermined)
        let asked = walk
        walk.read(.approximateOnly)
        #expect(ChecklistText.announcement(from: asked, to: walk)
            == "Location, allowed. Precise location. So riders see the vehicle at the right stop. Next.")
        let answered = walk
        walk.read(.approximateOnly)
        #expect(ChecklistText.announcement(from: answered, to: walk) == nil)
    }

    @Test func eachRowSaysItsOwnNameWhenAllowed() {
        #expect(ChecklistText.allowedNow(.location) == "Location, allowed.")
        #expect(ChecklistText.allowedNow(.precise) == "Precise location, allowed.")
        #expect(ChecklistText.allowedNow(.locationServices) == "Location services, allowed.")
    }

    /// A row allowed again after a refusal is said the same way, though it
    /// does not tick again.
    @Test func allowedAgainIsSaidTheSameWay() {
        var walk = LocationChecklist(.notDetermined)
        walk.read(.approximateOnly)
        walk.read(.denied)
        let refused = walk
        walk.read(.approximateOnly)
        #expect(ChecklistText.announcement(from: refused, to: walk)
            == "Location, allowed. Precise location. So riders see the vehicle at the right stop. Next.")
    }

    /// Only a row allowed now says "allowed": a refusal and a row reached
    /// for the first time are read in full, as before.
    @Test func otherChangesAreReadInFull() {
        var refused = LocationChecklist(.notDetermined)
        let asked = refused
        refused.read(.denied)
        #expect(ChecklistText.announcement(from: asked, to: refused) == "Location. Location is needed to report.")

        var servicesOn = LocationChecklist(.servicesOff)
        let off = servicesOn
        servicesOn.read(.notDetermined)
        #expect(ChecklistText.announcement(from: off, to: servicesOn)
            == "Location. So the app knows where the vehicle is. Next. Location services. Location must be on for the whole phone.")
    }

    /// The row's own label, read when the driver moves to it, keeps its words.
    @Test func theRowsLabelIsUnchangedByTheTick() {
        #expect(ChecklistText.spoken(.location, .allowed) == "Location. So the app knows where the vehicle is. Allowed.")
    }

    @Test func reduceMotionShowsTheCheckWithNoAnimation() {
        #expect(ChecklistMotion.tick(reduceMotion: true) == nil)
        #expect(ChecklistMotion.change(reduceMotion: true) == nil)
        #expect(ChecklistMotion.dip(reduceMotion: true) == 0)
        #expect(ChecklistMarker.Look(.allowed) == .done)
    }

    /// The app's own springs: `.smooth` for a change and for the row's dip,
    /// which is small and never overshoots, and the small bounce of
    /// `.snappy` only for the check.
    @Test func withMotionTheTickLandsOnTheAppsSprings() {
        #expect(ChecklistMotion.change(reduceMotion: false) == .smooth(duration: 0.25))
        #expect(ChecklistMotion.tick(reduceMotion: false) == .snappy(duration: 0.35))
        #expect(ChecklistMotion.dip(reduceMotion: false) == 2)
        #expect(ChecklistMotion.push == .smooth(duration: 0.12))
        #expect(ChecklistMotion.settle == .smooth(duration: 0.3))
    }
}
