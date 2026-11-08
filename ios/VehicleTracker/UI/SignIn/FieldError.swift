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
    /// Reads `error` with the field, straight after its name, while there is
    /// one. No error is a nil value, not a branch: a branch would make the
    /// field a new view when its error goes, and the new one has no focus,
    /// so the keyboard would close.
    func fieldError(_ error: String?) -> some View {
        accessibilityCustomContent(AccessibilityCustomContentKey(Text(SignInText.errorLabel), id: "fieldError"),
                                   error.map { Text($0) }, importance: .high)
    }
}
