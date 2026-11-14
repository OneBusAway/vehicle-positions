import Foundation
import Observation

/// How full the vehicle is, as the driver says it: the four GTFS-RT
/// `OccupancyStatus` values the Tracking screen offers, spelled as
/// `POST /api/v1/locations` takes them.
nonisolated enum Occupancy: String, Sendable, Codable, CaseIterable {
    case manySeatsAvailable = "MANY_SEATS_AVAILABLE"
    case fewSeatsAvailable = "FEW_SEATS_AVAILABLE"
    case standingRoomOnly = "STANDING_ROOM_ONLY"
    case full = "FULL"

    /// One step fuller (`up`) or emptier than `current`, where nil is "not
    /// set" and sits below the emptiest; it stops at either end. VoiceOver's
    /// swipe up and down on the row.
    static func stepped(_ current: Occupancy?, up: Bool) -> Occupancy? {
        let all: [Occupancy?] = [nil] + allCases
        let index = all.firstIndex(of: current) ?? 0
        return all[up ? min(index + 1, all.count - 1) : max(index - 1, 0)]
    }
}

/// The driver's occupancy choice, which belongs to one trip: it is keyed by
/// the trip's GTFS id and service date, so a new trip starts with none, and
/// kept in `UserDefaults`, so a relaunch keeps it. Only the latest trip's
/// choice is kept.
@MainActor @Observable
final class OccupancyChoice {
    /// The one the trip sheet and the reporter share.
    static let shared = OccupancyChoice()
    nonisolated static let key = "occupancy-choice"

    private nonisolated struct Stored: Codable, Equatable {
        var tripID: String
        var serviceDate: String
        var occupancy: Occupancy
    }

    private let defaults: UserDefaults
    private var stored: Stored?

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        stored = defaults.data(forKey: Self.key).flatMap { try? JSONCoding.decoder.decode(Stored.self, from: $0) }
    }

    func occupancy(tripID: String, serviceDate: String) -> Occupancy? {
        guard let stored, stored.tripID == tripID, stored.serviceDate == serviceDate else { return nil }
        return stored.occupancy
    }

    /// A tap on `occupancy`: it becomes the choice, or clears it when it
    /// already was.
    func choose(_ occupancy: Occupancy, tripID: String, serviceDate: String) {
        let current = self.occupancy(tripID: tripID, serviceDate: serviceDate)
        set(current == occupancy ? nil : occupancy, tripID: tripID, serviceDate: serviceDate)
    }

    func set(_ occupancy: Occupancy?, tripID: String, serviceDate: String) {
        stored = occupancy.map { Stored(tripID: tripID, serviceDate: serviceDate, occupancy: $0) }
        if let stored, let data = try? JSONCoding.encoder.encode(stored) {
            defaults.set(data, forKey: Self.key)
        } else {
            defaults.removeObject(forKey: Self.key)
        }
    }
}
