import Foundation
import Testing
@testable import VehicleTracker

/// A port of Android's `ResumeCard.of` and `StartedAgo` cases. Times are
/// formatted in en_US, in the trip's zone, whatever the machine's are.
@Suite struct ResumeCardTests {
    let us = Locale(identifier: "en_US")
    /// The narrow space en_US puts before AM and PM.
    let space = "\u{202F}"

    func active(_ trip: TripGeometry = TripFixtures.t1, startedAt: Date = TripFixtures.at(8, 0)) -> ActiveTrip {
        ActiveTrip(serverTripID: 7, vehicle: Vehicle(id: "4317", label: "Bus 4317"), trip: trip,
                   startedAt: startedAt, driverEmail: "d@test.com")
    }

    func started(after seconds: TimeInterval) -> String {
        ResumeCard(active(), locale: us).started(now: TripFixtures.at(8, 0).addingTimeInterval(seconds))
    }

    @Test func theStartIsInWholeMinutesRoundedDown() {
        #expect(started(after: 0) == "Started 0 min ago, at 8:00\(space)AM")
        #expect(started(after: 59) == "Started 0 min ago, at 8:00\(space)AM")
        #expect(started(after: 60) == "Started 1 min ago, at 8:00\(space)AM")
        #expect(started(after: 59 * 60 + 59) == "Started 59 min ago, at 8:00\(space)AM")
    }

    @Test func fromAnHourItCountsHoursAndAlwaysTheMinutes() {
        #expect(started(after: 60 * 60) == "Started 1 h 0 min ago, at 8:00\(space)AM")
        #expect(started(after: 125 * 60) == "Started 2 h 5 min ago, at 8:00\(space)AM")
    }

    @Test func aPhoneClockBehindTheStartReadsNoTimeYet() {
        #expect(started(after: -30) == "Started 0 min ago, at 8:00\(space)AM")
    }

    /// Kiritimati is UTC+14: a time formatted in the phone's zone would be a
    /// different hour on any machine west of it, CI's UTC included.
    @Test func timesAreTheTripsZone() {
        var trip = TripFixtures.t1
        trip.timezone = "Pacific/Kiritimati"
        let start = Date(timeIntervalSince1970: 1_790_000_000)
        trip.stops[0].departureAt = start
        let card = ResumeCard(active(trip, startedAt: start), locale: us)
        #expect(card.startTime == "4:13\(space)AM")
        #expect(card.runTime == "4:13\(space)AM")
    }

    @Test func theRouteIsItsShortNameOrElseItsID() {
        var trip = TripFixtures.t1
        trip.routeID = "100263"
        trip.route.shortName = ""
        let card = ResumeCard(active(trip), locale: us)
        #expect(card.badgeName == "100263")
        #expect(card.title == "North")
    }

    @Test func aTripWithNoHeadsignIsNamedByItsRoute() {
        var trip = TripFixtures.t1
        trip.routeID = "100263"
        trip.headsign = ""
        #expect(ResumeCard(active(trip), locale: us).title == "Route 1")
        trip.route.shortName = ""
        #expect(ResumeCard(active(trip), locale: us).title == "Route 100263")
    }

    @Test func theRunIsItsFirstDepartureWhenItHasStops() {
        #expect(ResumeCard(active(), locale: us).runTime == "8:00\(space)AM")
        var trip = TripFixtures.t1
        trip.stops = []
        #expect(ResumeCard(active(trip), locale: us).runTime == nil)
    }

    @Test func theVehicleIsItsFleetNumber() {
        #expect(ResumeCard(active(), locale: us).vehicleID == "4317")
    }

    @Test func voiceOverReadsTheRouteAndTheStartInWords() {
        let card = ResumeCard(active(), locale: us)
        #expect(card.spokenRoute == "Route 1 to North")
        #expect(card.spokenStarted(now: TripFixtures.at(8, 19)) == "started 19 minutes ago, at 8:00\(space)AM")
        #expect(card.spokenStarted(now: TripFixtures.at(8, 1)) == "started 1 minute ago, at 8:00\(space)AM")
        #expect(card.spokenStarted(now: TripFixtures.at(10, 5)) == "started 2 hours 5 minutes ago, at 8:00\(space)AM")
        #expect(card.spokenStarted(now: TripFixtures.at(9, 1)) == "started 1 hour 1 minute ago, at 8:00\(space)AM")
        var trip = TripFixtures.t1
        trip.headsign = ""
        #expect(ResumeCard(active(trip), locale: us).spokenRoute == "Route 1")
    }
}
