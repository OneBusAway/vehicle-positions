import Foundation
@testable import VehiclePositionsKit

/// A scripted stand-in for the network. Responses are queued per endpoint key
/// ("METHOD group", e.g. `"POST positions"`) and consumed in order; the last
/// scripted result repeats once the queue runs dry.
final class FakeRideTransport: RideTransport, @unchecked Sendable {
    struct Recorded: Sendable {
        let request: RiderRequest
        let baseURL: URL
    }

    struct Unscripted: Error {
        let key: String
    }

    private let lock = NSLock()
    private var _recorded: [Recorded] = []
    private var scripted: [String: [Result<RiderResponse, any Error>]] = [:]

    var recorded: [Recorded] { lock.withLock { _recorded } }

    /// Appends results for `key`. Consumed FIFO; the last one repeats forever.
    func script(_ key: String, _ results: Result<RiderResponse, any Error>...) {
        lock.withLock { scripted[key, default: []].append(contentsOf: results) }
    }

    func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse {
        let key = Self.key(for: request)
        let result: Result<RiderResponse, any Error> = try lock.withLock {
            _recorded.append(Recorded(request: request, baseURL: baseURL))
            guard var queue = scripted[key], !queue.isEmpty else { throw Unscripted(key: key) }
            // The last scripted result stays put so it repeats when exhausted.
            let next = queue.count > 1 ? queue.removeFirst() : queue[0]
            scripted[key] = queue
            return next
        }
        return try result.get()
    }

    func requests(matching key: String) -> [RiderRequest] {
        recorded.map(\.request).filter { Self.key(for: $0) == key }
    }

    static func ok(_ status: Int = 200, json: String) -> Result<RiderResponse, any Error> {
        .success(RiderResponse(status: status, body: Data(json.utf8)))
    }

    static func error(_ status: Int, message: String) -> Result<RiderResponse, any Error> {
        let body = try! RiderAPICodec.encode(ServerErrorBody(error: message))
        return .success(RiderResponse(status: status, body: body))
    }

    static let transportFailure: Result<RiderResponse, any Error> = .failure(URLError(.notConnectedToInternet))

    /// "POST register" | "POST rides" | "POST positions" | "POST end" | "GET status"
    private static func key(for request: RiderRequest) -> String {
        let group = request.path.split(separator: "/").last.map(String.init) ?? ""
        return request.method + " " + group
    }
}
