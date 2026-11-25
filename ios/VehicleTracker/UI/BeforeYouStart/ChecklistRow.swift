import SwiftUI

/// One thing a trip needs (DESIGN-iOS §4.6, Android's `PreflightRow`): its
/// marker, title and reason, and its state or the one action that unblocks
/// it. When iOS answers, the marker and the state cross-fade in place. A row
/// the sheet sees iOS allow ticks instead (Android's `tick`): the icon turns
/// into the check and the row dips a little and settles.
struct ChecklistRow: View {
    let row: LocationChecklist.Row
    let state: LocationChecklist.RowState
    /// `LocationChecklist.hasTicked(_:)`: turning true plays the tick.
    let ticked: Bool
    let onAction: (LocationChecklist.RowAction) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 16) {
            HStack(spacing: 16) {
                ChecklistMarker(look: ChecklistMarker.Look(state), symbol: row.symbol)
                    .animation(ChecklistMotion.tick(reduceMotion: reduceMotion), value: ticked)
                    .symbolEffectsRemoved(reduceMotion)
                    // Placed by the row's animation, not the tick's, so it
                    // keeps in step with the words if the row's height changes.
                    .geometryGroup()
                VStack(alignment: .leading, spacing: 2) {
                    Text(ChecklistText.title(of: row))
                        .font(.headline)
                    Text(ChecklistText.reason(of: row, state))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                stateWord
                    .transition(.opacity)
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(ChecklistText.spoken(row, state))
            if let action = state.action {
                Button(action == .openSettings ? ChecklistText.openSettings : ChecklistText.tryAgain) { onAction(action) }
                    .buttonStyle(.borderless)
                    .font(.subheadline.weight(.medium))
                    .frame(minHeight: 44)
                    .transition(.opacity)
            }
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 8)
        .frame(minHeight: 76)
        .animation(ChecklistMotion.change(reduceMotion: reduceMotion), value: state)
        // The whole row moves as one, whatever its parts are animating.
        .geometryGroup()
        .phaseAnimator([0, ChecklistMotion.dip(reduceMotion: reduceMotion)], trigger: ticked) { row, dip in
            row.offset(y: dip)
        } animation: { dip in
            dip == 0 ? ChecklistMotion.settle : ChecklistMotion.push
        }
    }

    @ViewBuilder private var stateWord: some View {
        switch state {
        case .next:
            Text(ChecklistText.next)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.accent)
        case .allowed:
            Text(ChecklistText.allowed)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)
        case .neededInSettings, .approximate, .off, .notReached:
            EmptyView()
        }
    }
}

/// A row's marker, 44 pt with a 22 pt glyph (Android's `Marker`): a filled
/// square with a check once allowed, a filled circle with the row's symbol
/// while it is the one to act on, a dashed ring before it is reached, and on
/// Permission check an amber ring with a minus when it is missing. The state
/// is said in words beside it. A look that comes in fades in, except
/// the check, which grows in from half its size and draws itself. A look
/// that goes fades out.
struct ChecklistMarker: View {
    enum Look: Hashable {
        case done, current, pending, missing

        init(stop: Readiness.Stop) {
            switch stop {
            case .allowed: self = .done
            case .missing: self = .missing
            case .beyond: self = .pending
            }
        }

        init(_ state: LocationChecklist.RowState) {
            switch state {
            case .allowed: self = .done
            case .notReached: self = .pending
            case .next, .neededInSettings, .approximate, .off: self = .current
            }
        }
    }

    static let side: CGFloat = 44

    let look: Look
    let symbol: String

    var body: some View {
        ZStack {
            switch look {
            case .done:
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.accent)
                    .transition(AsymmetricTransition(insertion: ScaleTransition(0.5).combined(with: .opacity), removal: .opacity))
                glyph("checkmark").foregroundStyle(.onAccent)
                    .transition(AsymmetricTransition(insertion: ScaleTransition(0.5).combined(with: .symbolEffect(.drawOn)), removal: .opacity))
            case .current:
                Circle().fill(.accentContainer)
                glyph(symbol).foregroundStyle(.onAccentContainer)
            case .pending:
                Circle().strokeBorder(Color(.separator), style: StrokeStyle(lineWidth: 2, dash: [4, 4]))
                glyph(symbol).foregroundStyle(.secondary)
            case .missing:
                Circle().strokeBorder(.amberOnSurface, lineWidth: 2)
                glyph("minus").foregroundStyle(.amberOnSurface)
            }
        }
        .frame(width: Self.side, height: Self.side)
    }

    private func glyph(_ name: String) -> some View {
        Image(systemName: name)
            .resizable()
            .scaledToFit()
            .fontWeight(.semibold)
            .frame(width: 22, height: 22)
    }
}

extension LocationChecklist.Row {
    var symbol: String {
        switch self {
        case .location: "location.fill"
        case .precise: "scope"
        case .locationServices: "location.magnifyingglass"
        }
    }
}

/// Before you start's springs, the app's own. A change on a row fades on
/// `.smooth`; the check lands with `.snappy`'s small bounce, which only
/// something as small as the marker may have, and the row's dip never
/// overshoots. With Reduce Motion nothing moves: the row shows its new look,
/// check and all, at once.
nonisolated enum ChecklistMotion {
    static func change(reduceMotion: Bool) -> Animation? {
        reduceMotion ? nil : .smooth(duration: 0.25)
    }

    static func tick(reduceMotion: Bool) -> Animation? {
        reduceMotion ? nil : .snappy(duration: 0.35)
    }

    /// How far the row dips as the check lands, in points, before it settles back.
    static func dip(reduceMotion: Bool) -> CGFloat {
        reduceMotion ? 0 : 2
    }

    static let push = Animation.smooth(duration: 0.12)
    static let settle = Animation.smooth(duration: 0.3)
}
