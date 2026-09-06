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
///
/// Every request carries a bearer token, a stable installation id, or a precise
/// location, so the transport refuses to send one over plain HTTP and refuses
/// to follow a redirect that would take one off the server it was addressed to.
public struct URLSessionRideTransport: RideTransport {
    private let session: URLSession
    private let allowsInsecureHTTP: Bool

    public init(session: URLSession = URLSessionRideTransport.makeSession()) {
        self.init(session: session, allowsInsecureHTTP: false)
    }

    private init(session: URLSession, allowsInsecureHTTP: Bool) {
        self.session = session
        self.allowsInsecureHTTP = allowsInsecureHTTP
    }

    /// A transport that will also talk to `http://` servers. Only for a local
    /// development server: it sends credentials and precise positions in the
    /// clear, so nothing shipped to a rider may use it.
    public static func insecureForDevelopment(
        session: URLSession = URLSessionRideTransport.makeSession()
    ) -> URLSessionRideTransport {
        URLSessionRideTransport(session: session, allowsInsecureHTTP: true)
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
        // Checked before the request is built, so a misconfigured server URL
        // cannot put a token or a rider's position on the wire even once.
        guard allowsInsecureHTTP || Self.isSecure(url) else {
            throw URLError(.appTransportSecurityRequiresSecureConnection)
        }

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

        let (data, response) = try await session.data(
            for: urlRequest,
            delegate: RedirectGuard(origin: url, allowsInsecureHTTP: allowsInsecureHTTP)
        )
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        return RiderResponse(status: http.statusCode, body: data)
    }

    static func isSecure(_ url: URL) -> Bool {
        url.scheme?.lowercased() == "https"
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

/// Decides which redirects a credentialed request may follow. `URLSession`
/// would otherwise replay the `Authorization` header — and the body, which
/// holds the rider's positions — at whatever a `Location` header names.
///
/// Only a redirect that stays on the same origin is followed. Anything else is
/// refused rather than stripped of its token: the rider API has no cross-origin
/// redirects, so one appearing means the server is not the one addressed, and
/// an anonymous retry there is no more wanted than a credentialed one.
final class RedirectGuard: NSObject, URLSessionTaskDelegate, Sendable {
    private let scheme: String?
    private let host: String?
    private let port: Int?
    private let allowsInsecureHTTP: Bool

    init(origin: URL, allowsInsecureHTTP: Bool) {
        self.scheme = origin.scheme?.lowercased()
        self.host = origin.host?.lowercased()
        self.port = origin.port
        self.allowsInsecureHTTP = allowsInsecureHTTP
        super.init()
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest
    ) async -> URLRequest? {
        mayFollow(request.url) ? request : nil
    }

    /// The whole of the policy, separated from the delegate callback so it can
    /// be stated as a table in the tests rather than driven through a session.
    func mayFollow(_ url: URL?) -> Bool {
        guard let url else { return false }
        guard allowsInsecureHTTP || URLSessionRideTransport.isSecure(url) else { return false }
        return url.scheme?.lowercased() == scheme
            && url.host?.lowercased() == host
            && url.port == port
    }
}
