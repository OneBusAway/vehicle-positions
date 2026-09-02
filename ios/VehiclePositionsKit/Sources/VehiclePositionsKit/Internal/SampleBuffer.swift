import Foundation

/// Fixes waiting to be uploaded, oldest first. Anything older than `retention`
/// is dropped on append: a position half an hour stale helps nobody, and an
/// offline device must not grow its buffer without bound.
struct SampleBuffer: Sendable {
    let retention: Duration
    private(set) var fixes: [LocationFix] = []

    init(retention: Duration) {
        self.retention = retention
    }

    var count: Int { fixes.count }

    mutating func append(_ fix: LocationFix, now: Date) {
        fixes.append(fix)
        let cutoff = now.addingTimeInterval(-retention.timeInterval)
        fixes.removeAll { $0.timestamp < cutoff }
    }

    /// Removes and returns up to `max` fixes, oldest first.
    mutating func take(max: Int) -> [LocationFix] {
        let taken = Swift.min(Swift.max(max, 0), fixes.count)
        guard taken > 0 else { return [] }
        defer { fixes.removeFirst(taken) }
        return Array(fixes.prefix(taken))
    }

    /// Puts fixes back at the front after a failed upload, preserving order.
    mutating func restore(_ restored: [LocationFix]) {
        fixes.insert(contentsOf: restored, at: 0)
    }
}
