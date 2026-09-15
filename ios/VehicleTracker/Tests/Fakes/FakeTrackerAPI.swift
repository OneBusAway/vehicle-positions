import Foundation
@testable import VehicleTracker

/// A server that answers from canned values and records every call. Each
/// endpoint's failure can be scripted by setting its error.
@MainActor final class FakeTrackerAPI: TrackerAPI {
    nonisolated(unsafe) var token = "jwt"
    nonisolated(unsafe) var vehicles: [Vehicle] = [Vehicle(id: "bus-1", label: "Bus 1")]
    nonisolated(unsafe) var routeList: [RouteInfo] = []
    nonisolated(unsafe) var page = RouteTripsPage(routeID: "R1", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: [])
    nonisolated(unsafe) var geometry = TripFixtures.t1
    nonisolated(unsafe) var startedID: Int64 = 42
    nonisolated(unsafe) var loginError: (any Error)?
    nonisolated(unsafe) var startError: (any Error)?
    nonisolated(unsafe) var endError: (any Error)?
    nonisolated(unsafe) var postError: (any Error)?
    /// Awaited, before recording the call, so tests can exercise a request
    /// that is still in flight.
    nonisolated(unsafe) var endDelay: Duration?

    private(set) nonisolated(unsafe) var logins: [(String, String)] = []
    private(set) nonisolated(unsafe) var tripRequests: [String] = []
    private(set) nonisolated(unsafe) var starts: [(String, String, String)] = []
    private(set) nonisolated(unsafe) var ends: [Int64] = []
    private(set) nonisolated(unsafe) var posted: [LocationReport] = []

    nonisolated init() {}

    func login(email: String, password: String) async throws -> String {
        logins.append((email, password))
        if let loginError { throw loginError }
        return token
    }
    func myVehicles() async throws -> [Vehicle] { vehicles }
    func routes() async throws -> [RouteInfo] { routeList }
    func trips(routeID: String) async throws -> RouteTripsPage { page }
    func trip(id: String) async throws -> TripGeometry {
        tripRequests.append(id)
        return geometry
    }
    func startTrip(vehicleID: String, routeID: String, gtfsTripID: String) async throws -> Int64 {
        starts.append((vehicleID, routeID, gtfsTripID))
        if let startError { throw startError }
        return startedID
    }
    func endTrip(id: Int64) async throws {
        if let endDelay { try? await Task.sleep(for: endDelay) }
        ends.append(id)
        if let endError { throw endError }
    }
    func postLocation(_ report: LocationReport) async throws {
        posted.append(report)
        if let postError { throw postError }
    }
}
