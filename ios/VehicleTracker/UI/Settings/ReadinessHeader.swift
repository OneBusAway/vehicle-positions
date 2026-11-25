import SwiftUI

/// The top of Settings: whether this phone can report a trip, and the line
/// that says why. It goes behind the navigation bar. As the page scrolls it
/// folds to its bar with the line as its lower edge: the words go up under
/// the bar, and nothing is resized, and no body run, for a frame of it. A tap
/// on it, folded or not, opens Permission check.
struct ReadinessHeader: View {
    let readiness: Readiness
    @Binding var fold: HeaderFold
    /// What the button does, while something the driver can put right stops a
    /// trip.
    let onFix: (Readiness.Fix) -> Void
    let openCheck: () -> Void
    /// Called with what the header shows, at first and as it changes.
    let onShow: (Readiness) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.dynamicTypeSize) private var typeSize
    /// What it shows: `readiness`, a moment late where the driver can see it
    /// change.
    @State private var shown: Readiness
    /// Where the vehicle is, as `Readiness.vehicleAt` counts.
    @State private var position: Double
    /// Bumped as the vehicle gets to the riders, which lifts the rider.
    @State private var arrivals = 0
    @State private var width: CGFloat = 0

    /// The header starts this far up into the bar.
    nonisolated static let intoBar: CGFloat = 4
    /// What is left under the bar once the header has folded: enough for the
    /// vehicle to clear the back button.
    private static let foldedEdge: CGFloat = 10
    /// From the words down to the header's lower edge, and what the stops'
    /// symbols add to it.
    private static let lineZone: CGFloat = 42
    private static let symbolZone: CGFloat = 28
    /// How far the title is scrolled before it has faded.
    private static let titleFade: CGFloat = 30
    /// Enough to fill the screen above it when the page is pulled down.
    private static let above: CGFloat = 1000

    init(readiness: Readiness, fold: Binding<HeaderFold>, onFix: @escaping (Readiness.Fix) -> Void,
         openCheck: @escaping () -> Void, onShow: @escaping (Readiness) -> Void) {
        self.readiness = readiness
        _fold = fold
        self.onFix = onFix
        self.openCheck = openCheck
        self.onShow = onShow
        _shown = State(initialValue: readiness)
        _position = State(initialValue: Double(readiness.vehicleAt))
    }

    private var geometry: LineGeometry { LineGeometry(width: width, stops: shown.stops.count) }

    /// From 130% text up the stops' symbols give way to the words.
    private var showsSymbols: Bool {
        shown.stops.contains(.missing) && typeSize < .xxxLarge && geometry.hasRoomForSymbols
    }

    private var block: Color {
        switch shown.state {
        case .ready: .accentContainer
        case .notAsked: Color(.systemGray5)
        case .refused, .restricted, .preciseOff, .servicesOff: .attentionContainer
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            words
            Color.clear.frame(height: Self.lineZone + (showsSymbols ? Self.symbolZone : 0))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background {
            block.padding(.top, -Self.above)
        }
        .overlay(alignment: .top) { strip }
        .overlay(alignment: .bottomLeading) {
            ReadinessLine(readiness: shown, geometry: geometry, position: position, arrivals: arrivals,
                          content: shown.ink, showsSymbols: showsSymbols, fold: fold)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
        .contentShape(.rect)
        .onTapGesture(perform: openCheck)
        .visualEffect { [fold] effect, proxy in effect.offset(y: fold.pastFold(proxy.headerScroll)) }
        .coordinateSpace(.named(GeometryProxy.headerSpace))
        .onGeometryChange(for: CGSize.self) { $0.size } action: { size in
            width = size.width
            fold.distance = max(size.height - Self.intoBar - Self.foldedEdge, 0)
        }
        .padding(.top, -Self.intoBar)
        .task(id: readiness) { await show(readiness) }
        .onChange(of: shown, initial: true) { onShow(shown) }
    }

    /// The words go up under it, and the button with them. Where it is, a tap
    /// is the header's and not theirs.
    private var strip: some View {
        block
            .padding(.top, -Self.above)
            .frame(height: Self.intoBar)
            .visualEffect { [fold] effect, proxy in effect.offset(y: min(max(proxy.headerScroll, 0), fold.distance)) }
            .accessibilityHidden(true)
    }

    private var words: some View {
        VStack(alignment: .leading, spacing: 14) {
            ZStack(alignment: .topLeading) {
                ReadinessWordsView(words: ReadinessWords(shown), isNarrow: geometry.isNarrow, titleFade: Self.titleFade, fold: fold)
                    .id(ReadinessWords(shown))
                    .transition(.opacity)
            }
            .accessibilityElement(children: .combine)
            .accessibilityAddTraits(.isButton)
            .accessibilityHint(PermissionCheckText.title)
            .accessibilityAction(.default, openCheck)
            if let fix = shown.headerFix {
                Button(PermissionCheckText.name(of: fix)) { onFix(fix) }
                    .buttonStyle(InkButtonStyle())
                    .visualEffect { [fold] effect, proxy in effect.opacity(fold.wordsOpacity(proxy.headerScroll)) }
                    .transition(.opacity)
            }
        }
        .foregroundStyle(shown.ink)
        .padding(.horizontal, 20)
    }

    /// Where the line changes, the driver is back from Settings and is shown
    /// the old state first, so the change can be seen: then the words and
    /// colours fade and the vehicle drives. Cut off at any moment, it goes on
    /// from where it is. With Reduce Motion it is there at once.
    private func show(_ new: Readiness) async {
        guard new != shown || position != Double(new.vehicleAt) else { return }
        let lineChanges = new.vehicleAt != shown.vehicleAt || new.stops != shown.stops
        guard !reduceMotion else {
            shown = new
            position = Double(new.vehicleAt)
            if lineChanges { announce(new) }
            return
        }
        do {
            if lineChanges { try await Task.sleep(for: ReadinessMotion.wait) }
            withAnimation(ReadinessMotion.change) { shown = new }
            if lineChanges { announce(new) }
            guard position != Double(new.vehicleAt) else { return }
            // In an update of its own, or the header's change would take the
            // drive's spring.
            try await Task.sleep(for: ReadinessMotion.waitToDrive)
        } catch {
            return
        }
        let drivesOn = new.canReport && position < Double(new.vehicleAt)
        withAnimation(ReadinessMotion.drive) {
            position = Double(new.vehicleAt)
        } completion: {
            if drivesOn { arrivals += 1 }
        }
    }

    private func announce(_ readiness: Readiness) {
        let words = ReadinessWords(readiness)
        AccessibilityNotification.Announcement("\(words.title). \(words.sentence)").post()
    }
}

/// The header's springs, the app's own.
nonisolated enum ReadinessMotion {
    /// How long a change waits before the header shows it.
    static let wait: Duration = .milliseconds(250)
    static let change = Animation.smooth(duration: 0.25)
    /// How long after that the vehicle leaves.
    static let waitToDrive: Duration = .milliseconds(50)
    /// The vehicle's drive along the line, which the driver is meant to
    /// follow.
    static let drive = Animation.smooth(duration: 0.6)
    /// How far the rider is lifted as the vehicle gets to them.
    static let lift: CGFloat = -4
    static let lifting = Animation.smooth(duration: 0.12)
    static let settling = Animation.smooth(duration: 0.3)
}

extension Readiness {
    /// The words on the header's block.
    var ink: Color { canReport ? .onAccentContainer : .primary }
}

extension GeometryProxy {
    nonisolated static let headerSpace = "settings-header"

    /// How far the page has scrolled, from anywhere in the header: how far
    /// the header, before its fold holds it, is above where it starts.
    nonisolated var headerScroll: CGFloat {
        frame(in: .named(Self.headerSpace)).minY - frame(in: .scrollView(axis: .vertical)).minY - ReadinessHeader.intoBar
    }
}

nonisolated extension HeaderFold {
    /// The sentence and the button, gone before the header has folded.
    func wordsOpacity(_ scroll: CGFloat) -> CGFloat {
        1 - Self.within(fraction(scroll), 0.3, 0.8)
    }

    /// The stops' symbols.
    func symbolsOpacity(_ scroll: CGFloat) -> CGFloat {
        1 - Self.within(fraction(scroll), 0.3, 0.7)
    }

    /// Whether the bar says under its title what the header did.
    func showsBarLine(_ scroll: CGFloat) -> Bool {
        fraction(scroll) >= 0.8
    }

    /// The vehicle, smaller by the time the header has folded.
    func vehicleScale(_ scroll: CGFloat) -> CGFloat {
        1 - 0.35 * fraction(scroll)
    }
}

/// The state, and one sentence on it.
private struct ReadinessWordsView: View {
    let words: ReadinessWords
    let isNarrow: Bool
    let titleFade: CGFloat
    let fold: HeaderFold

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(words.title)
                .font((isNarrow ? Font.title : .largeTitle).bold())
                .visualEffect { [titleFade] effect, proxy in
                    effect.opacity(1 - HeaderFold.within(proxy.headerScroll, 0, titleFade))
                }
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                if let symbol = words.symbol {
                    Image(systemName: symbol)
                        .accessibilityHidden(true)
                }
                Text(words.sentence)
            }
            .visualEffect { [fold] effect, proxy in effect.opacity(fold.wordsOpacity(proxy.headerScroll)) }
        }
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .combine)
    }
}

/// Ink on the block, so it stands out on each of its colours.
private struct InkButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.headline)
            .foregroundStyle(Color(.systemBackground))
            .padding(.horizontal, 24)
            .padding(.vertical, 10)
            .frame(minHeight: 50)
            .background(Color(.label), in: .capsule)
            .opacity(configuration.isPressed ? 0.7 : 1)
    }
}
