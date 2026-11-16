import SwiftUI

/// The welcome: three short screens a driver sees once, after the first
/// sign-in and before the pickers. Each says one thing with a title, a
/// sentence and a picture that plays once and then rests. Nothing in a
/// picture is tapped, nothing moves on by itself, and Skip is on every
/// screen.
///
/// The top row and the button stay where they are; Next and back change
/// only what is between them. At a text size too large for the screen that
/// part scrolls, under the button.
///
/// `onDone` is the caller's, for Skip and for Continue on the last screen.
/// The welcome keeps no record of having been seen: the first sign-in marks
/// it there, and a caller that shows it again need not.
struct WelcomeView: View {
    let onDone: () -> Void

    /// Shorter than this, the iPhone SE, and the screens close up
    /// (DESIGN-iOS §6).
    nonisolated static let compactHeight: CGFloat = 700

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var flow = WelcomeFlow()
    @State private var isCompact = false
    @State private var scroll = ScrollPosition(edge: .top)

    var body: some View {
        GeometryReader { proxy in
            ScrollView {
                ZStack(alignment: .top) {
                    WelcomePage(picture: flow.picture(still: reduceMotion), isCompact: isCompact)
                        .id(flow.showing)
                        .transition(WelcomeStepTransition())
                }
                .padding(.horizontal, 20)
                .padding(.top, 16)
                .frame(minHeight: proxy.size.height, alignment: .top)
            }
            .scrollPosition($scroll)
            .scrollBounceBehavior(.basedOnSize)
            .scrollEdgeEffectStyle(.soft, for: .vertical)
        }
        .environment(\.welcomeIsForward, flow.isForward)
        .accessibilitySortPriority(2)
        .safeAreaBar(edge: .bottom) {
            WideButton(title: flow.step.button, action: advance)
                .contentTransition(.opacity)
                .padding(.horizontal, 20)
                .padding(.top, 20)
                .padding(.bottom, isCompact ? 16 : 8)
                .accessibilitySortPriority(1)
        }
        .safeAreaBar(edge: .top) {
            topRow
        }
        .background(Color(.systemGroupedBackground))
        .onGeometryChange(for: Bool.self) { $0.size.height < Self.compactHeight } action: { isCompact = $0 }
        .onAppear {
            flow.start()
            AccessibilityNotification.ScreenChanged().post()
        }
        .onDisappear { flow.stop() }
    }

    /// Back, the route line and Skip. Back keeps its place on the first
    /// screen, unseen, so the line never shifts. VoiceOver comes here after
    /// the page and the button: the step, then Skip, then back.
    private var topRow: some View {
        HStack(spacing: 0) {
            Button(action: back) {
                Image(systemName: "chevron.left")
                    .font(.body.weight(.semibold))
                    .frame(width: 30, height: 30)
            }
            .buttonBorderShape(.circle)
            .accessibilityLabel(WelcomeText.back)
            .opacity(flow.isFirst ? 0 : 1)
            .disabled(flow.isFirst)
            .accessibilityHidden(flow.isFirst)
            WelcomeRouteLine(step: flow.step, length: isCompact ? 120 : 146)
                .frame(maxWidth: .infinity)
                .accessibilitySortPriority(0.75)
            Button(action: onDone) {
                Text(WelcomeText.skip)
                    .font(.body.weight(.medium))
                    .padding(.horizontal, 4)
                    .frame(minHeight: 30)
            }
            .buttonBorderShape(.capsule)
            .accessibilitySortPriority(0.5)
        }
        .buttonStyle(.glass)
        .padding(.horizontal, 16)
        .frame(minHeight: 44)
    }

    private func advance() {
        if flow.isLast {
            onDone()
        } else {
            move(flow.next)
        }
    }

    private func back() {
        move(flow.back)
    }

    /// One change of screen: the page between the top row and the button
    /// slides under a fade, the vehicle on the route line slides to its
    /// stop, and VoiceOver starts again from the new title. With Reduce
    /// Motion the screen changes at once.
    private func move(_ change: () -> Void) {
        withAnimation(reduceMotion ? nil : WelcomeMotion.screen) {
            change()
            scroll.scrollTo(edge: .top)
        }
        AccessibilityNotification.ScreenChanged().post()
    }
}

/// What changes from one screen to the next: the title, the sentence and
/// the picture. It is drawn from the picture alone, so a page that is
/// leaving stays as it was while it fades.
struct WelcomePage: View {
    let picture: WelcomePicture
    let isCompact: Bool

    @ScaledMetric(relativeTo: .largeTitle) private var compactTitleSize = 30.0

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(picture.step.title)
                .font(isCompact ? .system(size: compactTitleSize, weight: .bold) : .largeTitle.bold())
                .accessibilityAddTraits(.isHeader)
            Text(picture.step.sentence)
                .foregroundStyle(.secondary)
                .padding(.top, 6)
            Group {
                switch picture {
                case .riders(let beat): RidersPicture(beat: beat, isCompact: isCompact)
                case .choices(let beat): ChoicesPicture(beat: beat, isCompact: isCompact)
                case .reporting(let beat): ReportingPicture(beat: beat, isCompact: isCompact)
                }
            }
            .padding(.top, isCompact ? 12 : 20)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
