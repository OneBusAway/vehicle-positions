import SwiftUI

/// Screen 2's picture: the three choices before a trip as three cards that
/// fill in one after another, then what they lead to, the green "Riders see
/// you" with the reports counting up, and a picture of End trip. Every piece
/// is in its place from the first frame and comes in by offset and opacity.
/// None of it is a control: VoiceOver reads each card as one sentence and
/// does not read the count.
struct ChoicesPicture: View {
    let beat: ChoicesBeat
    let isCompact: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// The cards come in 120 ms apart, on one beat.
    private static let stagger = 0.12

    var body: some View {
        VStack(spacing: isCompact ? 6 : 10) {
            ForEach(Choice.allCases, id: \.self) { choice in
                ChoiceCard(choice: choice, isChosen: beat.choicesMade > choice.rawValue, isCompact: isCompact)
                    .risesIn(beat >= .cardsRise, motion(WelcomeMotion.land(0.35).delay(Self.stagger * Double(choice.rawValue))))
            }
            RidersSeeYouCard(reportsSent: beat.reportsSent)
                .risesIn(beat >= .ridersSeeYou, motion(WelcomeMotion.land(0.35)))
            WhenYouFinishRow()
                .risesIn(beat >= .rest, motion(WelcomeMotion.land(0.35)))
        }
    }

    private func motion(_ animation: Animation) -> Animation? {
        reduceMotion ? nil : animation
    }
}

/// What a driver chooses before a trip, in the pickers' order and words.
nonisolated enum Choice: Int, CaseIterable {
    case vehicle, route, run

    var title: String {
        switch self {
        case .vehicle: PickerText.vehiclesTitle
        case .route: PickerText.routesTitle
        case .run: PickerText.runsTitle
        }
    }

    var hint: String {
        switch self {
        case .vehicle: WelcomeText.vehicleHint
        case .route: WelcomeText.routeHint
        case .run: WelcomeText.runHint
        }
    }

    var symbol: String {
        switch self {
        case .vehicle: "bus.fill"
        case .route: "point.topleft.down.to.point.bottomright.curvepath"
        case .run: "clock"
        }
    }

    var spoken: String {
        WelcomeText.spoken(title: title, detail: hint)
    }
}

/// One choice. Empty, it is an outline with a grey symbol; chosen, it fills,
/// its symbol takes the accent and a check grows in. Both looks are drawn
/// from the start and cross-fade, so nothing changes size.
private struct ChoiceCard: View {
    let choice: Choice
    let isChosen: Bool
    let isCompact: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 22, style: .continuous)
        HStack(spacing: 16) {
            holder
            VStack(alignment: .leading, spacing: 2) {
                Text(choice.title)
                    .font(.headline)
                Text(choice.hint)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "checkmark")
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(.onAccent)
                .frame(width: 26, height: 26)
                .background(.accent, in: Circle())
                .scaleEffect(isChosen ? 1 : 0.5)
                .opacity(isChosen ? 1 : 0)
                .animation(motion(WelcomeMotion.pop), value: isChosen)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, isCompact ? 6 : 10)
        .frame(minHeight: isCompact ? 54 : 68)
        .background {
            shape.strokeBorder(Color(.separator), lineWidth: 1)
            shape.fill(Color(.secondarySystemGroupedBackground))
                .opacity(isChosen ? 1 : 0)
                .animation(motion(WelcomeMotion.fade), value: isChosen)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(choice.spoken)
    }

    /// The symbol in its holder: a rounded square for the vehicle and the
    /// route, a circle for the run, as the design draws them.
    private var holder: some View {
        let side: CGFloat = isCompact ? 38 : 44
        let shape = choice == .run ? AnyShape(Circle()) : AnyShape(RoundedRectangle(cornerRadius: 13, style: .continuous))
        return ZStack {
            symbol(.secondary)
                .background(Color(.tertiarySystemFill), in: shape)
            symbol(.onAccentContainer)
                .background(.accentContainer, in: shape)
                .opacity(isChosen ? 1 : 0)
                .animation(motion(WelcomeMotion.fade), value: isChosen)
        }
        .frame(width: side, height: side)
    }

    private func symbol(_ style: some ShapeStyle) -> some View {
        Image(systemName: choice.symbol)
            .font(.system(size: 20, weight: .semibold))
            .foregroundStyle(style)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func motion(_ animation: Animation) -> Animation? {
        reduceMotion ? nil : animation
    }
}

/// What the three choices lead to, drawn as the status card is: its check in
/// a disc on the on-time fill, with the trip bar's reporting symbol and sent
/// count at the end.
private struct RidersSeeYouCard: View {
    let reportsSent: Int

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        // "1 sent" from the start, so the count never climbs from nothing as
        // the card rises.
        let sent = max(reportsSent, 1)
        HStack(spacing: 12) {
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 24, weight: .semibold))
                .symbolRenderingMode(.hierarchical)
                .frame(width: 44, height: 44)
                .background(.white.opacity(0.18), in: Circle())
            Text(WelcomeText.ridersSeeYou)
                .font(.title3.weight(.semibold))
                .frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 6) {
                Image(systemName: "checkmark.icloud.fill")
                Text(TripStatusText.sent(sent))
                    .monospacedDigit()
                    .contentTransition(.numericText(value: Double(sent)))
                    .animation(reduceMotion ? nil : WelcomeMotion.count, value: sent)
            }
            .font(.subheadline.weight(.semibold))
        }
        .foregroundStyle(.onStatusFill)
        .padding(.leading, 12)
        .padding(.trailing, 16)
        .padding(.vertical, 10)
        .frame(minHeight: 68)
        .background(.statusOnTimeFill, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(WelcomeText.spokenRidersSeeYou)
    }
}

/// "When you finish", beside the trip bar's End trip as it looks there. It
/// is a picture of the button, not a button: it does nothing, and VoiceOver
/// reads the row as one sentence.
private struct WhenYouFinishRow: View {
    var body: some View {
        HStack(spacing: 12) {
            Text(WelcomeText.whenYouFinish)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
            Label(TripStatusText.endTrip, systemImage: "stop.circle")
                .font(.headline)
                .padding(.horizontal, 18)
                .frame(minHeight: 52)
                .glassEffect(.regular, in: .capsule)
        }
        .padding(.leading, 10)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(WelcomeText.spokenWhenYouFinish)
    }
}
