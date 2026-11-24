import Foundation
@testable import VehicleTracker

/// A server that answers from canned values and records every call. Each
/// endpoint's failure can be scripted by setting its error.
@MainActor final class FakeTrackerAPI: TrackerAPI {
    nonisolated(unsafe) var tokens = AuthTokens(accessToken: "jwt", refreshToken: "refresh-1", expiresIn: 900)
    nonisolated(unsafe) var vehicles: [Vehicle] = [Vehicle(id: "bus-1", label: "Bus 1")]
    nonisolated(unsafe) var routeList: [RouteInfo] = []
    nonisolated(unsafe) var page = RouteTripsPage(routeID: "R1", serviceDate: "20260902", timezone: "America/Los_Angeles", trips: [])
    nonisolated(unsafe) var geometry = TripFixtures.t1
    nonisolated(unsafe) var startedID: Int64 = 42
    nonisolated(unsafe) var loginError: (any Error)?
    nonisolated(unsafe) var vehiclesError: (any Error)?
    nonisolated(unsafe) var startError: (any Error)?
    nonisolated(unsafe) var endError: (any Error)?
    nonisolated(unsafe) var postError: (any Error)?
    /// Awaited, before recording the call, so tests can exercise a request
    /// that is still in flight.
    nonisolated(unsafe) var endDelay: Duration?
    /// Awaited inside `endTrip(id:)`, before recording the call, so a test
    /// can act while an end is out, or hold it until it is cancelled: what it
    /// throws then is the end's error, as a cancelled request's is.
    nonisolated(unsafe) var whileEnding: (() async throws -> Void)?
    nonisolated(unsafe) var logoutError: (any Error)?
    /// Awaited inside `logout()`, so a test can look at the session, or act
    /// on it, while the request is out. What it throws is the logout's error.
    nonisolated(unsafe) var whileLoggingOut: (() async throws -> Void)?
    nonisolated(unsafe) var postDelay: Duration?
    nonisolated(unsafe) var historyError: (any Error)?
    /// Awaited inside `postLocationHistory(_:)`, before recording the call, so
    /// a test can act while an upload is out. What it throws is the upload's
    /// error.
    nonisolated(unsafe) var whileUploadingHistory: (() async throws -> Void)?

    private(set) nonisolated(unsafe) var logins: [(String, String)] = []
    private(set) nonisolated(unsafe) var tripRequests: [String] = []
    private(set) nonisolated(unsafe) var starts: [(String, String, String)] = []
    private(set) nonisolated(unsafe) var ends: [Int64] = []
    private(set) nonisolated(unsafe) var posted: [LocationReport] = []
    private(set) nonisolated(unsafe) var historyUploads: [LocationHistoryRequest] = []
    /// The trip, start, end and logout calls in the order they were made, for
    /// tests where the order is the point.
    private(set) nonisolated(unsafe) var calls: [String] = []
    private(set) nonisolated(unsafe) var logouts = 0

    nonisolated init() {}

    func login(email: String, password: String) async throws -> AuthTokens {
        logins.append((email, password))
        if let loginError { throw loginError }
        return tokens
    }
    func myVehicles() async throws -> [Vehicle] {
        if let vehiclesError { throw vehiclesError }
        return vehicles
    }
    func routes() async throws -> [RouteInfo] { routeList }
    func trips(routeID: String) async throws -> RouteTripsPage { page }
    func trip(id: String) async throws -> TripGeometry {
        tripRequests.append(id)
        calls.append("trip \(id)")
        return geometry
    }
    func startTrip(vehicleID: String, routeID: String, gtfsTripID: String) async throws -> Int64 {
        starts.append((vehicleID, routeID, gtfsTripID))
        calls.append("start \(gtfsTripID)")
        if let startError { throw startError }
        return startedID
    }
    func endTrip(id: Int64) async throws {
        if let endDelay { try? await Task.sleep(for: endDelay) }
        try await whileEnding?()
        ends.append(id)
        calls.append("end \(id)")
        if let endError { throw endError }
    }
    func postLocation(_ report: LocationReport) async throws {
        if let postDelay { try? await Task.sleep(for: postDelay) }
        posted.append(report)
        if let postError { throw postError }
    }
    func postLocationHistory(_ request: LocationHistoryRequest) async throws {
        try await whileUploadingHistory?()
        historyUploads.append(request)
        if let historyError { throw historyError }
    }
    func logout() async throws {
        try await whileLoggingOut?()
        logouts += 1
        calls.append("logout")
        if let logoutError { throw logoutError }
    }
}
