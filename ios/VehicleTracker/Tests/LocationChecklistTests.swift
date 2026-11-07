import Foundation
import Testing
@testable import VehicleTracker

/// A port of the location, precise and location-services cases in Android's
/// `PermissionStepsTest`, reduced to what iOS asks: one prompt each, and a
/// refusal that is final at once.
@Suite struct LocationChecklistTests {
    typealias State = LocationChecklist.RowState

    func checklist(_ access: LocationAccessState, preciseAsked: Bool = false) -> LocationChecklist {
        var checklist = LocationChecklist(.notDetermined)
        if preciseAsked { checklist.answeredPrecise(access) } else { checklist.read(access) }
        return checklist
    }

    func rows(_ checklist: LocationChecklist) -> [State] {
        LocationChecklist.Row.allCases.map(checklist.state(of:))
    }

    /// Every access state iOS reports, with and without the precise prompt asked.
    static let everyState: [(LocationAccessState, Bool)] = [LocationAccessState.allowed, .notDetermined, .denied, .restricted, .servicesOff, .approximateOnly]
        .flatMap { [($0, false), ($0, true)] }

    @Test func eachStateShowsItsRowsWhatContinueAsksAndWhetherItCanBeLeft() {
        let first = checklist(.notDetermined)
        #expect(rows(first) == [.next, .notReached, .notReached])
        #expect(first.continueAsk == .location)
        #expect(!first.canCancel)

        let approximate = checklist(.approximateOnly)
        #expect(rows(approximate) == [.allowed, .next, .notReached])
        #expect(approximate.continueAsk == .precise)
        #expect(!approximate.canCancel)

        let declinedPrecise = checklist(.approximateOnly, preciseAsked: true)
        #expect(rows(declinedPrecise) == [.allowed, .approximate, .notReached])
        #expect(declinedPrecise.continueAsk == nil)
        #expect(declinedPrecise.canCancel)

        let refused = checklist(.denied)
        #expect(rows(refused) == [.neededInSettings, .notReached, .notReached])
        #expect(refused.continueAsk == nil)
        #expect(refused.canCancel)

        let servicesOff = checklist(.servicesOff)
        #expect(rows(servicesOff) == [.notReached, .notReached, .off])
        #expect(servicesOff.continueAsk == nil)
        #expect(servicesOff.canCancel)

        let ready = checklist(.allowed)
        #expect(rows(ready) == [.allowed, .allowed, .allowed])
        #expect(ready.isReady)
        #expect(ready.continueAsk == nil)
    }

    @Test func onlyAllowedNeedsNoChecklist() {
        #expect(!LocationChecklist.needed(.allowed))
        for access in [LocationAccessState.notDetermined, .denied, .restricted, .servicesOff, .approximateOnly] {
            #expect(LocationChecklist.needed(access), "\(access)")
        }
    }

    @Test func aFirstTripWalksIOSsPromptsInTurn() {
        var walk = LocationChecklist(.notDetermined)
        #expect(walk.continueAsk == .location)
        // The driver allows location but turns Precise off in the prompt.
        walk.read(.approximateOnly)
        #expect(walk.continueAsk == .precise)
        #expect(!walk.isReady)
        walk.answeredPrecise(.allowed)
        #expect(walk.isReady)
    }

    @Test func approximateAfterAskingWaitsForTheDriversTryAgain() {
        var declined = checklist(.approximateOnly, preciseAsked: true)
        #expect(declined.continueAsk == nil)
        #expect(!declined.isReady)
        #expect(declined.state(of: .precise).action == .askPrecise)
        declined.answeredPrecise(.allowed)
        #expect(declined.isReady)
    }

    @Test func aRestrictionReadsAsARefusal() {
        #expect(rows(checklist(.restricted)) == rows(checklist(.denied)))
        #expect(checklist(.restricted).state(of: .location).action == .openSettings)
    }

    @Test func locationServicesIsReachedOnlyWhileOff() {
        for (access, asked) in Self.everyState {
            let expected: State = access == .servicesOff ? .off : access == .allowed ? .allowed : .notReached
            #expect(checklist(access, preciseAsked: asked).state(of: .locationServices) == expected, "\(access) \(asked)")
        }
        #expect(checklist(.servicesOff).state(of: .locationServices).action == .readAgain)
    }

    /// App Review 5.1.1(iv): a screen before a permission prompt must not be
    /// one the driver can leave without reaching the prompt.
    @Test func cancelNeverShowsWhileContinueWouldPrompt() {
        for (access, asked) in Self.everyState {
            let checklist = checklist(access, preciseAsked: asked)
            if checklist.continueAsk != nil {
                #expect(!checklist.canCancel, "\(access) \(asked)")
            }
        }
    }

    @Test func onlyARowThatNeedsTheDriverHasAnAction() {
        #expect(State.neededInSettings.action == .openSettings)
        #expect(State.approximate.action == .askPrecise)
        #expect(State.off.action == .readAgain)
        for state in [State.next, .allowed, .notReached] {
            #expect(state.action == nil, "\(state)")
        }
    }
}
