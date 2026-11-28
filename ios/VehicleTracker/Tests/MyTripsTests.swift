import Foundation
import Testing
@testable import VehicleTracker

/// My trips: the request, the list as it pages and refreshes, the days and the rows.
@MainActor @Suite(.serialized) struct MyTripsTests {
    static let zone = TimeZone(identifier: "America/Los_Angeles")!
    static let calendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        return calendar
    }()
    static let english = Locale(identifier: "en_US")

    /// 2026-11-25 13:00 in Los Angeles, a Wednesday.
    static let now = date(2026, 11, 25, 13, 0)

    static func date(_ year: Int, _ month: Int, _ day: Int, _ hour: Int, _ minute: Int) -> Date {
        calendar.date(from: DateComponents(year: year, month: month, day: day, hour: hour, minute: minute))!
    }

    static func trip(_ id: Int64, start: Date = now, ended: Bool = true, headsign: String = "Downtown Seattle",
                     route: MyTrip.Route? = MyTrip.Route(shortName: "7", longName: "Rainier Beach", color: "FFC72C", textColor: "000000"),
                     routeID: String = "100479") -> MyTrip {
        MyTrip(id: id, vehicleID: "4317", routeID: routeID, route: route, headsign: headsign,
               startTime: start, endTime: ended ? start.addingTimeInterval(49 * 60) : nil)
    }

    static func page(_ ids: ClosedRange<Int64>, hasMore: Bool, running: Int64? = nil) -> MyTripsPage {
        MyTripsPage(hasMore: hasMore, trips: ids.reversed().map { trip($0, ended: $0 != running) })
    }

    // MARK: The request

    @Test func theRequestAsksForAPageWithTheBearerToken_andReadsGosTimes() async throws {
        // As the server writes them: Go's time with its microseconds, a null
        // route, and no end_time while a trip runs.
        let body = #"""
        {"count":2,"has_more":true,"trips":[
          {"id":9,"vehicle_id":"4317","vehicle_label":"Bus 4317","route_id":"","route":null,"gtfs_trip_id":"T9","headsign":"","start_time":"2026-11-28T09:20:29.611579+05:30","status":"active"},
          {"id":8,"vehicle_id":"4401","vehicle_label":"","route_id":"100479","route":{"short_name":"7","long_name":"Rainier Beach","color":"FFC72C","text_color":"000000"},"gtfs_trip_id":"T8","headsign":"Downtown Seattle","start_time":"2026-11-28T03:50:01.123456Z","end_time":"2026-11-28T04:39:00Z","status":"completed"}
        ]}
        """#
        StubURLProtocol.reset { _ in (200, Data(body.utf8)) }
        let tokens = InMemoryTokenStore(StoredToken(token: "tok", issuedAt: Date()))
        let client = try TrackerClient(baseURL: URL(string: "https://positions.example.org")!, tokens: tokens,
                                       configuration: StubURLProtocol.configuration())

        let page = try await client.myTrips(limit: 50, offset: 100)

        let request = try #require(StubURLProtocol.last)
        #expect(request.method == "GET")
        #expect(request.url.path == "/api/v1/trips")
        let query = URLComponents(url: request.url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        #expect(Set(query) == [URLQueryItem(name: "limit", value: "50"), URLQueryItem(name: "offset", value: "100")])
        #expect(request.headers["Authorization"] == "Bearer tok")
        #expect(page.hasMore)
        #expect(page.trips.map(\.id) == [9, 8])
        #expect(page.trips[0].route == nil)
        #expect(page.trips[0].endTime == nil)
        #expect(abs(page.trips[0].startTime.timeIntervalSince1970 - 1_795_837_829.611579) < 0.001)
        #expect(page.trips[1].route == MyTrip.Route(shortName: "7", longName: "Rainier Beach", color: "FFC72C", textColor: "000000"))
        #expect(page.trips[1].endTime == Date(timeIntervalSince1970: 1_795_840_740))
    }

    // MARK: The list

    @Test func theFirstPageLeavesTheRunningTripOut_butCountsItForTheNext() throws {
        var list = MyTripsList()
        #expect(list.load == .loading)
        #expect(list.nextPage == nil)

        list.receivedFirst(Self.page(51...100, hasMore: true, running: 100))

        #expect(list.load == .loaded)
        #expect(list.trips.map(\.id) == Array((51...99).reversed()))
        let next = try #require(list.nextPage)
        #expect(next.offset == 50)
    }

    @Test func theNextPageGoesOnTheEnd_withATripAlreadyHereShownOnce() throws {
        var list = MyTripsList()
        list.receivedFirst(Self.page(51...100, hasMore: true))
        let next = try #require(list.nextPage)

        // A trip started since pushed trip 51 onto the second page.
        list.received(Self.page(2...51, hasMore: true), for: next)

        #expect(list.trips.map(\.id) == Array((2...100).reversed()))
        let third = try #require(list.nextPage)
        #expect(third.offset == 100, "where the server's rows go on, the repeat counted")

        list.received(Self.page(1...1, hasMore: false), for: third)
        #expect(list.trips.map(\.id) == Array((1...100).reversed()))
        #expect(list.nextPage == nil, "no more to load")
    }

    @Test func aPageAskedForBeforeARefreshIsDropped() throws {
        var list = MyTripsList()
        list.receivedFirst(Self.page(51...100, hasMore: true))
        let before = try #require(list.nextPage)

        list.receivedFirst(Self.page(52...101, hasMore: true))
        list.received(Self.page(1...50, hasMore: false), for: before)
        list.failed(before)

        #expect(list.trips.map(\.id) == Array((52...101).reversed()))
        #expect(!list.moreFailed)
        #expect(list.nextPage != before, "the same offset, asked for again after the refresh")
        #expect(list.nextPage?.offset == 50)
    }

    @Test func aFailedNextPageWaitsForTryAgain() throws {
        var list = MyTripsList()
        list.receivedFirst(Self.page(51...100, hasMore: true))
        let next = try #require(list.nextPage)

        list.failed(next)
        #expect(list.moreFailed)
        #expect(list.nextPage == nil, "the end of the list stops loading until Try again")

        list.retryMore()
        #expect(list.nextPage == next)
    }

    @Test func aFailedRefreshKeepsTheList_andAFailedFirstLoadTakesTheScreen() {
        var first = MyTripsList()
        first.firstFailed()
        #expect(first.load == .failed)
        first.reload()
        #expect(first.load == .loading)

        var list = MyTripsList()
        list.receivedFirst(Self.page(1...3, hasMore: false))
        list.firstFailed()
        #expect(list.load == .loaded)
        #expect(list.refreshFailed)
        #expect(list.trips.count == 3)

        list.receivedFirst(Self.page(1...4, hasMore: false))
        #expect(!list.refreshFailed)
        #expect(list.trips.count == 4)
    }

    // MARK: Days

    @Test func tripsGoUnderTodayYesterdayAndTheirDate_inThePhonesTimeZone() {
        let trips = [
            Self.trip(6, start: Self.date(2026, 11, 25, 7, 13)),
            Self.trip(5, start: Self.date(2026, 11, 25, 6, 5)),
            // 23:30 in Los Angeles is already the 25th in UTC.
            Self.trip(4, start: Self.date(2026, 11, 24, 23, 30)),
            Self.trip(3, start: Self.date(2026, 11, 23, 9, 20)),
            Self.trip(2, start: Self.date(2025, 12, 31, 8, 10)),
        ]

        let days = MyTripsDay.of(trips, now: Self.now, calendar: Self.calendar, locale: Self.english)

        #expect(days.map(\.title) == ["Today", "Yesterday", "Monday, November 23", "Wednesday, December 31, 2025"])
        #expect(days.map { $0.trips.map(\.id) } == [[6, 5], [4], [3], [2]])
    }

    // MARK: Rows

    @Test func aRowHasTheRoutesBadge_whereTheTripWent_andWhenAndOnWhichVehicle() {
        let row = MyTripRow(Self.trip(1, start: Self.date(2026, 11, 25, 7, 13)), calendar: Self.calendar, locale: Self.english)

        #expect(row.badge == "7")
        #expect(row.color == "FFC72C")
        #expect(row.textColor == "000000")
        #expect(row.title == "Downtown Seattle")
        // ICU puts a narrow no-break space before AM.
        #expect(row.detail.replacingOccurrences(of: "\u{202F}", with: " ") == "7:13 AM to 8:02 AM · Vehicle 4317")
        #expect(row.spoken.replacingOccurrences(of: "\u{202F}", with: " ") == "Route 7, Downtown Seattle, 7:13 AM to 8:02 AM · Vehicle 4317")
    }

    @Test func withNoHeadsignTheRoutesNameShows_andARouteTheScheduleLostShowsItsID() {
        let noHeadsign = MyTripRow(Self.trip(1, headsign: ""), calendar: Self.calendar, locale: Self.english)
        #expect(noHeadsign.title == "Rainier Beach")

        let lost = MyTripRow(Self.trip(2, headsign: "", route: nil), calendar: Self.calendar, locale: Self.english)
        #expect(lost.badge == "100479")
        #expect(lost.color.isEmpty, "the accent pair, as any badge with no colour")
        #expect(lost.title == nil)
        #expect(lost.spoken.hasPrefix("Route 100479, "))

        let noShortName = MyTrip.Route(shortName: "", longName: "Loop", color: "", textColor: "")
        #expect(MyTripRow(Self.trip(3, route: noShortName), calendar: Self.calendar, locale: Self.english).badge == "100479")

        let nothing = MyTripRow(Self.trip(4, headsign: "", route: nil, routeID: ""), calendar: Self.calendar, locale: Self.english)
        #expect(nothing.title == nil)
        #expect(!nothing.spoken.hasPrefix("Route"), "no route to read out")
    }
}
