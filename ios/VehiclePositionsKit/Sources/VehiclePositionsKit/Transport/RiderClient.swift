import Foundation

/// The rider API, spelled out as five calls (spec §4.3–4.4). Every failure —
/// transport, HTTP status, or malformed body — surfaces as a ``RideError`` so
/// callers never have to reason about two error domains at once.
///
/// Internal: ``RideReporter`` is the SDK's public surface (spec §4.12), and a
/// host that wants the raw calls should be given a reason to want them first.
struct RiderClient: Sendable {
    private let serverURL: URL
    private let transport: any RideTransport

    init(serverURL: URL, transport: any RideTransport) {
        self.serverURL = serverURL
        self.transport = transport
    }

    func register(installationID: String, appID: String, appVersion: String) async throws -> RegisterResponse {
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

    func startRide(token: String, trip: TripDescriptor) async throws -> StartRideResponse {
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

    func uploadPositions(
        token: String,
        rideID: String,
        positions: [PositionUpload]
    ) async throws -> PositionsResponse {
        try await perform(
            RiderRequest(
                method: "POST",
                path: "api/v1/rider/rides/\(Self.pathSegment(rideID))/positions",
                body: try encode(PositionsRequest(positions: positions)),
                bearerToken: token
            ),
            decoding: PositionsResponse.self
        )
    }

    func endRide(token: String, rideID: String, reason: RideEndReason) async throws -> EndRideResponse {
        // Server-only reasons (spec §4.4) are the server's verdict to reach, not
        // ours to claim. Every caller is inside this module and already filters
        // on `isClientReportable`, so this is a debug check, not a live guard.
        assert(reason.isClientReportable, "\(reason.rawValue) is not a reason a client may report")
        return try await perform(
            RiderRequest(
                method: "POST",
                path: "api/v1/rider/rides/\(Self.pathSegment(rideID))/end",
                body: try encode(EndRideRequest(reason: reason)),
                bearerToken: token
            ),
            decoding: EndRideResponse.self
        )
    }

    func tripStatus(token: String, tripID: String, startDate: String?) async throws -> TripStatus {
        var query: [String: String] = [:]
        if let startDate { query["start_date"] = startDate }
        return try await perform(
            RiderRequest(
                method: "GET",
                path: "api/v1/rider/trips/\(Self.pathSegment(tripID))/status",
                query: query,
                bearerToken: token
            ),
            decoding: TripStatus.self
        )
    }

    /// The `platform` every registration reports.
    private static let platform = "ios"

    /// Escapes one path component. GTFS trip ids carry spaces and slashes in
    /// real feeds ("1_604321", but also "Route 5/Northbound"), and an unescaped
    /// slash would silently address a different endpoint.
    private static func pathSegment(_ value: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove("/")
        return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
    }

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
        } catch is CancellationError {
            // The ride was torn down. That is not a network failure, and callers
            // structure their cancellation handling around CancellationError.
            throw CancellationError()
        } catch let error as URLError where error.code == .cancelled {
            throw CancellationError()
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
