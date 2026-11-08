import SwiftUI

/// The warning a stale activity shows once the app has stopped updating it,
/// as OneBusAway iOS's `LiveActivityStaleChrome`: the content dims so it
/// can't pass for live, and this line stays at full opacity, since dimming it
/// too would take it below 2:1.
struct TripActivityStaleRow: View {
    static let contentOpacity = 0.55
    static let symbol = "exclamationmark.triangle.fill"

    let text: String
    let color: Color

    var body: some View {
        Label(text, systemImage: Self.symbol)
            .font(.caption.weight(.semibold))
            .foregroundStyle(color)
            .lineLimit(2)
    }

    /// The warning, then what the activity said when it was last updated.
    static func spoken(_ attributes: TripActivityAttributes, _ state: TripActivityAttributes.ContentState) -> String {
        "\(attributes.staleLine) \(state.spoken)"
    }
}
