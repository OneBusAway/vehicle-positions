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
        content.modifier(WelcomeStepSlide(phase: phase, progress: phase.value))
    }

    /// Where a screen sits in `phase`, as a signed share of the slide:
    /// positive is towards the trailing side. A screen yet to appear is one
    /// slide ahead of the move, and one that has left is one slide behind.
    nonisolated static func shift(_ phase: TransitionPhase, isForward: Bool) -> Double {
        -phase.value * (isForward ? 1 : -1)
    }

    /// Whether a screen takes taps: only at rest in its place, so the
    /// Language row on a screen sliding in or fading out can't be hit, as
    /// Android's is enabled only on the screen shown. The phase alone can't
    /// tell: a screen coming in is at `.identity` from the first frame of
    /// its move, and one leaving is still in place on that frame.
    nonisolated static func takesTaps(_ phase: TransitionPhase, progress: Double) -> Bool {
        phase.isIdentity && progress == 0
    }
}

/// The slide, and how far along it a screen is: `progress` is `phase.value`
/// as the move's animation carries it there frame by frame, from -1 (yet to
/// appear) through 0 (in place) to 1 (gone). With Reduce Motion the move
/// has no animation and it gets there at once.
struct WelcomeStepSlide: ViewModifier, Animatable {
    let phase: TransitionPhase
    var progress: Double
    @Environment(\.welcomeIsForward) private var isForward
    @Environment(\.layoutDirection) private var layoutDirection

    nonisolated var animatableData: Double {
        get { progress }
        set { progress = newValue }
    }

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
            .allowsHitTesting(WelcomeStepTransition.takesTaps(phase, progress: progress))
    }
}

extension EnvironmentValues {
    /// The welcome's last move was Next rather than back.
    @Entry var welcomeIsForward = true
}
