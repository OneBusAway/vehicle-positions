import Foundation

/// Every word the picker screens and CarPlay's picker templates show, in
/// Android's words (`res/values/strings.xml`, `vehicles_*`, `routes_*`,
/// `runs_*`) with "vehicle" for "bus". One place for all of them, so the
/// phone and the car cannot differ.
nonisolated enum PickerText {
    // MARK: Choose your vehicle

    static var vehiclesTitle: String {
        String(localized: "Choose your vehicle", comment: "Title of the first picker screen, the driver's assigned vehicles.")
    }

    static var vehicleSearchPrompt: String {
        String(localized: "Vehicle number", comment: "Placeholder of the vehicle screen's search field: the fleet number painted on the vehicle.")
    }

    static var favorites: String {
        String(localized: "Favorites", comment: "Section header over the vehicles the driver starred.")
    }

    static var recent: String {
        String(localized: "Recent", comment: "Section header over the vehicles or routes the driver used last.")
    }

    static var allVehicles: String {
        String(localized: "All vehicles", comment: "Section header over the rest of the driver's assigned vehicles.")
    }

    static var matches: String {
        String(localized: "Matches", comment: "Section header over the vehicles or routes that match what the driver typed.")
    }

    static func section(_ section: VehicleSection) -> String {
        switch section {
        case .favorites: favorites
        case .recent: recent
        case .all: allVehicles
        case .matches: matches
        }
    }

    static func noVehicleMatches(_ query: String) -> String {
        String(localized: "No vehicle matches \(query)", comment: "Empty state title when the vehicle search finds nothing; the argument is what the driver typed.")
    }

    static var checkFleetNumber: String {
        String(localized: "Check the fleet number on the vehicle.", comment: "Empty state text under 'No vehicle matches …'.")
    }

    static var noVehicles: String {
        String(localized: "No vehicles are assigned to you", comment: "Empty state title when the driver has no assigned vehicles.")
    }

    static var askDispatcher: String {
        String(localized: "Ask your dispatcher to assign one, then check again.", comment: "Empty state text under 'No vehicles are assigned to you'.")
    }

    static var checkAgain: String {
        String(localized: "Check again", comment: "Button that loads a picker's list again when it came back empty.")
    }

    static var vehiclesLoadFailed: String {
        String(localized: "Couldn't load your vehicles", comment: "Empty state title when the vehicle list could not be loaded.")
    }

    static var checkConnection: String {
        String(localized: "Check your connection and try again.", comment: "Empty state text under a 'Couldn't load …' title.")
    }

    static var tryAgain: String {
        String(localized: "Try again", comment: "Button that retries a load or a start that failed.")
    }

    /// The tile as VoiceOver reads it: the fleet number, then the label when
    /// it says more ("4317, Bus 4317").
    static func vehicleTile(id: String, label: String?) -> String {
        guard let label else { return id }
        return String(localized: "\(id), \(label)", comment: "VoiceOver label of a vehicle tile: the fleet number, then the vehicle's label.")
    }

    static var favorite: String {
        String(localized: "Favorite", comment: "VoiceOver value of a starred vehicle tile.")
    }

    static func addToFavorites(_ id: String) -> String {
        String(localized: "Add \(id) to favorites", comment: "Label of the star on a vehicle tile that is not yet a favourite; the argument is the fleet number.")
    }

    static func removeFromFavorites(_ id: String) -> String {
        String(localized: "Remove \(id) from favorites", comment: "Label of the star on a vehicle tile that is a favourite; the argument is the fleet number.")
    }

    // MARK: Choose a route

    static var routesTitle: String {
        String(localized: "Choose a route", comment: "Title of the second picker screen, the schedule's routes.")
    }

    static var routeSearchPrompt: String {
        String(localized: "Route number or name", comment: "Placeholder of the route screen's search field.")
    }

    static var allRoutes: String {
        String(localized: "All routes", comment: "Section header over every route in the schedule, as rows with names.")
    }

    static func allRoutes(count: Int) -> String {
        String(localized: "All routes · \(count)", comment: "Section header over a grid of route numbers, from a feed that names none of its routes: how many there are.")
    }

    static func noRouteMatches(_ query: String) -> String {
        String(localized: "No route matches \(query)", comment: "Empty state title when the route search finds nothing; the argument is what the driver typed.")
    }

    static var routeSearchHint: String {
        String(localized: "Search by the number on the sign, or part of the name.", comment: "Empty state text under 'No route matches …'.")
    }

    static var noRoutes: String {
        String(localized: "The schedule has no routes", comment: "Empty state title when the server's schedule lists no routes.")
    }

    static var askAdmin: String {
        String(localized: "Ask your agency's admin to check the schedule.", comment: "Empty state text under 'The schedule has no routes'.")
    }

    static var routesLoadFailed: String {
        String(localized: "Couldn't load the routes", comment: "Empty state title when the route list could not be loaded.")
    }

    static var noSchedule: String {
        String(localized: "No schedule on this server yet", comment: "Empty state title when the server has no GTFS schedule loaded.")
    }

    static var noScheduleDetail: String {
        String(localized: "An operator has to load one before trips can start. Tell your agency's admin.", comment: "Empty state text under 'No schedule on this server yet'.")
    }

    /// The subtitle of Choose a route: the fleet number of the vehicle chosen.
    static func vehicle(_ id: String) -> String {
        String(localized: "Vehicle \(id)", comment: "Subtitle of the route screen: the fleet number of the vehicle the driver chose.")
    }

    /// The number a route goes by, or its name when the feed gave no number.
    static func name(of route: RouteInfo) -> String {
        route.shortName.isEmpty ? route.longName : route.shortName
    }

    static func route(_ name: String) -> String {
        String(localized: "Route \(name)", comment: "VoiceOver label of a route badge; the argument is the route's number.")
    }

    /// A row as VoiceOver reads it: the number, then the name. A route with
    /// only one of the two is read once, not as "Route Loop, Loop".
    static func routeRow(_ route: RouteInfo) -> String {
        if route.shortName.isEmpty || route.longName.isEmpty { return Self.route(name(of: route)) }
        return String(localized: "Route \(route.shortName), \(route.longName)", comment: "VoiceOver label of a route row: the route's number, then its name.")
    }

    // MARK: Choose a run

    static var runsTitle: String {
        String(localized: "Choose a run", comment: "Title of the third picker screen, the route's runs today.")
    }

    /// The subtitle of Choose a run: the vehicle, then the route.
    static func runsSubtitle(vehicle: String, route: String) -> String {
        String(localized: "Vehicle \(vehicle) · Route \(route)", comment: "Subtitle of the run screen: the fleet number of the vehicle, then the route's number.")
    }

    static func noRuns(route: String) -> String {
        String(localized: "No runs on route \(route) today", comment: "Empty state title when the route has no runs today; the argument is the route's number.")
    }

    static var noRunsUnnamed: String {
        String(localized: "No runs on this route today", comment: "Empty state title when a route whose feed gave no number has no runs today.")
    }

    /// "No runs on route 7 today", or the unnamed form for a route whose
    /// feed gave no number.
    static func noRunsTitle(route: RouteInfo) -> String {
        let number = route.shortName.trimmingCharacters(in: .whitespaces)
        return number.isEmpty ? noRunsUnnamed : noRuns(route: number)
    }

    static var noRunsDetail: String {
        String(localized: "Pick another route, or check again tomorrow.", comment: "Empty state text under 'No runs on route … today'.")
    }

    static var chooseAnotherRoute: String {
        String(localized: "Choose another route", comment: "Button under 'No runs on route … today' that goes back to the route screen.")
    }

    static var runsLoadFailed: String {
        String(localized: "Couldn't load the runs", comment: "Empty state title when the run list could not be loaded.")
    }

    static var now: String {
        String(localized: "Now", comment: "Tag on the run under way, and the VoiceOver label of the line between the runs gone and the rest.")
    }

    static var next: String {
        String(localized: "Next", comment: "Tag on the next run to start, when none is under way.")
    }

    /// A segment of the direction switch: where most runs that way are headed.
    static func direction(_ headsign: String) -> String {
        String(localized: "To \(headsign)", comment: "A segment of the direction switch over the run board; the argument is the headsign most of that direction's runs show, e.g. 'To Downtown Seattle'.")
    }

    static var directionTitle: String {
        String(localized: "Direction", comment: "VoiceOver title of the direction switch over the run board.")
    }

    /// Under the date: the first stop, then the last stop, of the direction shown.
    static func stops(_ first: String, _ last: String) -> String {
        String(localized: "\(first) → \(last)", comment: "Under the run board's date: the direction's first stop, then its last stop.")
    }

    static var starting: String {
        String(localized: "Starting…", comment: "VoiceOver value of the run being started.")
    }

    // MARK: Starting a run

    static func startTitle(time: String) -> String {
        String(localized: "Start the \(time) run?", comment: "Title of the start confirmation; the argument is the run's start time in the agency's zone, e.g. '6:43 AM'.")
    }

    static func startMessage(route: String, headsign: String, vehicle: String) -> String {
        String(localized: "Route \(route) to \(headsign) on vehicle \(vehicle).", comment: "Message of the start confirmation: the route's number, the run's headsign, then the vehicle's fleet number.")
    }

    /// CarPlay's alert has no message line, so its longest title carries
    /// the message too; the car picks the longest that fits.
    static func startTitleWithMessage(time: String, route: String, headsign: String, vehicle: String) -> String {
        String(localized: "Start the \(time) run? Route \(route) to \(headsign) on vehicle \(vehicle).", comment: "CarPlay's start confirmation in one line: the run's start time, the route's number, the headsign, then the vehicle's fleet number.")
    }

    static var startTrip: String {
        String(localized: "Start trip", comment: "The start confirmation's confirming button.")
    }

    static var cancel: String {
        String(localized: "Cancel", comment: "The start confirmation's cancel button.")
    }

    static func notAssigned(_ vehicleID: String) -> String {
        String(localized: "You're not assigned to vehicle \(vehicleID).", comment: "Banner when the server refused a start because the driver is not assigned to the vehicle; the argument is the fleet number.")
    }

    static var changeVehicle: String {
        String(localized: "Change vehicle", comment: "Banner button that goes back to the vehicle screen after a start was refused for the vehicle.")
    }

    static var tripAlreadyRunning: String {
        String(localized: "You already have a trip running. End it before starting another.", comment: "Banner when the server refused a start because the driver already has a trip running.")
    }

    static var cannotReachServer: String {
        String(localized: "Can't reach the server. Check your connection.", comment: "Banner when a start could not reach the server.")
    }

    static var scheduleMovedOn: String {
        String(localized: "The schedule moved on to a new day.", comment: "Banner when the server refused a start because the run is not in service on the day it now resolves to.")
    }

    static var reload: String {
        String(localized: "Reload", comment: "Banner button that loads the run list again after the schedule moved on to a new day.")
    }

    static var couldNotStart: String {
        String(localized: "Couldn't start the trip.", comment: "Banner when a start failed for a reason the app does not name.")
    }

    static var openSettings: String {
        String(localized: "Open Settings", comment: "Banner button that opens the app's page in the Settings app after a start was refused for location access.")
    }
}
