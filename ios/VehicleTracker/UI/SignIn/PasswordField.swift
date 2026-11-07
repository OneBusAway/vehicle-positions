import SwiftUI

/// The password, hidden or shown with the eye at its end. Both fields stay in
/// place over one text and the eye moves the focus to the one now showing:
/// the keyboard stays up, and no secure field leaves the screen, which
/// Password AutoFill reads as a submitted form and offers to save. The hidden
/// password is only ever typed into the secure field, which no other
/// keyboard can read. As in any secure field on iOS, typing after hiding it
/// again starts it over.
struct PasswordField: View {
    @Binding var text: String
    var focus: FocusState<SignInField?>.Binding
    let error: String?
    let onSubmit: () -> Void

    @State private var shown = false
    /// The shown field's own focus; the hidden one takes the form's.
    @FocusState private var shownFocused: Bool

    var body: some View {
        HStack(spacing: 4) {
            ZStack {
                SecureField(SignInText.password, text: $text, prompt: Text(verbatim: ""))
                    .textContentType(.password)
                    .focused(focus, equals: .password)
                    .opacity(shown ? 0 : 1)
                    .allowsHitTesting(!shown)
                    .accessibilityHidden(shown)
                    .accessibilityLabel(SignInText.password)
                    .fieldError(error)
                TextField(SignInText.password, text: $text, prompt: Text(verbatim: ""))
                    .focused($shownFocused)
                    .opacity(shown ? 1 : 0)
                    .allowsHitTesting(shown)
                    .accessibilityHidden(!shown)
                    .accessibilityLabel(SignInText.password)
                    .fieldError(error)
            }
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .submitLabel(.go)
            .onSubmit(onSubmit)
            Button {
                let typing = focus.wrappedValue == .password || shownFocused
                shown.toggle()
                guard typing else { return }
                if shown {
                    shownFocused = true
                } else {
                    focus.wrappedValue = .password
                }
            } label: {
                Image(systemName: shown ? "eye.slash" : "eye")
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .accessibilityLabel(shown ? SignInText.hidePassword : SignInText.showPassword)
        }
        // The form asks for the password, a missing one, while it is shown:
        // the field showing takes it.
        .onChange(of: focus.wrappedValue) { _, field in
            if shown && field == .password { shownFocused = true }
        }
    }
}
