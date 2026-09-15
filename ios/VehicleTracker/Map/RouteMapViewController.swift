import MapKit
import UIKit

/// Draws the trip: the shape in the route colour, its stops, the vehicle
/// pointed along its course, and the projected position while off route.
/// Shared by the phone's tracking screen and the CarPlay window. The map
/// takes no touches of its own: hosts drive it through `pan`, `zoom`,
/// `recentre` and `followsVehicle`.
@MainActor
final class RouteMapViewController: UIViewController, MKMapViewDelegate {
    /// Camera distance while following, metres, before the host zooms.
    static let defaultFollowDistance: CLLocationDistance = 1200
    /// The range `zoom(by:)` may set the follow distance to.
    static let followDistanceRange: ClosedRange<CLLocationDistance> = 200...50_000
    /// How far ahead of the vehicle the camera centres, so the road ahead
    /// fills the screen and the vehicle sits low.
    static let lookAheadMetres = 300.0

    let mapView = MKMapView()

    /// Whether the map takes touches itself (the phone) or is driven only by
    /// its host (CarPlay, where the template owns input). Set before the view
    /// loads.
    var allowsDirectInteraction = false

    /// With no trip drawn, show and follow the phone's own position (the
    /// CarPlay idle screen). Cleared by `setTrip`.
    var showsPhoneLocation = false {
        didSet {
            mapView.showsUserLocation = showsPhoneLocation
            mapView.setUserTrackingMode(showsPhoneLocation ? .follow : .none, animated: true)
        }
    }

    var followsVehicle = true {
        didSet { if followsVehicle { follow(animated: true) } }
    }

    private var trip: TripGeometry?
    private var shape: ShapeGeometry?
    private var lastAdherence: Adherence?
    /// The camera distance `follow()` uses; `zoom(by:)` changes it so a zoom
    /// survives the next fix instead of being undone by it.
    private var followDistance = RouteMapViewController.defaultFollowDistance
    /// Core Location reports -1 for an unknown course. Steering the camera to
    /// north on every such fix spins the map; the last known heading is a far
    /// better guess, so it is kept.
    private var lastCourse = 0.0
    /// How much darker than the route colour the casing under the line is
    /// drawn (spec §6.3).
    static let casingDarkening: CGFloat = 0.35
    private var routeColor = UIColor.systemBlue
    private var polyline: MKPolyline?
    private var polylineRenderer: MKPolylineRenderer?
    /// The same shape, drawn wider and darker underneath, so a bright route
    /// colour still reads as a line over pale streets.
    private var casing: MKPolyline?
    private var casingRenderer: MKPolylineRenderer?
    private var stops: [StopAnnotation] = []
    private let vehicle = VehicleAnnotation()
    private let snapped = SnappedAnnotation()
    private var vehiclePlaced = false
    private var snappedPlaced = false

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
        routeColor = UIColor(hex: trip.route.color) ?? .systemBlue
        let coords = shape.points.map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
        let under = MKPolyline(coordinates: coords, count: coords.count)
        casing = under
        let line = MKPolyline(coordinates: coords, count: coords.count)
        polyline = line
        // Casing first: overlays draw in the order they are added, so the
        // 6 pt line sits centred on the 10 pt one underneath it.
        mapView.addOverlay(under, level: .aboveRoads)
        mapView.addOverlay(line, level: .aboveRoads)
        stops = trip.stops.map(StopAnnotation.init(stop:))
        mapView.addAnnotations(stops)
        mapView.setVisibleMapRect(line.boundingMapRect, edgePadding: UIEdgeInsets(top: 40, left: 40, bottom: 40, right: 40), animated: false)
    }

    /// Moves the vehicle, re-styles the shape and stops, and follows.
    func update(_ adherence: Adherence?) {
        // SwiftUI re-runs `updateUIViewController` for reasons that have
        // nothing to do with the vehicle; re-animating the camera each time
        // makes the map stutter. Only a genuinely new fix moves anything.
        guard adherence != lastAdherence else { return }
        lastAdherence = adherence
        guard let adherence else {
            if vehiclePlaced { mapView.removeAnnotation(vehicle); vehiclePlaced = false }
            if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }
            return
        }
        vehicle.coordinate = CLLocationCoordinate2D(latitude: adherence.fix.latitude, longitude: adherence.fix.longitude)
        if (0...360).contains(adherence.fix.course) { lastCourse = adherence.fix.course }
        vehicle.course = lastCourse
        vehicle.isOnRoute = adherence.isOnRoute
        if !vehiclePlaced { mapView.addAnnotation(vehicle); vehiclePlaced = true }
        refreshVehicleView()

        if adherence.isOnRoute {
            if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }
        } else if let shape {
            let p = shape.point(at: adherence.projection.alongShape)
            snapped.coordinate = CLLocationCoordinate2D(latitude: p.lat, longitude: p.lon)
            if !snappedPlaced { mapView.addAnnotation(snapped); snappedPlaced = true }
        }

        let alpha: CGFloat = adherence.isOnRoute ? 1 : 0.35
        polylineRenderer?.strokeColor = routeColor.withAlphaComponent(alpha)
        polylineRenderer?.setNeedsDisplay()
        casingRenderer?.strokeColor = casingColor.withAlphaComponent(alpha)
        casingRenderer?.setNeedsDisplay()

        for stop in stops where stop.isNext != (stop.stop.id == adherence.nextStop.id && stop.stop.sequence == adherence.nextStop.sequence) {
            stop.isNext.toggle()
            mapView.removeAnnotation(stop)
            mapView.addAnnotation(stop)
        }

        if followsVehicle { follow(animated: true) }
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
        followDistance = min(Self.followDistanceRange.upperBound,
                             max(Self.followDistanceRange.lowerBound, followDistance / factor))
        if followsVehicle, lastAdherence != nil {
            follow(animated: true)
        } else {
            let camera = mapView.camera.copy() as! MKMapCamera
            camera.centerCoordinateDistance = followDistance
            mapView.setCamera(camera, animated: true)
        }
    }

    func recentre() {
        followDistance = Self.defaultFollowDistance
        followsVehicle = true
    }

    private func follow(animated: Bool) {
        guard let adherence = lastAdherence else { return }
        let course = lastCourse
        let here = GeoPoint(adherence.fix.latitude, adherence.fix.longitude)
        let rad = Geo.rad(course)
        let ahead = GeoPoint(
            here.lat + Self.lookAheadMetres * cos(rad) / Geo.metresPerDegree,
            here.lon + Self.lookAheadMetres * sin(rad) / (Geo.metresPerDegree * max(cos(Geo.rad(here.lat)), 1e-6))
        )
        let camera = MKMapCamera(lookingAtCenter: CLLocationCoordinate2D(latitude: ahead.lat, longitude: ahead.lon),
                                 fromDistance: followDistance, pitch: 0, heading: course)
        mapView.setCamera(camera, animated: animated)
    }

    private func refreshVehicleView() {
        guard let view = mapView.view(for: vehicle) else { return }
        let fill = (vehicle.isOnRoute ? routeColor : .systemGray).resolvedColor(with: mapView.traitCollection)
        view.image = MapGlyphs.vehicle(fill: fill)
        let relative = vehicle.course - mapView.camera.heading
        view.transform = CGAffineTransform(rotationAngle: relative * .pi / 180)
    }

    /// Redraws every annotation image against the current traits. The glyphs
    /// bake `UIColor.label` and `.systemBackground` in as they are drawn, so a
    /// light/dark change — the car's own day/night switch included — leaves
    /// stale colours on screen until they are drawn again.
    func refreshGlyphs() {
        for stop in stops {
            guard let view = mapView.view(for: stop) else { continue }
            view.image = stopGlyph(for: stop)
            view.centerOffset = stopCentreOffset(for: view)
        }
        refreshVehicleView()
        if let view = mapView.view(for: snapped) {
            view.image = MapGlyphs.dot(traits: mapView.traitCollection)
        }
    }

    private func stopGlyph(for stop: StopAnnotation) -> UIImage {
        MapGlyphs.stop(diameter: stop.isNext ? 18 : 10, fill: routeColor,
                       label: stop.isNext ? stop.stop.name : nil, traits: mapView.traitCollection)
    }

    /// A labelled stop is drawn as a dot with its name beside it; shifting the
    /// view by half the extra width keeps the dot over the stop itself.
    private func stopCentreOffset(for view: MKAnnotationView) -> CGPoint {
        guard let stop = view.annotation as? StopAnnotation, stop.isNext, let image = view.image else { return .zero }
        return CGPoint(x: (image.size.width - 18) / 2, y: 0)
    }

    // MARK: MKMapViewDelegate

    func mapView(_ mapView: MKMapView, rendererFor overlay: MKOverlay) -> MKOverlayRenderer {
        guard let line = overlay as? MKPolyline else { return MKOverlayRenderer(overlay: overlay) }
        let renderer = MKPolylineRenderer(polyline: line)
        let alpha: CGFloat = lastAdherence?.isOnRoute == false ? 0.35 : 1
        if line === casing {
            renderer.strokeColor = casingColor.withAlphaComponent(alpha)
            renderer.lineWidth = 10
            casingRenderer = renderer
        } else {
            renderer.strokeColor = routeColor.withAlphaComponent(alpha)
            renderer.lineWidth = 6
            polylineRenderer = renderer
        }
        renderer.lineCap = .round
        renderer.lineJoin = .round
        return renderer
    }

    private var casingColor: UIColor { routeColor.darkened(by: Self.casingDarkening) }

    func mapView(_ mapView: MKMapView, viewFor annotation: MKAnnotation) -> MKAnnotationView? {
        switch annotation {
        case let stop as StopAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "stop") ?? MKAnnotationView(annotation: stop, reuseIdentifier: "stop")
            view.annotation = stop
            view.image = stopGlyph(for: stop)
            view.centerOffset = stopCentreOffset(for: view)
            view.displayPriority = stop.isNext ? .required : .defaultLow
            view.zPriority = stop.isNext ? .max : .defaultUnselected
            return view
        case is VehicleAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "vehicle") ?? MKAnnotationView(annotation: annotation, reuseIdentifier: "vehicle")
            view.annotation = annotation
            view.displayPriority = .required
            view.zPriority = .max
            DispatchQueue.main.async { [weak self] in self?.refreshVehicleView() }
            return view
        case is SnappedAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "snapped") ?? MKAnnotationView(annotation: annotation, reuseIdentifier: "snapped")
            view.annotation = annotation
            view.image = MapGlyphs.dot(traits: mapView.traitCollection)
            view.displayPriority = .required
            return view
        default:
            return nil
        }
    }

    func mapView(_ mapView: MKMapView, regionDidChangeAnimated animated: Bool) {
        refreshVehicleView()
    }
}
