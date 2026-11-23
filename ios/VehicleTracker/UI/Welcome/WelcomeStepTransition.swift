import SwiftUI

/// Next and back: the title, the sentence and the picture slide a short way
/// under a fade. Next brings the new screen in from the trailing side as the
/// old one leaves towards the leading side; back is the other way round.
///
/// The way is read from the environment when the move happens, not kept in
/// the transition: a screen that leaves was last drawn before the tap, so a
/// direction stored with it would be the move before's.
struct WelcomeStepTransition: Transition {
    func body(content: Content, phase: TransitionPhase) -> some View {
        content.modifier(WelcomeStepSlide(phase: phase))
    }

    /// Where a screen sits in `phase`, as a signed share of the slide:
    /// positive is towards the trailing side. A screen yet to appear is one
    /// slide ahead of the move, and one that has left is one slide behind.
    nonisolated static func shift(_ phase: TransitionPhase, isForward: Bool) -> Double {
        -phase.value * (isForward ? 1 : -1)
    }

    /// Whether a screen in `phase` takes taps: only once it is in place, so
    /// the Language row on a screen sliding in or fading out can't be hit, as
    /// Android's is enabled only on the screen shown.
    nonisolated static func takesTaps(_ phase: TransitionPhase) -> Bool {
        phase.isIdentity
    }
}

private struct WelcomeStepSlide: ViewModifier {
    let phase: TransitionPhase
    @Environment(\.welcomeIsForward) private var isForward
    @Environment(\.layoutDirection) private var layoutDirection

    func body(content: Content) -> some View {
        let towardsTrailing: CGFloat = layoutDirection == .rightToLeft ? -1 : 1
        content
            // The screen slides as one piece, whatever its picture is doing.
            .geometryGroup()
            // A screen that leaves is gone in a third of the move, so its
            // words are never read through the next screen's. With Reduce
            // Motion the move has no animation, and this adds none.
            .transaction { transaction in
                if phase == .didDisappear, transaction.animation != nil {
                    transaction.animation = WelcomeMotion.leave
                }
            } body: { leaving in
                leaving.opacity(phase.isIdentity ? 1 : 0)
            }
            .offset(x: WelcomeStepTransition.shift(phase, isForward: isForward) * WelcomeMotion.slide * towardsTrailing)
            .allowsHitTesting(WelcomeStepTransition.takesTaps(phase))
    }
}

extension EnvironmentValues {
    /// The welcome's last move was Next rather than back.
    @Entry var welcomeIsForward = true
}
