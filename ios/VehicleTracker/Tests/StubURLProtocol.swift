import Foundation
import Synchronization

/// Answers every request made through a URLSession configured with it, and
/// records the requests so tests can inspect method, headers and body.
nonisolated final class StubURLProtocol: URLProtocol {
    struct Recorded: Sendable {
        var method: String
        var url: URL
        var headers: [String: String]
        var body: Data?
    }

    nonisolated static let responder = Mutex<(@Sendable (URLRequest) -> (Int, Data))?>(nil)
    nonisolated static let recorded = Mutex<[Recorded]>([])

    static func reset(_ respond: @escaping @Sendable (URLRequest) -> (Int, Data)) {
        responder.withLock { $0 = respond }
        recorded.withLock { $0.removeAll() }
    }

    static var last: Recorded? { recorded.withLock { $0.last } }
    static var count: Int { recorded.withLock { $0.count } }

    static func configuration() -> URLSessionConfiguration {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [StubURLProtocol.self]
        return c
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let body = request.httpBody ?? request.httpBodyStream.map { stream in
            stream.open()
            defer { stream.close() }
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buffer, maxLength: buffer.count)
                if n <= 0 { break }
                data.append(buffer, count: n)
            }
            return data
        }
        Self.recorded.withLock {
            $0.append(Recorded(method: request.httpMethod ?? "", url: request.url!,
                               headers: request.allHTTPHeaderFields ?? [:], body: body))
        }
        let (status, data) = Self.responder.withLock { $0 }?(request) ?? (500, Data())
        var headers = ["Content-Type": "application/json"]
        // A real 3xx carries a Location; wire it up and tell the client so
        // the session's task delegate (RedirectRefuser) is actually consulted
        // instead of the test merely proving a bare 3xx surfaces as a status.
        let redirectLocation = "https://elsewhere.example.org/api/v1/vehicles"
        if (300..<400).contains(status) {
            headers["Location"] = redirectLocation
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
                                       headerFields: headers)!
        if (300..<400).contains(status), let redirectURL = URL(string: redirectLocation) {
            var redirected = URLRequest(url: redirectURL)
            redirected.httpMethod = request.httpMethod
            client?.urlProtocol(self, wasRedirectedTo: redirected, redirectResponse: response)
        }
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
