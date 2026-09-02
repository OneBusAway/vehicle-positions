import Foundation

/// One rider-API call, described without reference to any HTTP library so tests
/// can inspect it and fakes can answer it.
public struct RiderRequest: Sendable, Equatable {
    public var method: String
    /// Path relative to the server URL, without a leading slash, and already
    /// percent-encoded: a transport must not escape it a second time.
    public var path: String
    public var query: [String: String]
    public var body: Data?
    public var bearerToken: String?

    public init(
        method: String,
        path: String,
        query: [String: String] = [:],
        body: Data? = nil,
        bearerToken: String? = nil
    ) {
        self.method = method
        self.path = path
        self.query = query
        self.body = body
        self.bearerToken = bearerToken
    }
}

/// What came back: the HTTP status and the raw body, decoded by the caller.
public struct RiderResponse: Sendable, Equatable {
    public var status: Int
    public var body: Data

    public init(status: Int, body: Data) {
        self.status = status
        self.body = body
    }
}

/// The network seam. Only ``URLSessionRideTransport`` talks to a real server.
public protocol RideTransport: Sendable {
    func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse
}

/// `URLSession`-backed transport. Every non-HTTP response is an error: a rider
/// API call that did not reach a server has nothing useful to say.
public struct URLSessionRideTransport: RideTransport {
    private let session: URLSession

    public init(session: URLSession = URLSessionRideTransport.makeSession()) {
        self.session = session
    }

    /// An ephemeral session that fails fast rather than queueing behind a dead
    /// network: a stale position is worth less than a prompt failure.
    public static func makeSession() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 10
        configuration.waitsForConnectivity = false
        return URLSession(configuration: configuration)
    }

    public func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse {
        guard let url = Self.url(for: request, baseURL: baseURL) else { throw URLError(.badURL) }

        var urlRequest = URLRequest(url: url)
        urlRequest.httpMethod = request.method
        urlRequest.httpBody = request.body
        if request.body != nil {
            urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let token = request.bearerToken {
            urlRequest.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        urlRequest.setValue("application/json", forHTTPHeaderField: "Accept")

        let (data, response) = try await session.data(for: urlRequest)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        return RiderResponse(status: http.statusCode, body: data)
    }

    /// Joins a request onto the server URL. The path is set through
    /// `percentEncodedPath` rather than `URL.appending(path:)`, which would
    /// escape the escapes an already-encoded id carries ("%2F" → "%252F").
    static func url(for request: RiderRequest, baseURL: URL) -> URL? {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else { return nil }
        var prefix = components.percentEncodedPath
        if prefix.hasSuffix("/") { prefix.removeLast() }
        components.percentEncodedPath = prefix + "/" + request.path
        if !request.query.isEmpty {
            components.queryItems = request.query.sorted { $0.key < $1.key }.map(URLQueryItem.init)
        }
        return components.url
    }
}
