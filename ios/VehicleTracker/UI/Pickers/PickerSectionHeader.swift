import SwiftUI

/// A section header in a picker's grid, in the style of a list's: Favorites,
/// Recent, All vehicles, All routes · 33.
struct PickerSectionHeader: View {
    let title: String

    var body: some View {
        Text(title)
            .font(.footnote)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.top, 8)
            .accessibilityAddTraits(.isHeader)
    }
}
