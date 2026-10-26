import Foundation

/// The server over URLSession. HTTPS-only outside development hosts, never
/// follows a redirect (a token or a position must not be re-sent to a host the
/// request was not addressed to), and reads the bearer token from `tokens` on
/// every request so a re-login mid-trip takes effect at once. Given a
/// `refresher`, it renews an access token about to expire before the request,
/// and one the server turned away with a 401 after it, sending the request
/// again, once.
nonisolated final class TrackerClient: TrackerAPI, Sendable {
    let baseURL: URL
    private let session: URLSession
    private let tokens: any TokenStoring
    private let refresher: TokenRefresher?

    init(baseURL: URL, tokens: any TokenStoring, refresher: TokenRefresher? = nil,
         configuration: URLSessionConfiguration = .ephemeral) throws {
        guard ServerURLPolicy.isAllowed(baseURL) else { throw APIError.insecureURL }
        self.baseURL = baseURL
        self.tokens = tokens
        self.refresher = refresher
        // A driver on a dead cell keeps moving; the default 60 s means a
        // stuck request outlives a dozen fixes. Copied so a configuration the
        // caller still holds (the tests' stub) is not mutated underneath it.
        let configuration = configuration.copy() as! URLSessionConfiguration
        configuration.timeoutIntervalForRequest = 10
        session = URLSession(configuration: configuration, delegate: RedirectRefuser(), delegateQueue: nil)
    }

    deinit {
        // A URLSession with a delegate retains it, and itself, until it is
        // invalidated: a client dropped on sign-out would otherwise leak.
        session.finishTasksAndInvalidate()
    }

    func login(email: String, password: String) async throws -> AuthTokens {
        let data = try await send("POST", "api/v1/auth/login", body: try encode(LoginRequest(email: email, password: password)), authorized: false)
        return try decode(AuthTokens.self, data)
    }

    func logout() async throws {
        _ = try await send("POST", "api/v1/auth/logout")
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
        // Sign-in and refresh go without a token, so neither can set off a
        // refresh: a wrong password must not quietly renew the session of
        // whoever signed in on this phone before.
        guard authorized, let stored = try? tokens.load() else {
            return try await perform(request)
        }
        var token = stored.token
        if let refresher, refresher.isRefreshDue(stored) {
            token = await refresher.beforeExpiry(observedToken: token, refresh: { [self] in await refresh($0) })
        }
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        do {
            return try await perform(request)
        } catch let error as APIError {
            guard case .status(401, _) = error, let refresher else { throw error }
            switch await refresher.afterUnauthorized(failedToken: token, refresh: { [self] in await refresh($0) }) {
            case .retry(let renewed):
                request.setValue("Bearer \(renewed)", forHTTPHeaderField: "Authorization")
                // A 401 on the retry is the caller's: the token is new, so
                // another refresh would not help.
                return try await perform(request)
            case .refused:
                throw error
            case .unreachable(let message):
                throw APIError.transport(message)
            }
        }
    }

    private func perform(_ request: URLRequest) async throws -> Data {
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

    /// `POST /api/v1/auth/refresh`, for the refresher. Only a 401 says the
    /// server refused the token. Anything else (no answer, a 429 or 5xx, a
    /// reply that is not a token pair) says nothing about it, so the token is
    /// kept for the next request.
    private func refresh(_ refreshToken: String) async -> TokenRefresher.RefreshReply {
        do {
            let body = try encode(RefreshRequest(refreshToken: refreshToken))
            return .issued(try decode(AuthTokens.self, try await send("POST", "api/v1/auth/refresh", body: body, authorized: false)))
        } catch APIError.status(401, _) {
            return .refused
        } catch APIError.status(let code, _) {
            return .unreachable("refresh failed with HTTP \(code)")
        } catch APIError.transport(let message) {
            return .unreachable(message)
        } catch {
            // A 200 that is not a token pair, such as a captive portal's page.
            return .unreachable(error.localizedDescription)
        }
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
