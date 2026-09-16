import Foundation

/// Everything the app asks the server. `TrackerClient` is the shipping
/// implementation; tests substitute a fake.
nonisolated protocol TrackerAPI: Sendable {
    func login(email: String, password: String) async throws -> String
    func myVehicles() async throws -> [Vehicle]
    func routes() async throws -> [RouteInfo]
    func trips(routeID: String) async throws -> RouteTripsPage
    func trip(id: String) async throws -> TripGeometry
    func startTrip(vehicleID: String, routeID: String, gtfsTripID: String) async throws -> Int64
    func endTrip(id: Int64) async throws
    func postLocation(_ report: LocationReport) async throws
}
