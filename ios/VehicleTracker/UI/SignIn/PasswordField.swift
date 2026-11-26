import SwiftUI

/// The password, hidden or shown with the eye at its end. Both fields stay in
/// place over one text and the eye moves the focus to the one now showing:
/// the keyboard stays up, and no secure field leaves the screen, which
/// Password AutoFill reads as a submitted form and offers to save. The hidden
/// password is only ever typed into the secure field, which no other
/// keyboard can read. As in any secure field on iOS, typing after hiding it
/// again starts it over.
struct PasswordField<Field: Hashable>: View {
    /// What VoiceOver calls it, and, when `prompted`, what the empty field
    /// says, for a form with no names beside its fields.
    let name: String
    @Binding var text: String
    var focus: FocusState<Field?>.Binding
    /// This field's value of `focus`.
    let field: Field
    var contentType = UITextContentType.password
    var prompted = false
    var submitLabel = SubmitLabel.go
    let error: String?
    let onSubmit: () -> Void

    @State private var shown = false
    /// The shown field's own focus; the hidden one takes the form's.
    @FocusState private var shownFocused: Bool

    var body: some View {
        HStack(spacing: 4) {
            ZStack {
                SecureField(name, text: $text, prompt: prompt)
                    .textContentType(contentType)
                    .focused(focus, equals: field)
                    .opacity(shown ? 0 : 1)
                    .allowsHitTesting(!shown)
                    .accessibilityHidden(shown)
                    .accessibilityLabel(name)
                    .fieldError(error)
                TextField(name, text: $text, prompt: prompt)
                    .focused($shownFocused)
                    .opacity(shown ? 1 : 0)
                    .allowsHitTesting(shown)
                    .accessibilityHidden(!shown)
                    .accessibilityLabel(name)
                    .fieldError(error)
            }
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .submitLabel(submitLabel)
            .onSubmit(onSubmit)
            Button {
                let typing = focus.wrappedValue == field || shownFocused
                shown.toggle()
                guard typing else { return }
                if shown {
                    shownFocused = true
                } else {
                    focus.wrappedValue = field
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
        .onChange(of: focus.wrappedValue) { _, focused in
            if shown && focused == field { shownFocused = true }
        }
    }

    private var prompt: Text {
        prompted ? Text(name) : Text(verbatim: "")
    }
}
