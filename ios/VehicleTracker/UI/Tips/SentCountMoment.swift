import Foundation

/// Where the vehicle is in the moment Tracking's tip waits for (walkthrough
/// spec §8): the tip says what the sent count is, and it may say so only
/// before the vehicle moves.
nonisolated enum SentCountMoment: Equatable {
    /// Waiting for GPS, or a known speed under 1 m/s: the tip may show.
    case parked
    /// A fix with no speed: a tip that is up stays, and none comes up.
    case unknown
    /// 1 m/s or more: a tip that is up goes, and that counts as seen.
    case moving

    /// The speed, in m/s, from which the vehicle counts as moving.
    static let movingSpeed = 1.0

    /// `answer` is the status card's; `speed` is the last fix's, in m/s,
    /// which Core Location gives as -1 when it does not know it. A speed
    /// that says the vehicle moves wins over everything else: nothing asks
    /// for attention on the road.
    static func of(answer: TripStatus.Answer?, speed: Double?) -> SentCountMoment {
        if let speed, speed >= movingSpeed { return .moving }
        if case .waiting? = answer { return .parked }
        if let speed, speed >= 0 { return .parked }
        return .unknown
    }
}
