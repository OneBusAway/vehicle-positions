import Testing
@testable import VehicleTracker

/// When Tracking's tip may show (walkthrough spec §8): only before the
/// vehicle moves. The status is the card's answer; the speed is the last
/// fix's, which Core Location gives as -1 when it does not know it.
@Suite struct SentCountMomentTests {
    let answers: [TripStatus.Answer] = [.waiting, .onTime, .late, .early, .offSchedule, .offRoute]
    var withAFix: [TripStatus.Answer] { answers.filter { $0 != .waiting } }

    /// Waiting for GPS: no fix yet, so no speed, and the vehicle has not
    /// been seen to move.
    @Test func waitingForGPSIsParked() {
        #expect(SentCountMoment.of(answer: .waiting, speed: nil) == .parked)
        #expect(SentCountMoment.of(answer: .waiting, speed: -1) == .parked)
        #expect(SentCountMoment.of(answer: .waiting, speed: 0) == .parked)
    }

    /// Whatever the card says, a known speed under 1 m/s is parked.
    @Test(arguments: [0, 0.2, 0.5, 0.99])
    func aKnownSpeedUnderOneMetreASecondIsParked(speed: Double) {
        for answer in answers {
            #expect(SentCountMoment.of(answer: answer, speed: speed) == .parked, "\(answer)")
        }
    }

    /// From 1 m/s the vehicle moves, whatever the card says: even Waiting
    /// for GPS, which a moving fix cannot come with, does not keep a tip on
    /// the road.
    @Test(arguments: [1, 1.0001, 5, 27])
    func fromOneMetreASecondItIsMoving(speed: Double) {
        for answer in answers {
            #expect(SentCountMoment.of(answer: answer, speed: speed) == .moving, "\(answer)")
        }
    }

    /// A fix whose speed is not known says nothing either way: a tip that
    /// is up stays, and none comes up.
    @Test(arguments: [-1, -0.5])
    func anUnknownSpeedIsUnknown(speed: Double) {
        for answer in withAFix {
            #expect(SentCountMoment.of(answer: answer, speed: speed) == .unknown, "\(answer)")
        }
    }

    @Test func noSpeedAfterTheFirstFixIsUnknown() {
        for answer in withAFix {
            #expect(SentCountMoment.of(answer: answer, speed: nil) == .unknown, "\(answer)")
        }
    }

    /// No trip to describe, no card.
    @Test func noStatusIsUnknownUntilTheSpeedSays() {
        #expect(SentCountMoment.of(answer: nil, speed: nil) == .unknown)
        #expect(SentCountMoment.of(answer: nil, speed: -1) == .unknown)
        #expect(SentCountMoment.of(answer: nil, speed: 0.4) == .parked)
        #expect(SentCountMoment.of(answer: nil, speed: 3) == .moving)
    }

    /// The tip goes by itself as the vehicle pulls away: an unknown speed,
    /// stopped, walking pace, then 1 m/s and up.
    @Test func pullingAwayMovesTheTipOn() {
        let speeds = [-1, 0, 0.6, 1, 4.2]
        #expect(speeds.map { SentCountMoment.of(answer: .onTime, speed: $0) } == [.unknown, .parked, .parked, .moving, .moving])
        #expect(SentCountMoment.movingSpeed == 1)
    }
}
