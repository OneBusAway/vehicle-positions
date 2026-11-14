import Testing
@testable import VehicleTracker

@Suite struct OccupancyRowTests {
    @Test func theFourChoicesInOrderOfHowFull() {
        #expect(Occupancy.allCases.map(OccupancyText.name) == ["Seats free", "Few seats", "Standing", "Full"])
        #expect(Occupancy.allCases.map(\.rawValue) == ["MANY_SEATS_AVAILABLE", "FEW_SEATS_AVAILABLE", "STANDING_ROOM_ONLY", "FULL"])
    }

    /// VoiceOver reads one element: the question, then the choice or "Not set".
    @Test func voiceOverValueNamesTheChoice() {
        #expect(OccupancyText.value(nil) == "Not set")
        #expect(OccupancyText.value(.full) == "Full")
    }

    /// Swiping up goes one step fuller, down one step emptier, and down from
    /// the emptiest clears the choice; neither end wraps.
    @Test func swipingStepsThroughTheChoices() {
        #expect(Occupancy.stepped(nil, up: true) == .manySeatsAvailable)
        #expect(Occupancy.stepped(.manySeatsAvailable, up: true) == .fewSeatsAvailable)
        #expect(Occupancy.stepped(.standingRoomOnly, up: true) == .full)
        #expect(Occupancy.stepped(.full, up: true) == .full)
        #expect(Occupancy.stepped(.full, up: false) == .standingRoomOnly)
        #expect(Occupancy.stepped(.manySeatsAvailable, up: false) == nil)
        #expect(Occupancy.stepped(nil, up: false) == nil)
    }
}
