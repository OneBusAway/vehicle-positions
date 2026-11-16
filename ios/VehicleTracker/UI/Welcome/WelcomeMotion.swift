import SwiftUI

/// The welcome's springs. Only offset, scale, rotation and opacity are ever
/// animated, on views that have their final place from the first frame, so
/// SwiftUI moves them without running a view's body for a frame. What
/// travels uses the app's own `.smooth`; only small things get a little
/// bounce, and nothing large overshoots.
///
/// Each piece names its own animation with `animation(_:value:)`, on the
/// one value that sets it off, and whatever moves is a `geometryGroup()`.
/// SwiftUI works out a moving view's place at each view that draws, with
/// that view's own animation: without the group, a part with an animation of
/// its own, such as a ring changing colour, would reach the new place on its
/// own curve, ahead of the piece it belongs to.
nonisolated enum WelcomeMotion {
    /// How far a card rises, and how far a screen slides.
    static let rise: CGFloat = 10
    static let slide: CGFloat = 24

    /// A change of screen, as `RootView`'s.
    static let screen = Animation.smooth(duration: 0.35)
    /// The screen that leaves, fading out ahead of the one that comes in.
    static let leave = Animation.smooth(duration: 0.12)
    /// A fade or a change of colour.
    static let fade = Animation.smooth(duration: 0.3)
    /// The sent count, as the trip bar's.
    static let count = Animation.smooth(duration: 0.25)
    /// A check, an icon or a wave: small enough to bounce.
    static let pop = Animation.bouncy(duration: 0.4)

    /// A card landing in its place, with a little bounce at the end.
    static func land(_ duration: TimeInterval) -> Animation {
        .snappy(duration: duration)
    }
}

extension View {
    /// Rises in: from `WelcomeMotion.rise` below and invisible to its place.
    /// The place is the view's from the start; only the offset and the
    /// opacity change.
    func risesIn(_ isIn: Bool, _ animation: Animation?) -> some View {
        geometryGroup()
            .opacity(isIn ? 1 : 0)
            .offset(y: isIn ? 0 : WelcomeMotion.rise)
            .animation(animation, value: isIn)
    }
}

/// The vehicle marker's arrow, as `MapGlyphs.vehicle` draws it on the
/// Tracking map: pointing up, in a box the width of the marker's disc.
nonisolated struct VehicleArrow: Shape {
    func path(in rect: CGRect) -> Path {
        // The map's arrow in its 34 pt disc: tip, right wing, notch, left wing.
        let points: [CGPoint] = [CGPoint(x: 17, y: 6), CGPoint(x: 25.5, y: 26), CGPoint(x: 17, y: 21.5), CGPoint(x: 8.5, y: 26)]
        let scale = min(rect.width, rect.height) / 34
        var path = Path()
        path.addLines(points.map { CGPoint(x: rect.minX + $0.x * scale, y: rect.minY + $0.y * scale) })
        path.closeSubpath()
        return path
    }
}
