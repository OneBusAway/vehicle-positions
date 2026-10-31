import Foundation
import Testing
@testable import VehicleTracker

/// A port of Android's `DepartureBoardTest` and `RunDirectionsTest`, plus the
/// boundaries at now, at a run's end, before the first run and after the last.
@Suite struct RunBoardsTests {
    /// A ten-minute run, so no two of these overlap.
    func run(_ id: String, _ hour: Int, _ minute: Int, direction: Int? = nil, headsign: String = "Downtown",
             first: String = "A", last: String = "B", minutes: Double = 10) -> TripSummary {
        let start = TripFixtures.at(hour, minute)
        return TripSummary(id: id, headsign: headsign, directionID: direction, startsAt: start,
                           endsAt: start.addingTimeInterval(minutes * 60), firstStop: first, lastStop: last)
    }

    func boards(at now: Date, _ runs: [TripSummary]) -> RunBoards {
        RunBoards.of(RouteTripsPage(routeID: "R7", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: runs), now: now)
    }

    func board(at now: Date, _ runs: [TripSummary]) -> Board {
        let all = boards(at: now, runs)
        #expect(all.boards.count == 1)
        return all.boards[0]
    }

    var day: [TripSummary] { [run("a", 6, 28), run("b", 6, 43), run("c", 6, 58), run("d", 7, 13)] }

    @Test func theBoardPutsTheLineAtNowAndOpensOnTheRunJustGone() {
        let board = board(at: TripFixtures.at(7, 0), day)
        #expect(board.direction == nil, "a route without directions has one board and no switch")
        #expect(board.runs.map(\.id) == ["a", "b", "c", "d"])
        #expect(board.runs.map(\.startsAt) == [TripFixtures.at(6, 28), TripFixtures.at(6, 43), TripFixtures.at(6, 58), TripFixtures.at(7, 13)])
        #expect(board.runs.map(\.past) == [true, true, false, false])
        #expect(board.runs.map(\.highlight) == [nil, nil, .now, nil])
        #expect(board.nowAt == 2)
        #expect(board.openAt == "b")
    }

    @Test func beforeTheFirstRunThereIsNoLineAndTheBoardOpensAtTheTop() {
        let board = board(at: TripFixtures.at(6, 0), day)
        #expect(board.runs.map(\.highlight) == [.next, nil, nil, nil])
        #expect(board.runs.map(\.past) == [false, false, false, false])
        #expect(board.nowAt == nil)
        #expect(board.openAt == "a")
    }

    @Test func onceEveryRunHasGoneTheLineComesAfterTheLast() {
        let board = board(at: TripFixtures.at(9, 0), day)
        #expect(board.runs.map(\.past) == [true, true, true, true])
        #expect(board.runs.map(\.highlight) == [nil, nil, nil, nil])
        #expect(board.nowAt == 4)
        #expect(board.openAt == "d")
    }

    @Test func anEmptyDayHasNoLineAndNowhereToOpen() {
        let board = board(at: TripFixtures.at(9, 0), [])
        #expect(board.runs.isEmpty)
        #expect(board.nowAt == nil)
        #expect(board.openAt == nil)
    }

    @Test func eachDirectionHasItsOwnBoardWithItsOwnNowOrNext() {
        let all = boards(at: TripFixtures.at(7, 0), [
            run("in1", 6, 28, direction: 0),
            run("out1", 6, 35, direction: 1, headsign: "Mount Baker"),
            run("in2", 6, 58, direction: 0),
            run("out2", 7, 20, direction: 1, headsign: "Mount Baker"),
        ])
        #expect(all.boards.map { $0.direction?.headsign } == ["Downtown", "Mount Baker"])
        #expect(all.boards.map { $0.runs.map(\.id) } == [["in1", "in2"], ["out1", "out2"]])
        #expect(all.boards[0].runs.map(\.highlight) == [nil, .now])
        #expect(all.boards[1].runs.map(\.highlight) == [nil, .next])
        #expect(all.boards.map(\.nowAt) == [1, 1])
        #expect(all.boards.map(\.openAt) == ["in1", "out1"])
        // The route's run under way is inbound, so the screen opens on that board.
        #expect(all.start == 0)
    }

    @Test func twoDirectionsAreLabelledByTheHeadsignMostOfTheirRunsShow() {
        // Every run starts at 14:00, so the first listed, "a", is the one
        // highlighted: the board opens on its direction, outbound.
        let all = boards(at: TripFixtures.at(14, 0), [
            run("a", 14, 0, direction: 1, headsign: "Mount Baker", first: "Pine St", last: "Mount Baker TC"),
            run("b", 14, 0, direction: 0, headsign: "Downtown Seattle Via Rainier Ave S"),
            run("c", 14, 0, direction: 0, headsign: "Downtown Seattle", first: "Mount Baker TC", last: "Pine St"),
            run("d", 14, 0, direction: 0, headsign: "Downtown Seattle", first: "Mount Baker TC", last: "Pine St"),
        ])
        #expect(all.boards.map(\.direction) == [
            RunDirection(id: 0, headsign: "Downtown Seattle", firstStop: "Mount Baker TC", lastStop: "Pine St"),
            RunDirection(id: 1, headsign: "Mount Baker", firstStop: "Pine St", lastStop: "Mount Baker TC"),
        ])
        #expect(all.start == 1)
    }

    @Test func aTieGoesToTheHeadsignThatComesFirst() {
        let all = boards(at: TripFixtures.at(6, 0), [
            run("a", 7, 0, direction: 0, headsign: "Downtown"),
            run("b", 7, 10, direction: 0, headsign: "Pine St"),
            run("c", 7, 20, direction: 1, headsign: "Mount Baker"),
        ])
        #expect(all.boards[0].direction?.headsign == "Downtown")
    }

    @Test func onceTheDaysRunsAreOverTheBoardOpensOnTheFirstDirection() {
        let all = boards(at: TripFixtures.at(9, 0), [run("a", 7, 0, direction: 1, headsign: "Mount Baker"), run("b", 7, 10, direction: 0)])
        #expect(all.boards.count == 2)
        #expect(all.start == 0)
    }

    @Test(arguments: [
        ("one direction", [0, 0]),
        ("no direction ids", [nil, nil]),
        ("only some ids", [0, 1, nil]),
        ("three directions", [0, 1, 2]),
    ] as [(String, [Int?])])
    func runsThatDoNotSplitIntoTwoDirectionsKeepOneBoard(name: String, directions: [Int?]) {
        let runs = directions.enumerated().map { index, direction in run("r\(index)", 7, index * 10, direction: direction) }
        let all = boards(at: TripFixtures.at(6, 0), runs)
        #expect(all.boards.count == 1, Comment(rawValue: name))
        #expect(all.boards[0].direction == nil, Comment(rawValue: name))
        #expect(all.boards[0].runs.count == runs.count, Comment(rawValue: name))
    }

    @Test func aRunStartingExactlyNowIsNow() {
        #expect(board(at: TripFixtures.at(8, 0), [run("a", 8, 0)]).runs[0].highlight == .now)
        #expect(board(at: TripFixtures.at(7, 59, 59), [run("a", 8, 0)]).runs[0].highlight == .next)
    }

    @Test func aRunEndingExactlyNowStillCountsAndIsGoneASecondLater() {
        let runs = [run("a", 8, 0)]
        let ending = board(at: TripFixtures.at(8, 10), runs)
        #expect(ending.runs[0].highlight == .now)
        #expect(ending.runs[0].past == false)
        #expect(ending.nowAt == nil)
        let over = board(at: TripFixtures.at(8, 10, 1), runs)
        #expect(over.runs[0].highlight == nil)
        #expect(over.runs[0].past == true)
        #expect(over.nowAt == 1)
    }

    @Test func aTieBetweenTheRunUnderWayAndTheNextGoesToTheNext() {
        // a started 30 minutes ago and b starts in 30: a is already under
        // way, so the driver is about to drive b.
        let runs = [run("a", 8, 0, minutes: 30), run("b", 9, 0, minutes: 30)]
        let board = board(at: TripFixtures.at(8, 30), runs)
        #expect(board.runs.map(\.highlight) == [nil, .next])
        #expect(board.runs.map(\.past) == [true, false])
        #expect(board.nowAt == 1)
        #expect(board.openAt == "a")
    }
}
