import SwiftUI

/// Over the map once the driver has dragged it away from the vehicle: a
/// glass capsule, as OneBusAway iOS's map controls, that follows again.
struct RecenterButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label(TripStatusText.recenter, systemImage: "location.fill")
                .font(.headline)
                .padding(.horizontal, 6)
                .frame(minHeight: 28)
        }
        .buttonStyle(.glass)
        .buttonBorderShape(.capsule)
        .accessibilityLabel(TripStatusText.recenterAccessibilityLabel)
    }
}
