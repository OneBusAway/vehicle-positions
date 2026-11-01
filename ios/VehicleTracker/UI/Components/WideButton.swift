import SwiftUI

/// The full-width action at the foot of a screen before or around a trip,
/// such as Sign in (DESIGN-iOS §2.3): 56 pt tall, prominent glass in the
/// accent colour, or plain glass for the second choice. While `busyTitle` is
/// set it shows a spinner and those words instead, at full colour: the
/// driver sees it working, not greyed out. Taps then are the caller's to
/// ignore.
struct WideButton: View {
    enum Style {
        case prominent, plain
    }

    let title: String
    var busyTitle: String?
    var style = Style.prominent
    let action: () -> Void

    var body: some View {
        switch style {
        case .prominent: button.buttonStyle(.glassProminent)
        case .plain: button.buttonStyle(.glass)
        }
    }

    private var button: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                if busyTitle != nil {
                    ProgressView()
                        .tint(ink)
                }
                Text(busyTitle ?? title)
            }
            .font(.headline)
            .foregroundStyle(ink)
            // The glass adds 7 pt above and below its label.
            .frame(maxWidth: .infinity, minHeight: 42)
        }
        .accessibilityLabel(busyTitle ?? title)
    }

    private var ink: Color {
        style == .prominent ? .onAccent : .primary
    }
}
