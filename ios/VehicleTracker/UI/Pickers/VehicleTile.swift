import SwiftUI

/// One vehicle on Choose your vehicle (DESIGN-iOS §4.2, Android's
/// `BusTileView`): the fleet number big, the label under it when it says
/// more than the number, and the favourite star in the corner. The tile is
/// one button, read as "4317, Bus 4317, Favorite"; the star keeps its own.
struct VehicleTile: View {
    static let gap: CGFloat = 12

    let tile: VehicleTileModel
    let onSelect: () -> Void
    let onToggleFavorite: () -> Void

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Button(action: onSelect) {
                face
            }
            .buttonStyle(.plain)
            .accessibilityLabel(PickerText.vehicleTile(id: tile.vehicle.id, label: tile.label))
            .accessibilityValue(tile.favorite ? PickerText.favorite : "")
            // The same toggle as the star, so a long press offers it too.
            .contextMenu {
                Button(starLabel, systemImage: tile.favorite ? "star.slash" : "star", action: onToggleFavorite)
            }
            Button(action: onToggleFavorite) {
                Image(systemName: tile.favorite ? "star.fill" : "star")
                    .foregroundStyle(tile.favorite ? Color.amberOnSurface : Color(.secondaryLabel))
                    .frame(width: 44, height: 44)
                    // A plain button takes taps on what it draws, which here
                    // is the glyph alone; the whole square is the target.
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(starLabel)
        }
    }

    private var starLabel: String {
        tile.favorite ? PickerText.removeFromFavorites(tile.vehicle.id) : PickerText.addToFavorites(tile.vehicle.id)
    }

    private var face: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(tile.vehicle.id)
                .font(.largeTitle.weight(.medium).monospacedDigit())
                .lineLimit(1)
                .minimumScaleFactor(0.5)
                // Clear of the star, which sits over the end of this line on a short tile.
                .padding(.trailing, 30)
            if let label = tile.label {
                Text(label)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 18)
        .padding(.vertical, 14)
        .frame(maxWidth: .infinity, minHeight: 96, alignment: .bottomLeading)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
    }
}
