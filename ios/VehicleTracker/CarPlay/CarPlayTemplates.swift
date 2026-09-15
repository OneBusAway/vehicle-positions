import CarPlay
import UIKit

/// Builds the pieces of the car screen from session state. Pure functions of
/// their inputs, so each is tested without a car; the controller only wires
/// them to the interface controller.
@MainActor
enum CarPlayTemplates {
    /// A label in the navigation bar: a bar button that does nothing and
    /// looks disabled, which is the only text a map template can show there.
    static func titleButton(_ title: String) -> CPBarButton {
        let button = CPBarButton(title: title) { _ in }
        button.isEnabled = false
        return button
    }

    /// Navigation bar for the phases with no trip on the map (spec §6.2).
    static func idleBarButtons(phase: TripSession.Phase, onStart: @escaping () -> Void) -> (leading: [CPBarButton], trailing: [CPBarButton]) {
        switch phase {
        case .signedOut:
            return ([titleButton(String(localized: "Sign in on iPhone"))], [])
        case .starting:
            return ([titleButton(String(localized: "Starting…"))], [])
        default:
            let start = CPBarButton(title: String(localized: "Start trip")) { _ in onStart() }
            return ([titleButton(String(localized: "OBA Vehicle Tracker"))], [start])
        }
    }

    /// Navigation bar while a trip is paused: the driver can end it here, but
    /// only the phone can resume it (spec §6.2).
    static func pausedBarButtons(onEnd: @escaping () -> Void) -> (leading: [CPBarButton], trailing: [CPBarButton]) {
        let end = CPBarButton(title: String(localized: "End")) { _ in onEnd() }
        return ([end], [titleButton(String(localized: "Resume on iPhone"))])
    }

    // MARK: Active trip

    /// The next stop as a maneuver instruction, longest variant first: name,
    /// scheduled time and deviation; then name and deviation; then the name.
    static func maneuverVariants(stop: TripStop, adherence: Adherence?, timezone: String) -> [String] {
        let scheduled = Formatters.clock(stop.arrivalAt, timezone: timezone)
        guard let adherence else {
            return ["\(stop.name) · \(scheduled)", stop.name]
        }
        let deviation = Formatters.deviation(adherence.scheduleDeviation)
        return ["\(stop.name) · \(scheduled) · \(deviation)", "\(stop.name) · \(deviation)", stop.name]
    }

    static func maneuver(stop: TripStop, adherence: Adherence?, timezone: String) -> CPManeuver {
        let m = CPManeuver()
        m.instructionVariants = maneuverVariants(stop: stop, adherence: adherence, timezone: timezone)
        m.symbolImage = UIImage(systemName: "bus.fill", withConfiguration: UIImage.SymbolConfiguration(pointSize: 40, weight: .medium))
        m.userInfo = stop.id
        return m
    }

    /// CarPlay's estimate colour has no blue, so late is orange here; the
    /// rest of the app keeps OneBusAway's colours.
    static func timeRemainingColor(for adherence: Adherence?) -> CPTimeRemainingColor {
        switch adherence?.status {
        case .onTime: .green
        case .late: .orange
        case .early: .red
        case .offSchedule, .offRoute, nil: .default
        }
    }

    static func stopEstimates(adherence: Adherence) -> CPTravelEstimates {
        CPTravelEstimates(distanceRemaining: Measurement(value: adherence.distanceToNextStop, unit: UnitLength.meters),
                          timeRemaining: adherence.timeToNextStop)
    }

    /// Distance and time to the last stop at the current speed.
    static func tripEstimates(active: ActiveTrip, adherence: Adherence) -> CPTravelEstimates {
        let end = active.trip.stops.last?.alongShapeM ?? adherence.projection.alongShape
        let distance = max(0, end - adherence.projection.alongShape)
        let time = distance / max(adherence.fix.speed, AdherenceEvaluator.minimumSpeed)
        return CPTravelEstimates(distanceRemaining: Measurement(value: distance, unit: UnitLength.meters), timeRemaining: time)
    }

    static func detailItems(active: ActiveTrip, adherence: Adherence?, reporting: TripSession.ReportingStatus) -> [CPInformationItem] {
        let tz = active.trip.timezone
        let route = active.trip.route
        let schedule = adherence.map(\.statusLabel) ?? String(localized: "Waiting for GPS")
        let next = adherence.map { "\($0.nextStop.name) · \(Formatters.clock($0.nextStop.arrivalAt, timezone: tz)) · \(Formatters.distance($0.distanceToNextStop))" }
            ?? String(localized: "—")
        let reportingText: String = {
            if case .connected(let sent) = reporting { return String(localized: "Connected · \(sent) sent") }
            return reporting.label
        }()
        let gps: String = {
            guard let a = adherence, a.fix.horizontalAccuracy >= 0 else { return String(localized: "—") }
            return "±\(Int(a.fix.horizontalAccuracy.rounded())) m"
        }()
        return [
            CPInformationItem(title: String(localized: "Route"), detail: "\(route.shortName) · \(route.longName)"),
            CPInformationItem(title: String(localized: "Trip"), detail: "\(active.trip.headsign) · \(Formatters.clock(active.trip.stops.first?.departureAt ?? active.startedAt, timezone: tz))"),
            CPInformationItem(title: String(localized: "Schedule"), detail: schedule),
            CPInformationItem(title: String(localized: "Next stop"), detail: next),
            CPInformationItem(title: String(localized: "Reporting"), detail: reportingText),
            CPInformationItem(title: String(localized: "GPS"), detail: gps),
        ]
    }

    static var offRouteTitle: String { String(localized: "Off route") }

    /// How far off the route the vehicle is. The controller refreshes a
    /// standing alert with this while the distance text keeps changing.
    static func offRouteSubtitle(adherence: Adherence) -> String {
        String(localized: "\(Formatters.distance(adherence.projection.distanceToShape)) from the route")
    }

    static func offRouteAlert(adherence: Adherence, onOK: @escaping () -> Void) -> CPNavigationAlert {
        let ok = CPAlertAction(title: String(localized: "OK"), style: .default) { _ in onOK() }
        return CPNavigationAlert(
            titleVariants: [offRouteTitle],
            subtitleVariants: [offRouteSubtitle(adherence: adherence)],
            image: UIImage(systemName: "exclamationmark.triangle.fill"),
            primaryAction: ok, secondaryAction: nil, duration: 0
        )
    }

    // MARK: Map buttons

    static func mapButtons(onPan: @escaping () -> Void, onZoomIn: @escaping () -> Void, onZoomOut: @escaping () -> Void) -> [CPMapButton] {
        func button(_ symbol: String, _ handler: @escaping () -> Void) -> CPMapButton {
            let b = CPMapButton { _ in handler() }
            b.image = UIImage(systemName: symbol, withConfiguration: UIImage.SymbolConfiguration(pointSize: 24, weight: .semibold))
            return b
        }
        return [
            button("arrow.up.and.down.and.arrow.left.and.right", onPan),
            button("plus.magnifyingglass", onZoomIn),
            button("minus.magnifyingglass", onZoomOut),
        ]
    }

    // MARK: Starting a trip from the car

    /// A list template capped at the vehicle's item limit; when the cap cuts
    /// anything off, the last row says so.
    static func list(title: String, sections: [(header: String?, items: [CPListItem])], maxItems: Int) -> CPListTemplate {
        let cap = max(maxItems, 1)
        let total = sections.reduce(0) { $0 + $1.items.count }
        let truncated = total > cap
        var remaining = truncated ? cap - 1 : cap // room for the "see more" row
        var built: [CPListSection] = []
        for section in sections where remaining > 0 && !section.items.isEmpty {
            let items = Array(section.items.prefix(remaining))
            remaining -= items.count
            built.append(CPListSection(items: items, header: section.header, sectionIndexTitle: nil))
        }
        if truncated {
            let more = CPListItem(text: String(localized: "Use iPhone to see more"), detailText: nil)
            more.isEnabled = false
            built.append(CPListSection(items: [more]))
        }
        return CPListTemplate(title: title, sections: built)
    }

    static func vehicleItems(_ vehicles: [Vehicle], onSelect: @escaping (Vehicle) -> Void) -> [CPListItem] {
        vehicles.map { vehicle in
            let item = CPListItem(text: vehicle.label.isEmpty ? vehicle.id : vehicle.label, detailText: nil)
            item.handler = { _, done in
                onSelect(vehicle)
                done()
            }
            return item
        }
    }

    static func routeSections(_ routes: [RouteInfo], recentIDs: [String], onSelect: @escaping (RouteInfo) -> Void) -> [(header: String?, items: [CPListItem])] {
        func item(_ route: RouteInfo) -> CPListItem {
            let item = CPListItem(text: route.shortName.isEmpty ? route.longName : route.shortName, detailText: route.longName)
            item.handler = { _, done in
                onSelect(route)
                done()
            }
            return item
        }
        let recent = recentIDs.compactMap { id in routes.first { $0.id == id } }
        var sections: [(header: String?, items: [CPListItem])] = []
        if !recent.isEmpty {
            sections.append((String(localized: "Recent"), recent.map(item)))
        }
        sections.append((String(localized: "All routes"), routes.map(item)))
        return sections
    }

    static func tripItems(_ page: RouteTripsPage, now: Date, onSelect: @escaping (TripSummary) -> Void) -> [CPListItem] {
        let highlighted = TripRun.highlighted(in: page.trips, now: now)
        return page.trips.map { trip in
            var detail = "\(trip.firstStop) → \(trip.lastStop)"
            if trip.id == highlighted?.id {
                detail = (trip.startsAt <= now ? String(localized: "Now") : String(localized: "Next")) + " · " + detail
            }
            let item = CPListItem(text: "\(Formatters.clock(trip.startsAt, timezone: page.timezone)) → \(trip.headsign)", detailText: detail)
            item.handler = { _, done in
                onSelect(trip)
                done()
            }
            return item
        }
    }
}
