import Testing
@testable import VehicleTracker

/// Android's `TripPanelModelTest`, plus the folding iOS adds.
@Suite struct StopRowsTests {
    let stops = TripFixtures.t1.stops
    var st1: TripStop { stops[0] }
    var st2: TripStop { stops[1] }
    var st3: TripStop { stops[2] }

    @Test func theVehicleSitsBetweenTheStopItPassedAndTheNextWithTheRestAfter() {
        #expect(StopRows.rows(stops: stops, nextStopIndex: 1, unfolded: false)
            == [.stop(st1, .passed), .vehicle, .stop(st2, .next), .stop(st3, .last)])
    }

    @Test func beforeTheFirstStopThereIsNoStopBehindTheVehicle() {
        #expect(StopRows.rows(stops: stops, nextStopIndex: 0, unfolded: false)
            == [.vehicle, .stop(st1, .next), .stop(st2, .upcoming), .stop(st3, .last)])
    }

    /// Android lists the last passed stop alone; iOS folds the one before it.
    @Test func headingForTheLastStopItIsTheNextStopAndNothingFollows() {
        #expect(StopRows.rows(stops: stops, nextStopIndex: 2, unfolded: false)
            == [.folded(count: 1), .stop(st2, .passed), .vehicle, .stop(st3, .next)])
        #expect(StopRows.rows(stops: stops, nextStopIndex: 2, unfolded: true)
            == [.stop(st1, .passed), .stop(st2, .passed), .vehicle, .stop(st3, .next)])
    }

    @Test func withNoFixTheWholeRunShowsFromItsFirstStopAndNoVehicle() {
        let whole: [StopRows.Row] = [.stop(st1, .next), .stop(st2, .upcoming), .stop(st3, .last)]
        #expect(StopRows.rows(stops: stops, nextStopIndex: nil, unfolded: false) == whole)
        // A stop the trip does not have is no fix to place the vehicle by.
        #expect(StopRows.rows(stops: stops, nextStopIndex: -1, unfolded: false) == whole)
        #expect(StopRows.rows(stops: stops, nextStopIndex: 3, unfolded: false) == whole)
        #expect(StopRows.rows(stops: [], nextStopIndex: nil, unfolded: false).isEmpty)
    }

    @Test func offTheRouteTheVehicleStillSitsBeforeTheStopItIsHeadingFor() throws {
        let evaluator = try #require(AdherenceEvaluator(trip: TripFixtures.t1))
        let offRoute = evaluator.evaluate(TripFixtures.fix(lat: 47.6045, lon: -122.3200, at: TripFixtures.at(8, 5)), previous: nil)
        try #require(offRoute.status == .offRoute)
        let next = try #require(stops.firstIndex { $0.id == offRoute.nextStop.id })

        let rows = StopRows.rows(stops: stops, nextStopIndex: next, unfolded: false)
        let vehicle = try #require(rows.firstIndex(of: .vehicle))
        #expect(rows[vehicle + 1] == .stop(offRoute.nextStop, .next))
    }

    /// A run with many stops behind the vehicle: all but the last one passed
    /// fold into a row, so the vehicle stays near the top of the list.
    @Test func thePassedStopsAboveTheLastOneFold() {
        let long = (1...8).map { n in
            TripStop(id: "S\(n)", name: "Stop \(n)", sequence: n, lat: 47.6, lon: -122.33, alongShapeM: Double(n) * 100,
                     arrivalAt: TripFixtures.at(8, n), departureAt: TripFixtures.at(8, n))
        }
        let folded = StopRows.rows(stops: long, nextStopIndex: 6, unfolded: false)
        #expect(folded == [
            .folded(count: 5), .stop(long[5], .passed), .vehicle, .stop(long[6], .next), .stop(long[7], .last),
        ])

        let unfolded = StopRows.rows(stops: long, nextStopIndex: 6, unfolded: true)
        #expect(unfolded == long[0..<5].map { .stop($0, .passed) } + [
            .stop(long[5], .passed), .vehicle, .stop(long[6], .next), .stop(long[7], .last),
        ])

        // Even one stop above the last passed folds, so the list always opens the same way.
        #expect(StopRows.rows(stops: long, nextStopIndex: 2, unfolded: false)
            == [.folded(count: 1), .stop(long[1], .passed), .vehicle, .stop(long[2], .next)] + long[3...].map { .stop($0, $0.sequence == 8 ? .last : .upcoming) })
        // The last passed stop alone does not fold.
        #expect(StopRows.rows(stops: long, nextStopIndex: 1, unfolded: false).first == .stop(long[0], .passed))
    }

    /// A loop calls at the same stop twice: by id alone the two rows would
    /// collide, and SwiftUI would draw one of them wrong.
    @Test func rowIdsTellTwoCallsAtOneStopApart() {
        let rows = StopRows.rows(stops: TripFixtures.loop.stops, nextStopIndex: nil, unfolded: false)
        let ids = rows.map(\.id)
        #expect(ids == ["A#1", "B#2"])
        #expect(Set(ids).count == ids.count)
        #expect(StopRows.Row.vehicle.id == "vehicle")
        #expect(StopRows.Row.folded(count: 3).id == "folded")
    }
}
