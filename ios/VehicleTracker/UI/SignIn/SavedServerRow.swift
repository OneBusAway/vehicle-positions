import SwiftUI

/// The server this phone signs in to, as one quiet line: most drivers never
/// change it. Change opens the field to type another; a server that isn't
/// for changing here, the agency's or an open trip's, has none. At
/// accessibility sizes the line stacks, so the server is never cut short.
struct SavedServerRow: View {
    let label: String
    var onChange: (() -> Void)?

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        let stacked = dynamicTypeSize.isAccessibilitySize
        let layout = stacked ? AnyLayout(VStackLayout(alignment: .leading, spacing: 6)) : AnyLayout(HStackLayout(spacing: 10))
        layout {
            names(stacked: stacked)
                .accessibilityElement(children: .combine)
            if let onChange {
                Button(SignInText.change, action: onChange)
                    .buttonStyle(.borderless)
                    .frame(minHeight: 44)
            }
        }
        .frame(minHeight: 48)
    }

    private func names(stacked: Bool) -> some View {
        let layout = stacked ? AnyLayout(VStackLayout(alignment: .leading, spacing: 2)) : AnyLayout(HStackLayout(spacing: 10))
        return layout {
            HStack(spacing: 10) {
                Image(systemName: "server.rack")
                Text(SignInText.server)
            }
            .foregroundStyle(.secondary)
            .fixedSize()
            Text(label)
                .fontWeight(.medium)
                .lineLimit(stacked ? nil : 1)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .font(.subheadline)
    }
}
