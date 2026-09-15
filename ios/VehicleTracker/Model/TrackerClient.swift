import Foundation

/// The server over URLSession. HTTPS-only outside development hosts, never
/// follows a redirect (a token or a position must not be re-sent to a host the
/// request was not addressed to), and reads the bearer token on every request
/// so a re-login mid-trip takes effect at once.
nonisolated final class TrackerClient: TrackerAPI, Sendable {
    let baseURL: URL
    private let session: URLSession
    private let token: @Sendable () -> String?

    init(baseURL: URL, token: @escaping @Sendable () -> String?, configuration: URLSessionConfiguration = .ephemeral) throws {
        guard ServerURLPolicy.isAllowed(baseURL) else { throw APIError.insecureURL }
        self.baseURL = baseURL
        self.token = token
        session = URLSession(configuration: configuration, delegate: RedirectRefuser(), delegateQueue: nil)
    }

    func login(email: String, password: String) async throws -> String {
        let data = try await send("POST", "api/v1/auth/login", body: try encode(LoginRequest(email: email, password: password)), authorized: false)
        return try decode(LoginResponse.self, data).token
    }

    func myVehicles() async throws -> [Vehicle] {
        try decode([Vehicle].self, try await send("GET", "api/v1/vehicles"))
    }

    func routes() async throws -> [RouteInfo] {
        try decode(RoutesResponse.self, try await send("GET", "api/v1/gtfs/routes")).routes
    }

    func trips(routeID: String) async throws -> RouteTripsPage {
        try decode(RouteTripsPage.self, try await send("GET", "api/v1/gtfs/routes/\(routeID)/trips"))
    }

    func trip(id: String) async throws -> TripGeometry {
        try decode(TripGeometry.self, try await send("GET", "api/v1/gtfs/trips/\(id)"))
    }

    func startTrip(vehicleID: String, routeID: String, gtfsTripID: String) async throws -> Int64 {
        let body = try encode(StartTripRequest(vehicleID: vehicleID, routeID: routeID, gtfsTripID: gtfsTripID))
        return try decode(StartTripResponse.self, try await send("POST", "api/v1/trips/start", body: body)).id
    }

    func endTrip(id: Int64) async throws {
        _ = try await send("POST", "api/v1/trips/end", body: try encode(EndTripRequest(tripID: id)))
    }

    func postLocation(_ report: LocationReport) async throws {
        _ = try await send("POST", "api/v1/locations", body: try encode(report))
    }

    private func send(_ method: String, _ path: String, body: Data? = nil, authorized: Bool = true) async throws -> Data {
        var request = URLRequest(url: baseURL.appending(path: path))
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if authorized, let token = token() {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else {
            throw APIError.transport("not an HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONCoding.decoder.decode(ErrorBody.self, from: data))?.error ?? ""
            throw APIError.status(http.statusCode, message: message)
        }
        return data
    }

    private func encode(_ value: some Encodable) throws -> Data {
        do {
            return try JSONCoding.encoder.encode(value)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do {
            return try JSONCoding.decoder.decode(type, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }
}

/// Turns every redirect into the redirect response itself, which `send`
/// then reports as a non-2xx status.
nonisolated final class RedirectRefuser: NSObject, URLSessionTaskDelegate, Sendable {
    func urlSession(
        _ session: URLSession, task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
        completionHandler: @escaping @Sendable (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }
}
