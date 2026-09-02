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

    /// What a held endpoint does to a caller that gets cancelled while it waits.
    enum HoldPolicy {
        /// Fail the way a real request would: throw `CancellationError`.
        case failOnCancellation
        /// Ignore cancellation and answer only when `release` says so, which is
        /// how a test gets a response the caller no longer wants.
        case waitForRelease
    }

    private let lock = NSLock()
    private var _recorded: [Recorded] = []
    private var scripted: [String: [Result<RiderResponse, any Error>]] = [:]
    private var held: [String: HoldPolicy] = [:]
    private var waiters: [String: [CheckedContinuation<Void, Never>]] = [:]

    var recorded: [Recorded] { lock.withLock { _recorded } }

    /// Appends results for `key`. Consumed FIFO; the last one repeats forever.
    func script(_ key: String, _ results: Result<RiderResponse, any Error>...) {
        lock.withLock { scripted[key, default: []].append(contentsOf: results) }
    }

    /// Makes every send for `key` record its request and then hang, so a test
    /// can act while the request is in flight.
    func hold(_ key: String, _ policy: HoldPolicy = .failOnCancellation) {
        lock.withLock { held[key] = policy }
    }

    /// Lets a held endpoint answer: waiting sends resume with their scripted
    /// result and later ones are not held at all.
    func release(_ key: String) {
        let waiting: [CheckedContinuation<Void, Never>] = lock.withLock {
            held[key] = nil
            return waiters.removeValue(forKey: key) ?? []
        }
        for continuation in waiting { continuation.resume() }
    }

    func send(_ request: RiderRequest, baseURL: URL) async throws -> RiderResponse {
        let key = Self.key(for: request)
        lock.withLock { _recorded.append(Recorded(request: request, baseURL: baseURL)) }
        // Before the script is touched: a send that never answers must not eat
        // the response the next one is owed.
        try await waitWhileHeld(key)
        let result: Result<RiderResponse, any Error> = try lock.withLock {
            guard var queue = scripted[key], !queue.isEmpty else { throw Unscripted(key: key) }
            // The last scripted result stays put so it repeats when exhausted.
            let next = queue.count > 1 ? queue.removeFirst() : queue[0]
            scripted[key] = queue
            return next
        }
        return try result.get()
    }

    private func waitWhileHeld(_ key: String) async throws {
        switch lock.withLock({ held[key] }) {
        case .none:
            return
        case .failOnCancellation:
            // `Task.sleep` throws on cancellation, so a held request answers a
            // cancelled caller the way a real one would.
            while lock.withLock({ held[key] != nil }) {
                try await Task.sleep(for: .milliseconds(5))
            }
        case .waitForRelease:
            await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
                let parked = lock.withLock {
                    guard held[key] != nil else { return false }
                    waiters[key, default: []].append(continuation)
                    return true
                }
                if !parked { continuation.resume() }
            }
        }
    }

    func requests(matching key: String) -> [RiderRequest] {
        recorded.map(\.request).filter { Self.key(for: $0) == key }
    }

    /// Polls (real time) until a request for `key` has been recorded.
    func waitForRequest(matching key: String, timeout: Duration = .seconds(5)) async -> Bool {
        await poll(timeout: timeout) { !self.requests(matching: key).isEmpty }
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
