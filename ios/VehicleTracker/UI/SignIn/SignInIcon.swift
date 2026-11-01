import SwiftUI

/// The app's icon over the sign-in title, rendered from `AppIcon.icon` in
/// its default and dark looks: an `.icon` can't be drawn at run time. The
/// title beside it says what the screen is, so VoiceOver skips it.
struct SignInIcon: View {
    var body: some View {
        Image(.signInIcon)
            .resizable()
            .frame(width: 52, height: 52)
            .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            .accessibilityHidden(true)
    }
}
