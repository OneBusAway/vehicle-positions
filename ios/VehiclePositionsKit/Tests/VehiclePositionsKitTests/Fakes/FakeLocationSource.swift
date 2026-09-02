import Foundation
@testable import VehiclePositionsKit

/// A location source driven by the test: samples are pushed with `emit`, and
/// every background-activity handle it hands out is kept for inspection.
final class FakeLocationSource: LocationSource, @unchecked Sendable {
    final class Handle: BackgroundActivityHandle, @unchecked Sendable {
        private let lock = NSLock()
        private var _invalidated = false

        var invalidated: Bool { lock.withLock { _invalidated } }

        func invalidate() {
            lock.withLock { _invalidated = true }
        }
    }

    private let lock = NSLock()
    private var _handles: [Handle] = []
    private var continuation: AsyncThrowingStream<LocationSample, any Error>.Continuation?

    var handles: [Handle] { lock.withLock { _handles } }
    var lastHandleInvalidated: Bool { handles.last?.invalidated ?? false }

    func updates() -> AsyncThrowingStream<LocationSample, any Error> {
        AsyncThrowingStream { continuation in
            lock.withLock { self.continuation = continuation }
        }
    }

    func beginBackgroundActivity() -> any BackgroundActivityHandle {
        let handle = Handle()
        lock.withLock { _handles.append(handle) }
        return handle
    }

    func emit(_ sample: LocationSample) {
        lock.withLock { continuation }?.yield(sample)
    }

    func emitFix(
        lat: Double,
        lon: Double,
        at timestamp: Date,
        accuracy: Double = 5,
        speed: Double = 8,
        course: Double = 0,
        stationary: Bool = false
    ) {
        let fix = LocationFix(
            latitude: lat,
            longitude: lon,
            horizontalAccuracy: accuracy,
            speed: speed,
            course: course,
            timestamp: timestamp
        )
        emit(LocationSample(fix: fix, isStationary: stationary))
    }

    func finish(throwing error: (any Error)? = nil) {
        lock.withLock { continuation }?.finish(throwing: error)
    }
}
