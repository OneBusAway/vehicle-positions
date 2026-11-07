import SwiftUI

/// What a picker shows in place of its list when there is nothing to choose
/// (DESIGN-iOS §3, Android's `EmptyState`): a symbol, what happened, what the
/// driver can do about it, and one button when the app itself can do something.
struct EmptyState: View {
    let symbol: String
    let title: String
    var detail: String? = nil
    var action: String? = nil
    var onAction: () -> Void = {}

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: symbol)
        } description: {
            if let detail {
                Text(detail)
            }
        } actions: {
            if let action {
                Button(action, action: onAction)
                    .buttonStyle(.bordered)
                    .tint(.accent)
                    .controlSize(.large)
            }
        }
    }
}
