import Foundation
import Testing
@testable import VehicleTracker

/// Real `UserDefaults` suites, so a relaunch is a second choice reading the
/// same store, with the sign-in read back from it as the app does.
@Suite struct OccupancyChoiceTests {
    func withDefaults(_ body: (UserDefaults) throws -> Void) rethrows {
        let name = "OccupancyChoiceTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        try body(defaults)
    }

    @Test func aTripStartsWithNone() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults, settings: .signedIn(defaults))
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
        }
    }

    @Test func aTapSetsItAndAnotherChangesIt() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults, settings: .signedIn(defaults))
            choice.choose(.fewSeatsAvailable, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == .fewSeatsAvailable)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == .full)
        }
    }

    @Test func tappingTheSelectedOneClearsIt() {
        withDefaults { defaults in
            let settings = AppSettings.signedIn(defaults)
            let choice = OccupancyChoice(defaults: defaults, settings: settings)
            choice.choose(.standingRoomOnly, tripID: "T1", serviceDate: "20260902")
            choice.choose(.standingRoomOnly, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
            #expect(OccupancyChoice(defaults: defaults, settings: settings).occupancy(tripID: "T1", serviceDate: "20260902") == nil,
                    "cleared in the store too, so a relaunch does not bring it back")
        }
    }

    @Test func aNewTripStartsWithNone() {
        withDefaults { defaults in
            let choice = OccupancyChoice(defaults: defaults, settings: .signedIn(defaults))
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            #expect(choice.occupancy(tripID: "T2", serviceDate: "20260902") == nil)
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260903") == nil, "the same trip id on another day is another trip")
        }
    }

    @Test func theSameDriverOnTheSameServerHasItAfterARelaunch() {
        withDefaults { defaults in
            OccupancyChoice(defaults: defaults, settings: .signedIn(defaults)).choose(.full, tripID: "T1", serviceDate: "20260902")
            let relaunched = OccupancyChoice(defaults: defaults, settings: AppSettings(defaults: defaults))
            #expect(relaunched.occupancy(tripID: "T1", serviceDate: "20260902") == .full)
        }
    }

    /// Phones are handed on between shifts: the next driver starting the same
    /// trip that day must not send the last one's choice.
    @Test func anotherDriverOnTheSameServerStartsWithNone() {
        withDefaults { defaults in
            let settings = AppSettings.signedIn(defaults)
            let choice = OccupancyChoice(defaults: defaults, settings: settings)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            settings.email = "ben@example.org"
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
            let relaunched = OccupancyChoice(defaults: defaults, settings: AppSettings(defaults: defaults))
            #expect(relaunched.occupancy(tripID: "T1", serviceDate: "20260902") == nil, "nor after a relaunch")
        }
    }

    /// Trip ids are the agency's, so another server can have one of the same
    /// name on the same day.
    @Test func theSameDriverOnAnotherServerStartsWithNone() {
        withDefaults { defaults in
            let settings = AppSettings.signedIn(defaults)
            let choice = OccupancyChoice(defaults: defaults, settings: settings)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            settings.serverURLString = "https://other-agency.example.org"
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
            let relaunched = OccupancyChoice(defaults: defaults, settings: AppSettings(defaults: defaults))
            #expect(relaunched.occupancy(tripID: "T1", serviceDate: "20260902") == nil, "nor after a relaunch")
        }
    }

    /// The same driver and server as the session itself tells them apart:
    /// email case, a trailing slash or a written-out port don't make another.
    @Test func theSameDriverAndServerWrittenDifferentlyStillHaveIt() {
        withDefaults { defaults in
            let settings = AppSettings.signedIn(defaults)
            let choice = OccupancyChoice(defaults: defaults, settings: settings)
            choice.choose(.full, tripID: "T1", serviceDate: "20260902")
            settings.email = "Ana@Example.org"
            settings.serverURLString = "https://transit.example.org:443/"
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == .full)
        }
    }

    /// A choice saved by the build before this one says nothing of who made
    /// it, so it is not taken up by whoever is signed in now.
    @Test func aChoiceSavedWithoutItsDriverIsNotTakenUp() throws {
        struct Unowned: Codable {
            var tripID: String
            var serviceDate: String
            var occupancy: Occupancy
        }
        try withDefaults { defaults in
            let saved = try JSONCoding.encoder.encode(Unowned(tripID: "T1", serviceDate: "20260902", occupancy: .full))
            defaults.set(saved, forKey: OccupancyChoice.key)
            let choice = OccupancyChoice(defaults: defaults, settings: .signedIn(defaults))
            #expect(choice.occupancy(tripID: "T1", serviceDate: "20260902") == nil)
        }
    }

    /// The trip sheet and the reporter share one choice, which must go by the
    /// app's own sign-in, not a copy of it read at launch.
    @Test func theSharedChoiceGoesByTheAppsSignIn() {
        #expect(OccupancyChoice.shared.settings === AppContainer.shared.settings)
    }
}

extension AppSettings {
    /// The settings a sign-in leaves in `defaults`.
    static func signedIn(_ defaults: UserDefaults, email: String = "ana@example.org",
                         server: String = "https://transit.example.org") -> AppSettings {
        let settings = AppSettings(defaults: defaults)
        settings.email = email
        settings.serverURLString = server
        return settings
    }
}
