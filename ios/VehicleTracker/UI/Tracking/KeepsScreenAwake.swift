import SwiftUI

/// Identifies one mounted `keepsScreenAwakeWhileCharging()` to the
/// coordinator. A class purely so it has an address: leases are keyed by
/// `ObjectIdentifier`, and `@State` gives it the view's lifetime, which is
/// the lease's.
private final class ScreenAwakeLeaseToken {}

/// Takes the lease on appear and returns it on disappear. Neither fires when
/// the app goes to the background with the screen still mounted, so the hold
/// is also dropped on `.background` and taken again on `.active`; `.inactive`
/// (Control Center, a banner) keeps it.
private struct KeepsScreenAwakeWhileCharging: ViewModifier {
    @Environment(ScreenAwakeCoordinator.self) private var coordinator
    @Environment(\.scenePhase) private var scenePhase
    @State private var token = ScreenAwakeLeaseToken()

    private var owner: ObjectIdentifier { ObjectIdentifier(token) }

    func body(content: Content) -> some View {
        content
            .onAppear { coordinator.acquire(owner: owner) }
            .onDisappear { coordinator.release(owner: owner) }
            .onChange(of: scenePhase) { _, phase in
                switch phase {
                case .active: coordinator.acquire(owner: owner)
                case .background: coordinator.release(owner: owner)
                case .inactive: break
                @unknown default: break
                }
            }
    }
}

extension View {
    /// Keeps the display on while this view is on screen and the phone is
    /// charging (DESIGN-iOS §4.7.7).
    func keepsScreenAwakeWhileCharging() -> some View {
        modifier(KeepsScreenAwakeWhileCharging())
    }
}
