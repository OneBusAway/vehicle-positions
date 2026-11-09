import SwiftUI

/// What stopped the driver on a picker screen, such as a run the server
/// would not start: a symbol, one sentence, and at most one action, the one
/// that fixes it (DESIGN-iOS §3, Android's `AttentionBanner`). VoiceOver
/// reads it as one element, with the action as its action.
///
/// While `busyLabel` is set the action is running: a spinner takes the
/// button's place, so a second tap has nothing to hit, and VoiceOver says
/// `busyLabel`. If the banner is still there when it ends, the action did
/// not fix it, and VoiceOver reads the sentence again.
struct AttentionBanner: View {
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let symbol: String
    let text: String
    var action: String? = nil
    var busyLabel: String? = nil
    var onAction: () -> Void = {}

    var body: some View {
        Group {
            if dynamicTypeSize.isAccessibilitySize {
                // The action goes under the sentence: beside it, the two
                // would share a width that fits neither, and a bar that runs
                // out of height cuts the sentence short.
                VStack(alignment: .leading, spacing: 0) {
                    HStack(alignment: .top, spacing: 12) {
                        icon
                        sentence
                    }
                    if let action {
                        button(action)
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, action == nil ? 0 : 8)
            } else {
                HStack(spacing: 12) {
                    icon
                    sentence
                    if let action {
                        button(action)
                    }
                }
                .padding(.leading, 16)
                // The action's own padding makes up the end inset.
                .padding(.trailing, action == nil ? 16 : 4)
            }
        }
        .frame(minHeight: 56)
        .background(.attentionContainer, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityValue(busyLabel ?? "")
        .onChange(of: busyLabel) {
            AccessibilityNotification.Announcement(busyLabel ?? text).post()
        }
    }

    private var icon: some View {
        Image(systemName: symbol)
            .font(.system(size: 22))
            .symbolRenderingMode(.hierarchical)
            .foregroundStyle(.amberOnSurface)
    }

    private var sentence: some View {
        Text(text)
            .font(.subheadline)
            .foregroundStyle(Color(.label))
            // In a bar on the smallest phone at the largest size, the longest
            // sentence has more lines than the screen: it gives up a fifth of
            // its size there rather than its end.
            .minimumScaleFactor(dynamicTypeSize.isAccessibilitySize ? 0.8 : 1)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.vertical, 14)
    }

    private func button(_ action: String) -> some View {
        Group {
            if busyLabel != nil {
                // The words stay, unseen, so the spinner keeps the button's
                // size and the sentence does not move.
                Text(action)
                    .hidden()
                    .overlay { ProgressView() }
                    .accessibilityHidden(true)
            } else {
                Button(action, action: onAction)
                    .buttonStyle(.plain)
            }
        }
        .font(.subheadline.weight(.semibold))
        .padding(.horizontal, 12)
        .frame(minHeight: 44)
    }
}
