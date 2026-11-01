import SwiftUI

/// The full-width action at the foot of a screen before or around a trip,
/// such as Sign in (DESIGN-iOS §2.3): 56 pt tall, prominent glass in the
/// accent colour. While `busyTitle` is set it shows a spinner and those
/// words instead, at full colour: the driver sees it working, not greyed
/// out. Taps then are the caller's to ignore.
struct WideButton: View {
    let title: String
    var busyTitle: String?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                if busyTitle != nil {
                    ProgressView()
                        .tint(.onAccent)
                }
                Text(busyTitle ?? title)
            }
            .font(.headline)
            .foregroundStyle(.onAccent)
            // The glass adds 7 pt above and below its label.
            .frame(maxWidth: .infinity, minHeight: 42)
        }
        .buttonStyle(.glassProminent)
        .accessibilityLabel(busyTitle ?? title)
    }
}
