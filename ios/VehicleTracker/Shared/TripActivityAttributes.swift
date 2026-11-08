import ActivityKit
import Foundation

/// The running trip as its Live Activity shows it: on the Lock Screen, in the
/// Dynamic Island, in the Apple Watch Smart Stack and on the CarPlay
/// Dashboard. The words are finished in the app, from `TripStatus`, so every
/// surface says what the Tracking screen and CarPlay say. The extension only
/// draws them: it has no string catalog, so it can never fall back to English.
nonisolated struct TripActivityAttributes: ActivityAttributes {
    /// What changes during the trip. Nothing in it changes with every fix (no
    /// distance, no count), so an update goes out only when the words do.
    nonisolated struct ContentState: Codable, Hashable {
        /// The vehicle's status colour, as the ring round it on the map; the
        /// lead's colour unless `attention`.
        var tone: StatusTone
        /// A problem or Signed out: the lead and its symbol are amber.
        var attention: Bool
        /// The SF Symbol for the lead.
        var symbol: String
        /// "On time", "6 min late", "No connection", "Signed out".
        var lead: String
        /// "6 min", for the compact Dynamic Island; only while late or early.
        var minutes: String?
        /// "Next 3rd Ave & Seneca St · 7:18 AM"; nil before the first fix and
        /// while signed out.
        var next: String?
        /// The reports line's symbol; nil when the line is a problem's second
        /// line.
        var reportsSymbol: String?
        /// "Reporting", "Ending…", or what a problem means for the driver.
        var reports: String
        /// The activity as VoiceOver reads it, in one sentence.
        var spoken: String
    }

    /// The trip's id on the server: what tells one trip's activity from another's.
    var serverTripID: Int64
    /// The route as its badge names it.
    var routeName: String
    /// The feed's hex colours, as `RouteBadge` takes them.
    var routeColor: String
    var routeTextColor: String
    var headsign: String
}
