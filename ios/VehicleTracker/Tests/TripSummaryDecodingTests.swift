import Foundation
import Testing
@testable import VehicleTracker

/// The run list's reply with each run's number and block (#232), and the same
/// reply from a server older than that, which has neither.
@Suite struct TripSummaryDecodingTests {
    func page(_ extraFields: String) throws -> RouteTripsPage {
        let json = #"{"route_id":"100479","service_date":"20261026","timezone":"America/Los_Angeles","trips":[{"id":"T1","headsign":"Angle Lake","direction_id":0,"starts_at":"2026-10-26T07:00:00-07:00","ends_at":"2026-10-26T07:40:00-07:00","first_stop":"Lynnwood City Center","last_stop":"Angle Lake"\#(extraFields)}]}"#
        return try JSONCoding.decoder.decode(RouteTripsPage.self, from: Data(json.utf8))
    }

    @Test func aRunDecodesItsNumberAndBlock() throws {
        let trip = try #require(try page(#","short_name":"1503","block_id":"599-20""#).trips.first)
        #expect(trip.shortName == "1503")
        #expect(trip.blockID == "599-20")
        #expect(trip.headsign == "Angle Lake")
        #expect(trip.directionID == 0)
    }

    @Test func aServerWithoutTheFieldsStillDecodesWithBothEmpty() throws {
        let trip = try #require(try page("").trips.first)
        #expect(trip.shortName == "")
        #expect(trip.blockID == "")
        #expect(trip.id == "T1")
        #expect(trip.lastStop == "Angle Lake")
    }
}
