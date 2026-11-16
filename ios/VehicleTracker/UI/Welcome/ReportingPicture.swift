import SwiftUI

/// Screen 3's picture: the three things a trip depends on, as rows of one
/// grouped card, then what the driver sees if reporting stops. It asks for
/// nothing and has no switch; the real asks stay on Before you start. The
/// rows come in one after another, and each row's symbol makes one small
/// move as the next row arrives.
struct ReportingPicture: View {
    let beat: ReportingBeat
    let isCompact: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(spacing: 0) {
                ForEach(Need.allCases, id: \.self) { need in
                    NeedRow(need: need, hasMoved: beat.iconsMoved > need.rawValue, isCompact: isCompact)
                        .risesIn(beat.rowsIn > need.rawValue, motion(WelcomeMotion.land(0.4)))
                }
            }
            Text(WelcomeText.ifReportingStops)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .padding(.leading, 20)
                .padding(.top, 18)
                .padding(.bottom, 10)
                .accessibilityAddTraits(.isHeader)
                .opacity(beat >= .label ? 1 : 0)
                .animation(motion(.smooth(duration: 0.4)), value: beat >= .label)
            StoppedCard()
                .risesIn(beat >= .rest, motion(WelcomeMotion.land(0.4)))
        }
    }

    private func motion(_ animation: Animation) -> Animation? {
        reduceMotion ? nil : animation
    }
}

/// What a trip needs from the driver, in iOS's terms: location while the app
/// is in use and never Always, and an app that stops reporting when closed.
nonisolated enum Need: Int, CaseIterable {
    case location, charge, appOpen

    var title: String {
        switch self {
        case .location: WelcomeText.allowLocation
        case .charge: WelcomeText.keepOnCharge
        case .appOpen: WelcomeText.leaveAppOpen
        }
    }

    var reason: String {
        switch self {
        case .location: WelcomeText.allowLocationReason
        case .charge: WelcomeText.keepOnChargeReason
        case .appOpen: WelcomeText.leaveAppOpenReason
        }
    }

    var symbol: String {
        switch self {
        case .location: "location.fill"
        case .charge: "bolt.fill"
        case .appOpen: "iphone"
        }
    }

    var spoken: String {
        WelcomeText.spoken(title: title, detail: reason)
    }
}

/// One need: its symbol in a round holder, its title and the reason under
/// it. The three rows make one grouped card, each bringing its own part of
/// the card's shape and the hairline over it, so each can rise in by itself.
private struct NeedRow: View {
    let need: Need
    let hasMoved: Bool
    let isCompact: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private static let cornerRadius: CGFloat = 26

    var body: some View {
        HStack(spacing: 14) {
            symbol
                .font(.system(size: 18, weight: .semibold))
                .foregroundStyle(.onAccentContainer)
                .frame(width: 40, height: 40)
                .background(.accentContainer, in: Circle())
            VStack(alignment: .leading, spacing: 2) {
                Text(need.title)
                    .font(.headline)
                Text(need.reason)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, isCompact ? 8 : 12)
        .frame(minHeight: isCompact ? 62 : 72)
        .background(Color(.secondarySystemGroupedBackground), in: shape)
        .overlay(alignment: .top) {
            if need != Need.allCases.first {
                Divider()
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(need.spoken)
    }

    private var shape: UnevenRoundedRectangle {
        let top = need == Need.allCases.first ? Self.cornerRadius : 0
        let bottom = need == Need.allCases.last ? Self.cornerRadius : 0
        return UnevenRoundedRectangle(topLeadingRadius: top, bottomLeadingRadius: bottom, bottomTrailingRadius: bottom,
                                      topTrailingRadius: top, style: .continuous)
    }

    /// The symbol and its one move: the location arrow drops in from above,
    /// the bolt flashes larger and back, the phone turns upright.
    @ViewBuilder private var symbol: some View {
        let image = Image(systemName: need.symbol)
        switch need {
        case .location:
            image
                .offset(y: hasMoved ? 0 : -9)
                .opacity(hasMoved ? 1 : 0)
                .animation(motion(WelcomeMotion.pop), value: hasMoved)
        case .charge:
            // Out past full size and back: two steps, so a phase each.
            PhaseAnimator([false, true], trigger: hasMoved) { isFlashing in
                image.scaleEffect(isFlashing ? 1.25 : 1)
            } animation: { _ in
                motion(.snappy(duration: 0.26))
            }
            .scaleEffect(hasMoved ? 1 : 0.7)
            .opacity(hasMoved ? 1 : 0)
            .animation(motion(.snappy(duration: 0.26)), value: hasMoved)
        case .appOpen:
            image
                .rotationEffect(.degrees(hasMoved ? 0 : 14))
                .opacity(hasMoved ? 1 : 0)
                .animation(motion(WelcomeMotion.pop), value: hasMoved)
        }
    }

    private func motion(_ animation: Animation) -> Animation? {
        reduceMotion ? nil : animation
    }
}

/// What a stopped trip looks like, on the attention colour the app's
/// banners use: the driver opens the app and resumes.
private struct StoppedCard: View {
    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "pause.circle.fill")
                .font(.system(size: 22))
                .foregroundStyle(.amberOnSurface)
                .frame(width: 40, height: 40)
                .background(Color.amberOnSurface.opacity(0.16), in: Circle())
            VStack(alignment: .leading, spacing: 2) {
                Text(WelcomeText.stoppedTitle)
                    .font(.headline)
                Text(WelcomeText.stoppedDetail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .frame(minHeight: 70)
        .background(.attentionContainer, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(WelcomeText.spokenStopped)
    }
}
