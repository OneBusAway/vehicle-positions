import SwiftUI

/// The shared map inside SwiftUI. The controller keeps its own state; this
/// wrapper only pushes the trip and the latest adherence into it.
struct RouteMapView: UIViewControllerRepresentable {
    var trip: TripGeometry?
    var adherence: Adherence?
    /// The status colour of the ring round the vehicle.
    var ring: StatusTone = .neutral
    /// Points at the map's bottom that the trip sheet covers, so Apple's logo
    /// and the Legal link stay in view above it.
    var coveredBottom: CGFloat = 0

    func makeUIViewController(context: Context) -> RouteMapViewController {
        RouteMapViewController(allowsDirectInteraction: true)
    }

    func updateUIViewController(_ controller: RouteMapViewController, context: Context) {
        controller.setTrip(trip)
        controller.update(adherence, ring: ring)
        controller.coveredBottom = coveredBottom
    }
}
