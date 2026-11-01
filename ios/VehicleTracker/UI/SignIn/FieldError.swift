import SwiftUI

/// What is wrong with the field above it. VoiceOver reads it with the field
/// itself (`fieldError(_:)`), so the line is hidden from it.
struct FieldError: View {
    let text: String

    var body: some View {
        Label {
            Text(text)
        } icon: {
            Image(systemName: "exclamationmark.circle.fill")
        }
        .font(.footnote)
        .foregroundStyle(.errorText)
        .accessibilityHidden(true)
    }
}

extension View {
    /// Reads `error` with the field, straight after its name, while there is one.
    @ViewBuilder func fieldError(_ error: String?) -> some View {
        if let error {
            accessibilityCustomContent(Text(SignInText.errorLabel), Text(error), importance: .high)
        } else {
            self
        }
    }
}
