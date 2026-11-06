import MapKit
import UIKit

/// Draws the trip: the shape in the route colour over its casing, its stops,
/// the vehicle pointed along its course on its status ring, and the matched
/// position with a dotted line to it while off route. Shared by the phone's
/// tracking screen and the CarPlay window. On the phone the driver's pan and
/// pinch are reported out through `onUserMovedMap` and `onUserTouchEnded`;
/// the car drives it through `pan`, `zoom` and `followsVehicle` alone.
@MainActor
final class RouteMapViewController: UIViewController, MKMapViewDelegate, UIGestureRecognizerDelegate {
    /// Camera distance while following, metres, before a framing or a zoom
    /// changes it.
    static let defaultFollowDistance: CLLocationDistance = 1200
    /// The range the follow distance may be set to.
    static let followDistanceRange: ClosedRange<CLLocationDistance> = 200...50_000
    /// How far ahead of the vehicle the camera centres on the car, which has
    /// no framing to measure: the road ahead fills the screen and the
    /// vehicle sits low.
    static let lookAheadMetres = 300.0
    /// Each fix glides the camera and the vehicle over this long, as
    /// Android's location component does (DESIGN-iOS §2.6).
    static let followDuration: TimeInterval = 0.75
    /// With Reduce Motion on, a move longer than this jumps instead.
    static let jumpDistance = 300.0
    /// Android's widths: the line over a wider casing.
    static let lineWidth: CGFloat = 6
    static let casingWidth: CGFloat = 10
    /// The off-route link: round dots 2.5 across, 8 apart (Android's 2 on 6
    /// at 2.5 wide).
    static let linkWidth: CGFloat = 2.5
    static let linkDashes: [NSNumber] = [0, 8]

    let mapView = MKMapView()

    /// Whether the map takes touches itself (the phone) or is driven only by
    /// its host (CarPlay, where the template owns input).
    private let allowsDirectInteraction: Bool

    /// With no trip drawn, show and follow the phone's own position (the
    /// CarPlay idle screen). Cleared by `setTrip`. Re-arming user tracking
    /// fights the driver's own panning, so only a genuine change acts.
    var showsPhoneLocation = false {
        didSet {
            guard showsPhoneLocation != oldValue else { return }
            mapView.showsUserLocation = showsPhoneLocation
            mapView.setUserTrackingMode(showsPhoneLocation ? .follow : .none, animated: true)
        }
    }

    var followsVehicle = true {
        didSet { if followsVehicle { follow() } }
    }

    /// Points at the bottom of the map the host draws over. MapKit keeps its
    /// own controls, Apple's logo and the Legal link, inside the layout
    /// margins, so they stay in view above the trip sheet.
    var coveredBottom: CGFloat = 0 {
        didSet {
            guard coveredBottom != oldValue else { return }
            mapView.layoutMargins.bottom = coveredBottom
        }
    }

    /// Where the vehicle sits while following, on the phone. Nil on the car,
    /// which keeps a fixed look-ahead and its own zoom buttons.
    var followFraming: FollowFraming? {
        didSet {
            guard followFraming != oldValue else { return }
            if followsVehicle { follow() }
        }
    }

    /// Fired when the driver starts dragging or pinching the map, and when
    /// that touch ends. Phone only.
    var onUserMovedMap: (() -> Void)?
    var onUserTouchEnded: (() -> Void)?

    /// What VoiceOver calls the vehicle's mark: "Vehicle 4317".
    var vehicleName = "" {
        didSet {
            guard vehicleName != oldValue, let view = mapView.view(for: vehicle) else { return }
            view.accessibilityLabel = TripStatusText.spokenVehicleRow(name: vehicleName, status: nil)
        }
    }

    private var trip: TripGeometry?
    private var shape: ShapeGeometry?
    private var lastAdherence: Adherence?
    /// The camera distance `follow()` uses. The first fix of a trip sets it
    /// from the framing; a pinch, or the car's zoom buttons, change it so a
    /// zoom survives the next fix instead of being undone by it.
    private var followDistance = RouteMapViewController.defaultFollowDistance
    /// Core Location reports -1 for an unknown course. Steering the camera to
    /// north on every such fix spins the map; the last known heading is a far
    /// better guess, so it is kept.
    private var lastCourse = 0.0
    /// The ring round the vehicle: the status colour of how it is keeping to
    /// the schedule, so the map says what the card says.
    private var ring = StatusTone.neutral
    private var polyline: MKPolyline?
    private var polylineRenderer: MKPolylineRenderer?
    /// The same shape, drawn wider underneath in ink or white, so the line
    /// reads over the streets whatever its colour.
    private var casing: MKPolyline?
    private var casingRenderer: MKPolylineRenderer?
    /// The dotted line from an off-route vehicle to where it was matched on
    /// the shape, for the driver to find the way back by.
    private var offRouteLink: MKPolyline?
    private var linkRenderer: MKPolylineRenderer?
    private var stops: [StopAnnotation] = []
    private let vehicle = VehicleAnnotation()
    private let snapped = SnappedAnnotation()
    private var vehiclePlaced = false
    private var snappedPlaced = false
    /// The vehicle image and what it was drawn with. The ring and the style
    /// are the drawing's only inputs, so comparing them is what decides
    /// whether a new tone or a trait change needs a redraw, and nothing has
    /// to invalidate this by hand.
    private var vehicleGlyph: (ring: UIColor, style: UIUserInterfaceStyle, image: UIImage)?

    init(allowsDirectInteraction: Bool = false) {
        self.allowsDirectInteraction = allowsDirectInteraction
        super.init(nibName: nil, bundle: nil)
    }

    /// Never loaded from a storyboard: both hosts make one in code.
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        mapView.delegate = self
        mapView.isScrollEnabled = allowsDirectInteraction
        mapView.isZoomEnabled = allowsDirectInteraction
        mapView.isRotateEnabled = false
        mapView.isPitchEnabled = false
        mapView.showsUserLocation = false
        mapView.showsCompass = false
        mapView.pointOfInterestFilter = .excludingAll
        mapView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(mapView)
        if allowsDirectInteraction {
            // Alongside MapKit's own recognisers, which keep the panning and
            // zooming: these only say that the driver has taken the map.
            for recognizer in [UIPanGestureRecognizer(target: self, action: #selector(userTouched)),
                               UIPinchGestureRecognizer(target: self, action: #selector(userTouched))] {
                recognizer.delegate = self
                mapView.addGestureRecognizer(recognizer)
            }
        }
        // The glyphs are bitmaps: light/dark has to redraw them, it cannot
        // re-resolve the colours inside an image that is already drawn.
        registerForTraitChanges([UITraitUserInterfaceStyle.self]) { (controller: RouteMapViewController, _) in
            controller.refreshGlyphs()
        }
        NSLayoutConstraint.activate([
            mapView.topAnchor.constraint(equalTo: view.topAnchor),
            mapView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            mapView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            mapView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
    }

    /// Replaces the drawn trip. Nil clears the map.
    func setTrip(_ trip: TripGeometry?) {
        guard trip?.id != self.trip?.id else { return }
        if trip != nil { showsPhoneLocation = false }
        self.trip = trip
        if let casing { mapView.removeOverlay(casing) }
        if let polyline { mapView.removeOverlay(polyline) }
        removeOffRouteLink()
        mapView.removeAnnotations(stops)
        polyline = nil
        polylineRenderer = nil
        casing = nil
        casingRenderer = nil
        stops = []
        shape = nil
        lastAdherence = nil
        lastCourse = 0
        if vehiclePlaced { mapView.removeAnnotation(vehicle); vehiclePlaced = false }
        if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }

        guard let trip, let shape = ShapeGeometry(points: trip.shapePoints) else { return }
        self.shape = shape
        let coords = shape.points.map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
        let under = MKPolyline(coordinates: coords, count: coords.count)
        casing = under
        let line = MKPolyline(coordinates: coords, count: coords.count)
        polyline = line
        // Casing first: overlays draw in the order they are added, so the
        // line sits centred on the wider one underneath it.
        mapView.addOverlay(under, level: .aboveRoads)
        mapView.addOverlay(line, level: .aboveRoads)
        stops = trip.stops.indices.map { StopAnnotation(stop: trip.stops[$0], isLast: $0 == trip.stops.count - 1) }
        mapView.addAnnotations(stops)
        mapView.setVisibleMapRect(line.boundingMapRect, edgePadding: UIEdgeInsets(top: 40, left: 40, bottom: 40, right: 40), animated: false)
    }

    /// Moves the vehicle, rings it in `ring`, marks the next stop, and
    /// follows.
    func update(_ adherence: Adherence?, ring: StatusTone) {
        // A new tone redraws the ring and nothing else: the camera must not
        // move for a problem line coming or going.
        if ring != self.ring {
            self.ring = ring
            refreshVehicleView()
        }
        // SwiftUI re-runs `updateUIViewController` for reasons that have
        // nothing to do with the vehicle; re-animating the camera each time
        // makes the map stutter. Only a genuinely new fix moves anything.
        guard adherence != lastAdherence else { return }
        let isFirstFix = lastAdherence == nil
        lastAdherence = adherence
        guard let adherence else {
            if vehiclePlaced { mapView.removeAnnotation(vehicle); vehiclePlaced = false }
            if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }
            removeOffRouteLink()
            return
        }
        let coordinate = CLLocationCoordinate2D(latitude: adherence.fix.latitude, longitude: adherence.fix.longitude)
        if (0...360).contains(adherence.fix.course) { lastCourse = adherence.fix.course }
        vehicle.course = lastCourse
        if isFirstFix, let framing = followFraming, let scale = cameraScale() {
            // The first follow of a trip takes the scale that puts the road
            // ahead across the uncovered map; after that the driver's own
            // zoom holds.
            followDistance = Self.clamped(scale.distance * framing.metresPerPoint / scale.metresPerPoint)
        }
        if !vehiclePlaced {
            vehicle.coordinate = coordinate
            mapView.addAnnotation(vehicle)
            vehiclePlaced = true
        }
        refreshVehicleView()

        // Off the route, the matched position shows with a dotted line to it
        // from the vehicle. The route keeps its full colour: that is when the
        // driver needs to find it again.
        if adherence.isOnRoute {
            if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }
            removeOffRouteLink()
        } else if let shape {
            let p = shape.point(at: adherence.projection.alongShape)
            snapped.coordinate = CLLocationCoordinate2D(latitude: p.lat, longitude: p.lon)
            if !snappedPlaced { mapView.addAnnotation(snapped); snappedPlaced = true }
            replaceOffRouteLink(from: coordinate, to: snapped.coordinate)
        }

        for stop in stops where stop.isNext != (stop.stop.id == adherence.nextStop.id && stop.stop.sequence == adherence.nextStop.sequence) {
            stop.isNext.toggle()
            mapView.removeAnnotation(stop)
            mapView.addAnnotation(stop)
        }

        move(to: coordinate, camera: followsVehicle ? followCamera(for: adherence) : nil, first: isFirstFix)
    }

    /// The camera and the vehicle move together, in one animation, so the
    /// vehicle never jitters against the moving map. The first fix of a trip
    /// leaves the whole-route view for the vehicle; with Reduce Motion on, a
    /// long move jumps.
    private func move(to coordinate: CLLocationCoordinate2D, camera: MKMapCamera?, first: Bool) {
        let from = vehicle.coordinate
        let far = Geo.distance(GeoPoint(from.latitude, from.longitude), GeoPoint(coordinate.latitude, coordinate.longitude)) > Self.jumpDistance
        if first || (UIAccessibility.isReduceMotionEnabled && far) {
            vehicle.coordinate = coordinate
            if let camera { mapView.setCamera(camera, animated: first) }
            return
        }
        UIView.animate(withDuration: Self.followDuration, delay: 0, options: [.curveEaseOut, .beginFromCurrentState, .allowUserInteraction]) {
            self.vehicle.coordinate = coordinate
            if let camera { self.mapView.camera = camera }
        }
    }

    private func replaceOffRouteLink(from vehicle: CLLocationCoordinate2D, to matched: CLLocationCoordinate2D) {
        removeOffRouteLink()
        let link = MKPolyline(coordinates: [vehicle, matched], count: 2)
        offRouteLink = link
        mapView.addOverlay(link, level: .aboveLabels)
    }

    private func removeOffRouteLink() {
        guard let offRouteLink else { return }
        mapView.removeOverlay(offRouteLink)
        self.offRouteLink = nil
        linkRenderer = nil
    }

    // MARK: The driver's touches

    @objc private func userTouched(_ recognizer: UIGestureRecognizer) {
        switch recognizer.state {
        case .began:
            // Stop here and now, before the host hears of it: a fix landing
            // in between must not snap the camera back under the finger.
            followsVehicle = false
            onUserMovedMap?()
        case .ended, .cancelled, .failed:
            // The driver's zoom is kept for the next follow, as the car's
            // zoom buttons keep theirs.
            if recognizer is UIPinchGestureRecognizer {
                followDistance = Self.clamped(mapView.camera.centerCoordinateDistance)
            }
            onUserTouchEnded?()
        case .possible, .changed:
            break
        @unknown default:
            break
        }
    }

    /// MapKit's own recognisers keep the panning and the zooming.
    func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer) -> Bool {
        true
    }

    // MARK: Host-driven camera

    func pan(by translation: CGPoint) {
        followsVehicle = false
        let centre = mapView.convert(mapView.centerCoordinate, toPointTo: mapView)
        let moved = CGPoint(x: centre.x - translation.x, y: centre.y - translation.y)
        mapView.setCenter(mapView.convert(moved, toCoordinateFrom: mapView), animated: true)
    }

    func zoom(by factor: Double) {
        guard factor > 0 else { return }
        followDistance = Self.clamped(followDistance / factor)
        if followsVehicle, lastAdherence != nil {
            follow()
        } else {
            let camera = mapView.camera.copy() as! MKMapCamera
            camera.centerCoordinateDistance = followDistance
            mapView.setCamera(camera, animated: true)
        }
    }

    /// Brings the camera back to the vehicle: Re-center, the timed return,
    /// a new framing, or a zoom while following.
    private func follow() {
        guard let adherence = lastAdherence else { return }
        let camera = followCamera(for: adherence)
        UIView.animate(withDuration: Self.followDuration, delay: 0, options: [.curveEaseOut, .beginFromCurrentState, .allowUserInteraction]) {
            self.mapView.camera = camera
        }
    }

    /// Heading-up and flat, centred ahead of the vehicle: by the framing on
    /// the phone, so the vehicle lands a third of the way up the uncovered
    /// map, and by a fixed look-ahead on the car.
    private func followCamera(for adherence: Adherence) -> MKMapCamera {
        let course = lastCourse
        let here = GeoPoint(adherence.fix.latitude, adherence.fix.longitude)
        var ahead = Self.lookAheadMetres
        if let framing = followFraming, let scale = cameraScale() {
            // At zero pitch a point covers ground in proportion to the
            // camera's distance, so the scale at the follow distance follows
            // from the scale now.
            ahead = framing.vehicleOffsetFromCentre * scale.metresPerPoint * followDistance / scale.distance
        }
        let rad = Geo.rad(course)
        let centre = GeoPoint(
            here.lat + ahead * cos(rad) / Geo.metresPerDegree,
            here.lon + ahead * sin(rad) / (Geo.metresPerDegree * max(cos(Geo.rad(here.lat)), 1e-6))
        )
        return MKMapCamera(lookingAtCenter: CLLocationCoordinate2D(latitude: centre.lat, longitude: centre.lon),
                           fromDistance: followDistance, pitch: 0, heading: course)
    }

    /// The camera's distance now and the metres one point covers at it:
    /// measured, because MapKit does not publish its field of view. Nil
    /// before the map has a size to measure on.
    private func cameraScale() -> (distance: CLLocationDistance, metresPerPoint: Double)? {
        let bounds = mapView.bounds
        guard bounds.height >= 200 else { return nil }
        let centre = CGPoint(x: bounds.midX, y: bounds.midY)
        let a = mapView.convert(centre, toCoordinateFrom: mapView)
        let b = mapView.convert(CGPoint(x: centre.x, y: centre.y + 100), toCoordinateFrom: mapView)
        let metresPerPoint = Geo.distance(GeoPoint(a.latitude, a.longitude), GeoPoint(b.latitude, b.longitude)) / 100
        let distance = mapView.camera.centerCoordinateDistance
        guard metresPerPoint > 0, metresPerPoint.isFinite, distance > 0 else { return nil }
        return (distance, metresPerPoint)
    }

    private static func clamped(_ distance: CLLocationDistance) -> CLLocationDistance {
        min(followDistanceRange.upperBound, max(followDistanceRange.lowerBound, distance))
    }

    // MARK: Glyphs and colours

    private var isDark: Bool {
        mapView.traitCollection.userInterfaceStyle == .dark
    }

    /// The route colour and its casing for the map as it is, light or dark:
    /// Android's `RouteLine.kt`, so both apps draw a feed's colour the same.
    private var routeColors: (line: UIColor, casing: UIColor) {
        let line = RouteLineColors.line(hex: trip?.route.color ?? "", dark: isDark)
        return (line.uiColor, RouteLineColors.casing(line: line, dark: isDark).uiColor)
    }

    private func refreshVehicleView() {
        guard let view = mapView.view(for: vehicle) else { return }
        let traits = mapView.traitCollection
        let ringColor = ring.uiText.resolvedColor(with: traits)
        if vehicleGlyph?.ring != ringColor || vehicleGlyph?.style != traits.userInterfaceStyle {
            vehicleGlyph = (ringColor, traits.userInterfaceStyle, MapGlyphs.vehicle(ring: ringColor, traits: traits))
        }
        view.image = vehicleGlyph?.image
        updateVehicleTransform(of: view)
    }

    /// Points the vehicle along its course relative to the camera. The image
    /// says nothing about heading, so a camera move only has to turn it.
    private func updateVehicleTransform(of view: MKAnnotationView) {
        let relative = vehicle.course - mapView.camera.heading
        view.transform = CGAffineTransform(rotationAngle: relative * .pi / 180)
    }

    /// Redraws every annotation image and re-resolves the overlay colours
    /// against the current traits. The glyphs bake their colours in as they
    /// are drawn, and the route is lifted on the dark map, so a light/dark
    /// change, the car's own day/night switch included, leaves stale colours
    /// on screen until they are picked again.
    func refreshGlyphs() {
        for stop in stops {
            guard let view = mapView.view(for: stop) else { continue }
            view.image = stopGlyph(for: stop)
            view.centerOffset = stopCentreOffset(for: view)
        }
        refreshVehicleView()
        if let view = mapView.view(for: snapped) {
            view.image = MapGlyphs.stop(.last, traits: mapView.traitCollection)
        }
        let colors = routeColors
        polylineRenderer?.strokeColor = colors.line
        polylineRenderer?.setNeedsDisplay()
        casingRenderer?.strokeColor = colors.casing
        casingRenderer?.setNeedsDisplay()
        linkRenderer?.strokeColor = UIColor.label.resolvedColor(with: mapView.traitCollection)
        linkRenderer?.setNeedsDisplay()
    }

    private func stopGlyph(for stop: StopAnnotation) -> UIImage {
        let kind: MapGlyphs.Stop = stop.isNext ? .next(label: stop.stop.name) : stop.isLast ? .last : .ordinary
        return MapGlyphs.stop(kind, traits: mapView.traitCollection)
    }

    /// A labelled stop is drawn as its halo with the name beside it; shifting
    /// the view by half the extra width keeps the halo over the stop itself.
    private func stopCentreOffset(for view: MKAnnotationView) -> CGPoint {
        guard let stop = view.annotation as? StopAnnotation, stop.isNext, let image = view.image else { return .zero }
        return CGPoint(x: (image.size.width - MapGlyphs.nextStopHaloDiameter) / 2, y: 0)
    }

    // MARK: MKMapViewDelegate

    func mapView(_ mapView: MKMapView, rendererFor overlay: MKOverlay) -> MKOverlayRenderer {
        guard let line = overlay as? MKPolyline else { return MKOverlayRenderer(overlay: overlay) }
        let renderer = MKPolylineRenderer(polyline: line)
        renderer.lineCap = .round
        renderer.lineJoin = .round
        if line === casing {
            renderer.strokeColor = routeColors.casing
            renderer.lineWidth = Self.casingWidth
            casingRenderer = renderer
        } else if line === offRouteLink {
            renderer.strokeColor = UIColor.label.resolvedColor(with: mapView.traitCollection)
            renderer.lineWidth = Self.linkWidth
            renderer.lineDashPattern = Self.linkDashes
            linkRenderer = renderer
        } else {
            renderer.strokeColor = routeColors.line
            renderer.lineWidth = Self.lineWidth
            polylineRenderer = renderer
        }
        return renderer
    }

    func mapView(_ mapView: MKMapView, viewFor annotation: MKAnnotation) -> MKAnnotationView? {
        switch annotation {
        case let stop as StopAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "stop") ?? MKAnnotationView(annotation: stop, reuseIdentifier: "stop")
            view.annotation = stop
            view.image = stopGlyph(for: stop)
            view.centerOffset = stopCentreOffset(for: view)
            view.displayPriority = stop.isNext || stop.isLast ? .required : .defaultLow
            view.zPriority = stop.isNext ? .max : .defaultUnselected
            view.isAccessibilityElement = true
            view.accessibilityLabel = stop.isNext ? TripStatusText.spokenNextStopMark(name: stop.stop.name) : stop.stop.name
            return view
        case is VehicleAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "vehicle") ?? MKAnnotationView(annotation: annotation, reuseIdentifier: "vehicle")
            view.annotation = annotation
            view.displayPriority = .required
            view.zPriority = .max
            view.isAccessibilityElement = true
            view.accessibilityLabel = TripStatusText.spokenVehicleRow(name: vehicleName, status: nil)
            DispatchQueue.main.async { [weak self] in self?.refreshVehicleView() }
            return view
        case is SnappedAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "snapped") ?? MKAnnotationView(annotation: annotation, reuseIdentifier: "snapped")
            view.annotation = annotation
            view.image = MapGlyphs.stop(.last, traits: mapView.traitCollection)
            view.displayPriority = .required
            return view
        default:
            return nil
        }
    }

    func mapView(_ mapView: MKMapView, regionDidChangeAnimated animated: Bool) {
        if let view = mapView.view(for: vehicle) { updateVehicleTransform(of: view) }
    }
}
