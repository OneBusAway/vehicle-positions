import Testing
@testable import VehicleTracker

/// A port of Android's `RunLabelTest`: the line under a run on the board.
@Suite struct RunLabelTests {
    @Test func aRunWithANumberAndABlockShowsBoth() {
        #expect(RunLabel(number: "1503", block: "599-20").text == "Run 1503 · Block 599-20")
    }

    @Test func aRunWithOnlyANumberShowsTheNumber() {
        #expect(RunLabel(number: "1503", block: "").text == "Run 1503")
    }

    @Test func aRunWithOnlyABlockShowsTheBlock() {
        #expect(RunLabel(number: "", block: "599-20").text == "Block 599-20")
    }

    @Test func aRunWithNeitherGetsNoLine() {
        #expect(RunLabel(number: "", block: "").text == nil)
    }
}
