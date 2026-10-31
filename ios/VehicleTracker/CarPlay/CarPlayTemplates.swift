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
    /// scheduled time and the status card's answer, so the car says "On
    /// time" whenever the phone does; then name and answer; then the name.
    /// With no answer (the stop after next), the time and the name.
    static func maneuverVariants(stop: TripStop, answer: String?, timezone: String) -> [String] {
        let scheduled = Formatters.clock(stop.arrivalAt, timezone: timezone)
        guard let answer else {
            return ["\(stop.name) · \(scheduled)", stop.name]
        }
        return ["\(stop.name) · \(scheduled) · \(answer)", "\(stop.name) · \(answer)", stop.name]
    }

    static func maneuver(stop: TripStop, answer: String?, timezone: String) -> CPManeuver {
        let m = CPManeuver()
        m.instructionVariants = maneuverVariants(stop: stop, answer: answer, timezone: timezone)
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

    /// The Details template: the phone's status card and problem line in
    /// rows, from the same `TripStatus`, so the car and the phone agree.
    static func detailItems(status: TripStatus, adherence: Adherence?, fixesSent: Int) -> [CPInformationItem] {
        let active = status.active
        let tz = active.trip.timezone
        let route = active.trip.route
        let card = status.card
        // Off schedule, the card's answer is only the minutes; the row keeps
        // the words main showed with them.
        let schedule = status.answer == .offSchedule ? TripStatusText.pair(card.label, card.answer) : card.answer
        // From the fix, as on main. The card's lines hold the From line
        // before the first fix and the off-schedule question, neither of
        // which belongs under "Next stop".
        let next = adherence.map {
            TripStatusText.pair($0.nextStop.name, TripStatusText.pair(Formatters.clock($0.nextStop.arrivalAt, timezone: tz), Formatters.distance($0.distanceToNextStop)))
        } ?? String(localized: "—")
        let reports: String
        if let problem = status.problem {
            reports = TripStatusText.pair(problem.what, problem.meaning)
        } else if status.isSignedOut {
            reports = TripStatusText.pair(status.reports.text, String(localized: "Sign in on iPhone"))
        } else {
            reports = TripStatusText.pair(status.reports.text, TripStatusText.sent(fixesSent))
        }
        let gps: String = {
            guard let a = adherence, a.fix.horizontalAccuracy >= 0 else { return String(localized: "—") }
            return "±\(Int(a.fix.horizontalAccuracy.rounded())) m"
        }()
        return [
            CPInformationItem(title: String(localized: "Route"), detail: "\(route.shortName) · \(route.longName)"),
            CPInformationItem(title: String(localized: "Trip"), detail: "\(active.trip.headsign) · \(Formatters.clock(active.trip.stops.first?.departureAt ?? active.startedAt, timezone: tz))"),
            CPInformationItem(title: String(localized: "Schedule"), detail: schedule),
            CPInformationItem(title: String(localized: "Next stop"), detail: next),
            CPInformationItem(title: TripStatusText.reports, detail: reports),
            CPInformationItem(title: String(localized: "GPS"), detail: gps),
        ]
    }

    /// "Off route · 98 m", the phone's answer. The controller rewrites a
    /// standing alert's title with it while the distance keeps changing.
    static func offRouteTitle(adherence: Adherence) -> String {
        TripStatusText.offRoute(distance: Formatters.distance(adherence.projection.distanceToShape))
    }

    static func offRouteAlert(adherence: Adherence) -> CPNavigationAlert {
        // Dismissing the banner is all the OK button has to do; the controller
        // hears about that through `didDismiss`.
        let ok = CPAlertAction(title: String(localized: "OK"), style: .default) { _ in }
        return CPNavigationAlert(
            titleVariants: [offRouteTitle(adherence: adherence)],
            subtitleVariants: [],
            image: UIImage(systemName: "mappin.slash"),
            primaryAction: ok, secondaryAction: nil, duration: 0
        )
    }

    // MARK: Ending the trip

    static func endTripAlert(onEnd: @escaping () -> Void, onKeepGoing: @escaping () -> Void) -> CPAlertTemplate {
        CPAlertTemplate(titleVariants: [TripStatusText.endTripTitle], actions: [
            CPAlertAction(title: TripStatusText.endTrip, style: .destructive) { _ in onEnd() },
            CPAlertAction(title: TripStatusText.keepGoing, style: .cancel) { _ in onKeepGoing() },
        ])
    }

    /// The server did not confirm the end: the phone's three ways on.
    static func notEndedAlert(onTryAgain: @escaping () -> Void, onEndOnThisPhone: @escaping () -> Void,
                              onKeepGoing: @escaping () -> Void) -> CPAlertTemplate {
        CPAlertTemplate(titleVariants: [TripStatusText.notEndedTitle], actions: [
            CPAlertAction(title: TripStatusText.tryAgain, style: .default) { _ in onTryAgain() },
            CPAlertAction(title: TripStatusText.endOnThisPhone, style: .destructive) { _ in onEndOnThisPhone() },
            CPAlertAction(title: TripStatusText.keepGoing, style: .cancel) { _ in onKeepGoing() },
        ])
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

    /// A list template capped at the vehicle's item *and* section limits;
    /// when the item cap cuts anything off, the last row says so.
    static func list(title: String, sections: [(header: String?, items: [CPListItem])], maxItems: Int, maxSections: Int) -> CPListTemplate {
        let cap = max(maxItems, 1)
        let sectionCap = max(maxSections, 1)
        let total = sections.reduce(0) { $0 + $1.items.count }
        let truncated = total > cap
        // The "see more" row costs a row. With room for one row only, spend it
        // on something the driver can actually tap.
        let notice = truncated && cap > 1
        var remaining = notice ? cap - 1 : cap
        var built: [CPListSection] = []
        for section in sections where remaining > 0 && !section.items.isEmpty {
            let items = Array(section.items.prefix(remaining))
            remaining -= items.count
            built.append(CPListSection(items: items, header: section.header, sectionIndexTitle: nil))
        }
        if notice {
            let more = CPListItem(text: String(localized: "Use iPhone to see more"), detailText: nil)
            more.isEnabled = false
            built.append(CPListSection(items: [more]))
        }
        if built.count > sectionCap {
            // The car will not show more sections than that, and dropping the
            // overflow would hide rows the driver needs: fold it into the last
            // section that does get shown, under that section's own header.
            let kept = built.prefix(sectionCap - 1)
            let overflow = built.dropFirst(sectionCap - 1)
            built = Array(kept) + [CPListSection(items: overflow.flatMap(\.items), header: overflow.first?.header, sectionIndexTitle: nil)]
        }
        return CPListTemplate(title: title, sections: built)
    }

    /// Favorites, Recent and All vehicles, as the phone groups them: each
    /// item is the fleet number, with the label under it when it says more.
    static func vehicleSections(_ vehicles: [Vehicle], favorites: Set<String>, recents: [String],
                                onSelect: @escaping (Vehicle) -> Void) -> [(header: String?, items: [CPListItem])] {
        VehicleGroups.of(vehicles, query: "", favorites: favorites, recents: recents).map { group in
            (PickerText.section(group.section), group.tiles.map { tile in
                let item = CPListItem(text: tile.vehicle.id, detailText: tile.label)
                item.handler = { _, done in
                    onSelect(tile.vehicle)
                    done()
                }
                return item
            })
        }
    }

    /// Recent and All routes, as the phone lists them.
    static func routeSections(_ routes: [RouteInfo], recentIDs: [String], onSelect: @escaping (RouteInfo) -> Void) -> [(header: String?, items: [CPListItem])] {
        func item(_ route: RouteInfo) -> CPListItem {
            let item = CPListItem(text: PickerText.name(of: route), detailText: route.longName)
            item.handler = { _, done in
                onSelect(route)
                done()
            }
            return item
        }
        let list = RouteList.of(routes, query: "", recentIDs: recentIDs)
        var sections: [(header: String?, items: [CPListItem])] = []
        if !list.recent.isEmpty {
            sections.append((PickerText.recent, list.recent.map(item)))
        }
        sections.append((PickerText.allRoutes, list.visible.map(item)))
        return sections
    }

    /// The runs from the run just gone, so the run under way or next is
    /// second: the car caps the list from the top, and on a long route the
    /// runs since the morning would otherwise fill it.
    static func tripItems(_ page: RouteTripsPage, now: Date, onSelect: @escaping (TripSummary) -> Void) -> [CPListItem] {
        let highlighted = TripRun.highlighted(in: page.trips, now: now)
        let opening = TripRun.openAt(in: page.trips, now: now).flatMap { id in page.trips.firstIndex { $0.id == id } } ?? 0
        return page.trips[opening...].map { trip in
            var detail = PickerText.stops(trip.firstStop, trip.lastStop)
            if trip.id == highlighted?.id {
                detail = TripStatusText.pair(trip.startsAt <= now ? PickerText.now : PickerText.next, detail)
            }
            let item = CPListItem(text: "\(Formatters.clock(trip.startsAt, timezone: page.timezone)) → \(trip.headsign)", detailText: detail)
            item.handler = { _, done in
                onSelect(trip)
                done()
            }
            return item
        }
    }

    /// The start confirmation, in the phone's words. The alert template has
    /// no message line, so the longest variant carries the message; the car
    /// picks the longest that fits.
    static func startAlert(time: String, route: String, headsign: String, vehicle: String,
                           onStart: @escaping () -> Void, onCancel: @escaping () -> Void) -> CPAlertTemplate {
        let start = CPAlertAction(title: PickerText.startTrip, style: .default) { _ in onStart() }
        let cancel = CPAlertAction(title: PickerText.cancel, style: .cancel) { _ in onCancel() }
        return CPAlertTemplate(titleVariants: [
            PickerText.startTitleWithMessage(time: time, route: route, headsign: headsign, vehicle: vehicle),
            PickerText.startTitle(time: time),
        ], actions: [start, cancel])
    }

    /// A refused start, in the phone's sentence for it and never the
    /// server's. The car offers no fix: that is the phone's job.
    static func startRefusedAlert(_ error: any Error, vehicleID: String, onOK: @escaping () -> Void) -> CPAlertTemplate {
        errorAlert(StartRefusal(error, vehicleID: vehicleID).text, onOK: onOK)
    }

    static func errorAlert(_ message: String, onOK: @escaping () -> Void) -> CPAlertTemplate {
        let ok = CPAlertAction(title: String(localized: "OK"), style: .cancel) { _ in onOK() }
        return CPAlertTemplate(titleVariants: [message], actions: [ok])
    }
}
