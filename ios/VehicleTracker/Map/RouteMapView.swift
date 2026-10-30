import SwiftUI

/// The shared map inside SwiftUI. The controller keeps its own state; this
/// wrapper pushes the trip, the latest adherence and the framing in, with
/// guards so an unrelated SwiftUI update never re-animates the camera, and
/// hands the driver's touches out to `MapFollowing`.
struct RouteMapView: UIViewControllerRepresentable {
    var trip: TripGeometry?
    var adherence: Adherence?
    /// The status colour of the ring round the vehicle.
    var ring: StatusTone = .neutral
    /// What VoiceOver calls the vehicle's mark.
    var vehicleName = ""
    /// Points at the map's bottom that the trip sheet covers, so Apple's logo
    /// and the Legal link stay in view above it.
    var coveredBottom: CGFloat = 0
    /// Where the vehicle sits while following.
    var framing: FollowFraming?
    var following: MapFollowing

    func makeCoordinator() -> Coordinator {
        Coordinator(following: following)
    }

    func makeUIViewController(context: Context) -> RouteMapViewController {
        let controller = RouteMapViewController(allowsDirectInteraction: true)
        let coordinator = context.coordinator
        controller.onUserMovedMap = { [weak coordinator] in coordinator?.following.userMovedMap() }
        controller.onUserTouchEnded = { [weak coordinator] in coordinator?.following.userTouchEnded() }
        return controller
    }

    func updateUIViewController(_ controller: RouteMapViewController, context: Context) {
        controller.setTrip(trip)
        if controller.followFraming != framing { controller.followFraming = framing }
        controller.coveredBottom = coveredBottom
        if controller.followsVehicle != following.isFollowing { controller.followsVehicle = following.isFollowing }
        controller.vehicleName = vehicleName
        controller.update(adherence, ring: ring)
    }

    /// Holds what the controller's closures reach for, so they capture it
    /// weakly rather than the view value.
    @MainActor
    final class Coordinator {
        let following: MapFollowing

        init(following: MapFollowing) {
            self.following = following
        }
    }
}
