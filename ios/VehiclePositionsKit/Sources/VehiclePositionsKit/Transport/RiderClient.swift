import Foundation

/// The rider API, spelled out as five calls (spec §4.3–4.4). Every failure —
/// transport, HTTP status, or malformed body — surfaces as a ``RideError`` so
/// callers never have to reason about two error domains at once.
public struct RiderClient: Sendable {
    private let serverURL: URL
    private let transport: any RideTransport

    public init(serverURL: URL, transport: any RideTransport) {
        self.serverURL = serverURL
        self.transport = transport
    }

    public func register(installationID: String, appID: String, appVersion: String) async throws -> RegisterResponse {
        let body = RegisterRequest(
            installationID: installationID,
            platform: Self.platform,
            appID: appID,
            appVersion: appVersion
        )
        return try await perform(
            RiderRequest(method: "POST", path: "api/v1/rider/register", body: try encode(body)),
            decoding: RegisterResponse.self
        )
    }

    public func startRide(token: String, trip: TripDescriptor) async throws -> StartRideResponse {
        try await perform(
            RiderRequest(
                method: "POST",
                path: "api/v1/rider/rides",
                body: try encode(StartRideRequest(trip)),
                bearerToken: token
            ),
            decoding: StartRideResponse.self
        )
    }

    public func uploadPositions(
        token: String,
        rideID: String,
        positions: [PositionUpload]
    ) async throws -> PositionsResponse {
        try await perform(
            RiderRequest(
                method: "POST",
                path: "api/v1/rider/rides/\(rideID)/positions",
                body: try encode(PositionsRequest(positions: positions)),
                bearerToken: token
            ),
            decoding: PositionsResponse.self
        )
    }

    public func endRide(token: String, rideID: String, reason: RideEndReason) async throws -> EndRideResponse {
        try await perform(
            RiderRequest(
                method: "POST",
                path: "api/v1/rider/rides/\(rideID)/end",
                body: try encode(EndRideRequest(reason: reason)),
                bearerToken: token
            ),
            decoding: EndRideResponse.self
        )
    }

    public func tripStatus(token: String, tripID: String, startDate: String?) async throws -> TripStatus {
        var query: [String: String] = [:]
        if let startDate { query["start_date"] = startDate }
        return try await perform(
            RiderRequest(
                method: "GET",
                path: "api/v1/rider/trips/\(tripID)/status",
                query: query,
                bearerToken: token
            ),
            decoding: TripStatus.self
        )
    }

    /// The `platform` every registration reports.
    private static let platform = "ios"

    private func encode(_ value: some Encodable) throws -> Data {
        do {
            return try RiderAPICodec.encode(value)
        } catch {
            throw RideError.decoding(String(describing: error))
        }
    }

    private func perform<T: Decodable>(_ request: RiderRequest, decoding: T.Type) async throws -> T {
        let response = try await send(request)
        do {
            return try RiderAPICodec.decode(T.self, from: response.body)
        } catch {
            throw RideError.decoding(String(describing: error))
        }
    }

    private func send(_ request: RiderRequest) async throws -> RiderResponse {
        let response: RiderResponse
        do {
            response = try await transport.send(request, baseURL: serverURL)
        } catch {
            throw RideError.transport(String(describing: error))
        }

        switch response.status {
        case 200...299:
            return response
        case 401:
            throw RideError.notAuthorized
        case 409:
            throw RideError.alreadyEnded
        default:
            let message = (try? RiderAPICodec.decode(ServerErrorBody.self, from: response.body))?.error ?? ""
            throw RideError.server(status: response.status, message: message)
        }
    }
}
