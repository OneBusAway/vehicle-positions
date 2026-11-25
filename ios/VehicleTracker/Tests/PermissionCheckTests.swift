import CoreGraphics
import Testing
@testable import VehicleTracker

/// Permission check: what each row says and ticks for each thing iOS can say
/// about location, and where its line's vehicle waits.
@Suite struct PermissionCheckTests {
    nonisolated struct Case: Sendable, CustomTestStringConvertible {
        let access: LocationAccessState
        /// Location, precise location, Location Services.
        let reasons: [String]
        let waitsOver: Int
        let summary: String

        var testDescription: String { "\(access)" }
    }

    nonisolated static let why = ["So the app knows where the vehicle is", "So riders see the vehicle at the right stop",
                                  "Location must be on for the whole phone"]

    nonisolated static let cases = [
        Case(access: .allowed, reasons: why, waitsOver: 3, summary: "All allowed"),
        Case(access: .notDetermined, reasons: why, waitsOver: 0, summary: "Not asked yet"),
        Case(access: .denied, reasons: ["Location is needed to report", why[1], why[2]], waitsOver: 0, summary: "Location is not allowed"),
        Case(access: .restricted, reasons: ["Location is needed to report", why[1], why[2]], waitsOver: 0, summary: "Location is not allowed"),
        Case(access: .approximateOnly, reasons: [why[0], "Only approximate was allowed", why[2]], waitsOver: 1, summary: "Precise location is off"),
        Case(access: .servicesOff, reasons: why, waitsOver: 0, summary: "Location is off"),
    ]

    func readiness(_ access: LocationAccessState) -> Readiness {
        Readiness(access: access, waiting: 0)
    }

    @Test(arguments: cases) func eachAnswerHasItsWords(_ expected: Case) {
        let readiness = readiness(expected.access)
        let reasons = LocationChecklist.Row.allCases.map { ChecklistText.reason(of: $0, readiness.checklistState(of: $0)) }
        #expect(reasons == expected.reasons)
        #expect(SettingsText.permissions(readiness) == expected.summary)
        #expect(PermissionCheck.waitsOver(readiness) == expected.waitsOver)
    }

    /// Reports kept on the phone change nothing the app is allowed.
    @Test func reportsWaitingAreNoPermission() {
        #expect(SettingsText.permissions(Readiness(access: .allowed, waiting: 12)) == "All allowed")
    }

    @Test func thePagesOwnWords() {
        #expect(PermissionCheckText.title == "Permission check")
        #expect(PermissionCheckText.intro == "What the app needs to report your vehicle.")
        #expect(PermissionCheckText.riders == "Riders")
        #expect(PermissionCheckText.ridersSee == "They see your vehicle during a trip.")
        #expect(PermissionCheckText.ridersDoNotSee == "They can't see you yet.")
        #expect(SettingsText.phoneSection == "This phone")
    }

    /// App Review: no button that raises iOS's prompt says Allow.
    @Test func theButtonThatAsksSaysContinue() {
        #expect(PermissionCheckText.name(of: .ask) == "Continue")
    }

    @Test func aRowsMarkerIsItsStops() {
        let looks = [Readiness.Stop.allowed, .missing, .beyond].map(ChecklistMarker.Look.init(stop:))
        #expect(looks == [.done, .missing, .pending])
    }

    // MARK: The tick

    @Test func aRowTicksWhenThePageSeesIOSAllowIt() {
        var check = PermissionCheck(readiness(.notDetermined))
        #expect(check.ticked == [])
        check.read(readiness(.approximateOnly))
        #expect(check.ticked == [.location])
        check.read(readiness(.allowed))
        #expect(check.ticked == [.location, .precise])
    }

    @Test func aRowAllowedWhenThePageOpensNeverTicks() {
        var check = PermissionCheck(readiness(.approximateOnly))
        check.read(readiness(.denied))
        check.read(readiness(.approximateOnly))
        #expect(check.ticked == [])
    }

    @Test func aRowTicksOnce() {
        var check = PermissionCheck(readiness(.denied))
        check.read(readiness(.allowed))
        check.read(readiness(.denied))
        check.read(readiness(.allowed))
        #expect(check.ticked == [.location, .precise])
    }

    // MARK: What VoiceOver says

    @Test func aRowsButtonIsReadWithTheRowsName() {
        #expect(PermissionCheckText.spoken(.openSettings, for: .location) == "Location, Open Settings")
        #expect(PermissionCheckText.spoken(.readAgain, for: .locationServices) == "Location services, Try again")
    }

    @Test func aRowAllowedNowIsSaidOnce() {
        #expect(PermissionCheckText.announcement(from: readiness(.denied), to: readiness(.allowed))
            == "Location, allowed. Precise location, allowed.")
    }

    @Test func aRowTakenAwayIsSaidWithWhatItNeeds() {
        #expect(PermissionCheckText.announcement(from: readiness(.allowed), to: readiness(.approximateOnly))
            == "Precise location. Only approximate was allowed.")
    }

    @Test func nothingIsSaidWhenNoRowChanges() {
        for expected in Self.cases {
            #expect(PermissionCheckText.announcement(from: readiness(expected.access), to: readiness(expected.access)) == nil)
        }
    }

    // MARK: The line

    /// Three rows 96 pt tall and the riders' row, under the space the vehicle
    /// waits in.
    let places = CheckPlaces(rows: (0..<4).map { CGRect(x: 0, y: CheckPlaces.home + 96 * CGFloat($0), width: 300, height: 96) })

    @Test func theVehicleWaitsOverEachRow() {
        #expect(places.vehicle(over: 0) == 22)
        #expect(places.vehicle(over: 1) == 140)
        #expect(places.vehicle(over: 2) == 236)
        #expect(places.vehicle(over: 3) == 332)
    }

    @Test func theLineEndsAtTheRidersMarker() {
        #expect(places.end == 380)
    }

    @Test func beforeTheRowsAreLaidOutTheLineIsNowhere() {
        let none = CheckPlaces(rows: [])
        #expect(none.vehicle(over: 0) == 0)
        #expect(none.end == 0)
    }
}
