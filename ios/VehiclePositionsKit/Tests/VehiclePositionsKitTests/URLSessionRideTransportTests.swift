import Foundation
import Testing
@testable import VehiclePositionsKit

/// Records every request the session actually starts, and answers it, so a
/// test can tell "refused before sending" apart from "sent and failed".
final class RecordingURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) private static let lock = NSLock()
    nonisolated(unsafe) private static var seen: [URL] = []

    static func reset() { lock.withLock { seen = [] } }
    static var requested: [URL] { lock.withLock { seen } }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RecordingURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        if let url = request.url { Self.lock.withLock { Self.seen.append(url) } }
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data("{}".utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@Suite(.serialized) struct URLSessionRideTransportTests {
    let credentialed = RiderRequest(method: "POST", path: "api/v1/rider/rides", body: Data("{}".utf8), bearerToken: "tok")

    @Test func plainHTTPNeverReachesTheSession() async throws {
        RecordingURLProtocol.reset()
        let transport = URLSessionRideTransport(session: RecordingURLProtocol.session())

        await #expect(throws: URLError.self) {
            _ = try await transport.send(credentialed, baseURL: URL(string: "http://vp.example.org")!)
        }
        #expect(RecordingURLProtocol.requested.isEmpty, "the token must not go out over plain HTTP")
    }

    @Test func httpsIsSent() async throws {
        RecordingURLProtocol.reset()
        let transport = URLSessionRideTransport(session: RecordingURLProtocol.session())

        let response = try await transport.send(credentialed, baseURL: URL(string: "https://vp.example.org")!)
        #expect(response.status == 200)
        #expect(RecordingURLProtocol.requested.count == 1)
    }

    /// The development transport is the only way to reach an http:// server,
    /// so nothing built the ordinary way can be pointed at one by accident.
    @Test func developmentTransportAllowsHTTP() async throws {
        RecordingURLProtocol.reset()
        let transport = URLSessionRideTransport.insecureForDevelopment(session: RecordingURLProtocol.session())

        let response = try await transport.send(credentialed, baseURL: URL(string: "http://127.0.0.1:8080")!)
        #expect(response.status == 200)
        #expect(RecordingURLProtocol.requested.count == 1)
    }

    @Test func redirectsAreRefusedUnlessTheyStayOnTheOrigin() {
        let origin = URL(string: "https://vp.example.org/api/v1/rider/rides")!
        let guardian = RedirectGuard(origin: origin, allowsInsecureHTTP: false)
        func follows(_ target: String) -> Bool { guardian.mayFollow(URL(string: target)) }

        #expect(follows("https://vp.example.org/api/v2/rider/rides"), "same origin, new path")
        #expect(!follows("http://vp.example.org/api/v1/rider/rides"), "no downgrade to plain HTTP")
        #expect(!follows("https://evil.example.com/api/v1/rider/rides"), "no other host")
        #expect(!follows("https://vp.example.org:8443/api/v1/rider/rides"), "no other port")
        #expect(!guardian.mayFollow(nil), "a redirect with no URL is not followed")
    }
}
