import SwiftUI

/// The welcome's progress, drawn as a route: three stops on a track, the
/// vehicle on the stop for the screen that shows, and the way it has come in
/// the accent colour. On Next and back the vehicle slides to its stop on the
/// move's spring: only offsets and opacities change, the track never
/// resizes. VoiceOver reads it as one element, "Step 1 of 3".
struct WelcomeRouteLine: View {
    let step: WelcomeStep
    /// Stop to stop: 146 pt, or 120 pt on the smallest iPhone.
    let length: CGFloat

    private static let trackHeight: CGFloat = 6
    private static let stopDiameter: CGFloat = 12
    private static let vehicleDiameter: CGFloat = 26

    private var stops: [WelcomeStep] { WelcomeStep.allCases }

    /// How far along the track the vehicle is, 0 to 1.
    private var progress: CGFloat {
        CGFloat(step.rawValue) / CGFloat(stops.count - 1)
    }

    var body: some View {
        ZStack(alignment: .leading) {
            track
            ForEach(stops, id: \.self) { stop in
                stopMark(reached: stop.rawValue <= step.rawValue)
                    .offset(x: place(of: stop) - Self.stopDiameter / 2)
            }
            vehicle
                .offset(x: progress * length - Self.vehicleDiameter / 2)
        }
        .frame(width: length, height: Self.vehicleDiameter)
        .flipsForRightToLeftLayoutDirection(true)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(step.spokenStep)
    }

    private func place(of stop: WelcomeStep) -> CGFloat {
        CGFloat(stop.rawValue) / CGFloat(stops.count - 1) * length
    }

    /// The track, and over it the stretch behind the vehicle: a full-length
    /// bar that slides out from the leading end, clipped to the track.
    private var track: some View {
        Capsule()
            .fill(Color(.tertiarySystemFill))
            .overlay {
                Capsule()
                    .fill(.accent)
                    .offset(x: (progress - 1) * length)
            }
            .clipShape(Capsule())
            .frame(height: Self.trackHeight)
    }

    private func stopMark(reached: Bool) -> some View {
        Circle()
            .fill(Color(.systemGroupedBackground))
            .strokeBorder(Color(.tertiaryLabel), lineWidth: 2)
            .overlay {
                Circle()
                    .fill(.accent)
                    .opacity(reached ? 1 : 0)
            }
            .frame(width: Self.stopDiameter, height: Self.stopDiameter)
    }

    /// The Tracking map's vehicle marker, small and pointing along the line.
    private var vehicle: some View {
        Circle()
            .fill(.mapVehicleDisc)
            .strokeBorder(.mapVehicleEdge, lineWidth: 3)
            .overlay {
                VehicleArrow()
                    .fill(.mapVehicleArrow)
                    .frame(width: 20, height: 20)
                    .rotationEffect(.degrees(90))
            }
            .frame(width: Self.vehicleDiameter, height: Self.vehicleDiameter)
            .shadow(color: .black.opacity(0.18), radius: 2, y: 1)
    }
}
