import SwiftUI

/// One thing a trip needs (DESIGN-iOS §4.6, Android's `PreflightRow`): its
/// marker, title and reason, and its state or the one action that unblocks
/// it. When iOS answers, the marker and the state cross-fade in place.
struct ChecklistRow: View {
    let row: LocationChecklist.Row
    let state: LocationChecklist.RowState
    let onAction: (LocationChecklist.RowAction) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 16) {
            HStack(spacing: 16) {
                ChecklistMarker(look: ChecklistMarker.Look(state), symbol: row.symbol)
                    .id(ChecklistMarker.Look(state))
                    .transition(.opacity)
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
        .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: state)
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
/// while it is the one to act on, a dashed ring before it is reached. The
/// state is said in words beside it.
struct ChecklistMarker: View {
    enum Look: Hashable {
        case done, current, pending

        init(_ state: LocationChecklist.RowState) {
            switch state {
            case .allowed: self = .done
            case .notReached: self = .pending
            case .next, .neededInSettings, .approximate, .off: self = .current
            }
        }
    }

    let look: Look
    let symbol: String

    var body: some View {
        ZStack {
            switch look {
            case .done:
                RoundedRectangle(cornerRadius: 14, style: .continuous).fill(.accent)
                glyph("checkmark").foregroundStyle(.onAccent)
            case .current:
                Circle().fill(.accentContainer)
                glyph(symbol).foregroundStyle(.onAccentContainer)
            case .pending:
                Circle().strokeBorder(Color(.separator), style: StrokeStyle(lineWidth: 2, dash: [4, 4]))
                glyph(symbol).foregroundStyle(.secondary)
            }
        }
        .frame(width: 44, height: 44)
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
