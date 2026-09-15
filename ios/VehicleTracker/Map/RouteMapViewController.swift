import MapKit
import UIKit

/// Draws the trip: the shape in the route colour, its stops, the vehicle
/// pointed along its course, and the projected position while off route.
/// Shared by the phone's tracking screen and the CarPlay window. The map
/// takes no touches of its own: hosts drive it through `pan`, `zoom`,
/// `recentre` and `followsVehicle`.
@MainActor
final class RouteMapViewController: UIViewController, MKMapViewDelegate {
    /// Camera distance while following, metres.
    static let followDistance: CLLocationDistance = 1200
    /// How far ahead of the vehicle the camera centres, so the road ahead
    /// fills the screen and the vehicle sits low.
    static let lookAheadMetres = 300.0

    let mapView = MKMapView()

    /// Whether the map takes touches itself (the phone) or is driven only by
    /// its host (CarPlay, where the template owns input). Set before the view
    /// loads.
    var allowsDirectInteraction = false

    var followsVehicle = true {
        didSet { if followsVehicle { follow(animated: true) } }
    }

    private var trip: TripGeometry?
    private var shape: ShapeGeometry?
    private var adherence: Adherence?
    private var routeColor = UIColor.systemBlue
    private var polyline: MKPolyline?
    private var polylineRenderer: MKPolylineRenderer?
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
        self.trip = trip
        if let polyline { mapView.removeOverlay(polyline) }
        mapView.removeAnnotations(stops)
        polyline = nil
        polylineRenderer = nil
        stops = []
        shape = nil
        adherence = nil

        guard let trip, let shape = ShapeGeometry(points: trip.shapePoints) else { return }
        self.shape = shape
        routeColor = UIColor(hex: trip.route.color) ?? .systemBlue
        let coords = shape.points.map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
        let line = MKPolyline(coordinates: coords, count: coords.count)
        polyline = line
        mapView.addOverlay(line, level: .aboveRoads)
        stops = trip.stops.map(StopAnnotation.init(stop:))
        mapView.addAnnotations(stops)
        mapView.setVisibleMapRect(line.boundingMapRect, edgePadding: UIEdgeInsets(top: 40, left: 40, bottom: 40, right: 40), animated: false)
    }

    /// Moves the vehicle, re-styles the shape and stops, and follows.
    func update(_ adherence: Adherence?) {
        self.adherence = adherence
        guard let adherence else {
            if vehiclePlaced { mapView.removeAnnotation(vehicle); vehiclePlaced = false }
            if snappedPlaced { mapView.removeAnnotation(snapped); snappedPlaced = false }
            return
        }
        vehicle.coordinate = CLLocationCoordinate2D(latitude: adherence.fix.latitude, longitude: adherence.fix.longitude)
        vehicle.course = adherence.fix.course
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

        polylineRenderer?.strokeColor = routeColor.withAlphaComponent(adherence.isOnRoute ? 1 : 0.35)
        polylineRenderer?.setNeedsDisplay()

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
        let camera = mapView.camera.copy() as! MKMapCamera
        camera.centerCoordinateDistance = max(200, min(50_000, camera.centerCoordinateDistance / factor))
        mapView.setCamera(camera, animated: true)
    }

    func recentre() {
        followsVehicle = true
    }

    private func follow(animated: Bool) {
        guard let adherence else { return }
        let course = adherence.fix.course >= 0 ? adherence.fix.course : 0
        let here = GeoPoint(adherence.fix.latitude, adherence.fix.longitude)
        let rad = Geo.rad(course)
        let ahead = GeoPoint(
            here.lat + Self.lookAheadMetres * cos(rad) / Geo.metresPerDegree,
            here.lon + Self.lookAheadMetres * sin(rad) / (Geo.metresPerDegree * max(cos(Geo.rad(here.lat)), 1e-6))
        )
        let camera = MKMapCamera(lookingAtCenter: CLLocationCoordinate2D(latitude: ahead.lat, longitude: ahead.lon),
                                 fromDistance: Self.followDistance, pitch: 0, heading: course)
        mapView.setCamera(camera, animated: animated)
    }

    private func refreshVehicleView() {
        guard let view = mapView.view(for: vehicle) else { return }
        view.image = MapGlyphs.vehicle(fill: vehicle.isOnRoute ? routeColor : .systemGray)
        let relative = (vehicle.course >= 0 ? vehicle.course : 0) - mapView.camera.heading
        view.transform = CGAffineTransform(rotationAngle: relative * .pi / 180)
    }

    // MARK: MKMapViewDelegate

    func mapView(_ mapView: MKMapView, rendererFor overlay: MKOverlay) -> MKOverlayRenderer {
        guard let line = overlay as? MKPolyline else { return MKOverlayRenderer(overlay: overlay) }
        let renderer = MKPolylineRenderer(polyline: line)
        renderer.strokeColor = routeColor.withAlphaComponent(adherence?.isOnRoute == false ? 0.35 : 1)
        renderer.lineWidth = 6
        renderer.lineCap = .round
        renderer.lineJoin = .round
        polylineRenderer = renderer
        return renderer
    }

    func mapView(_ mapView: MKMapView, viewFor annotation: MKAnnotation) -> MKAnnotationView? {
        switch annotation {
        case let stop as StopAnnotation:
            let view = mapView.dequeueReusableAnnotationView(withIdentifier: "stop") ?? MKAnnotationView(annotation: stop, reuseIdentifier: "stop")
            view.annotation = stop
            view.image = MapGlyphs.stop(diameter: stop.isNext ? 18 : 10, fill: routeColor, label: stop.isNext ? stop.stop.name : nil)
            view.centerOffset = CGPoint(x: stop.isNext ? (view.image!.size.width - 18) / 2 : 0, y: 0)
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
            view.image = MapGlyphs.dot()
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
