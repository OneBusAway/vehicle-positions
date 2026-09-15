import SwiftUI

/// The shared map inside SwiftUI. The controller keeps its own state; this
/// wrapper only pushes the trip and the latest adherence into it.
struct RouteMapView: UIViewControllerRepresentable {
    var trip: TripGeometry?
    var adherence: Adherence?

    func makeUIViewController(context: Context) -> RouteMapViewController {
        let controller = RouteMapViewController()
        controller.allowsDirectInteraction = true
        return controller
    }

    func updateUIViewController(_ controller: RouteMapViewController, context: Context) {
        controller.setTrip(trip)
        controller.update(adherence)
    }
}
