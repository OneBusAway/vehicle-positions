import SwiftUI

/// What stopped the driver on a picker screen, such as a run the server
/// would not start: a symbol, one sentence, and at most one action, the one
/// that fixes it (DESIGN-iOS §3, Android's `AttentionBanner`). VoiceOver
/// reads it as one element, with the action as its action.
struct AttentionBanner: View {
    let symbol: String
    let text: String
    var action: String? = nil
    var onAction: () -> Void = {}

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 22))
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.amberOnSurface)
            Text(text)
                .font(.subheadline)
                .foregroundStyle(Color(.label))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.vertical, 14)
            if let action {
                Button(action, action: onAction)
                    .buttonStyle(.plain)
                    .font(.subheadline.weight(.semibold))
                    .padding(.horizontal, 12)
                    .frame(minHeight: 44)
            }
        }
        .padding(.leading, 16)
        // The action's own padding makes up the end inset.
        .padding(.trailing, action == nil ? 16 : 4)
        .frame(minHeight: 56)
        .background(.attentionContainer, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .accessibilityElement(children: .combine)
    }
}
