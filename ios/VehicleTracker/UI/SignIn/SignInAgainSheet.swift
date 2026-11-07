import SwiftUI

/// Sign in again (DESIGN-iOS §4.9), over the trip sheet. It opens only from
/// the Signed out card's Sign in, never by itself: a sheet coming up over
/// the map while the vehicle moves is the wrong moment for it. Not now
/// leaves the trip as it is, with the card still there.
struct SignInAgainSheet: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            SignInView(isAgain: true) { dismiss() }
                .navigationTitle(SignInText.againTitle)
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button(SignInText.notNow) { dismiss() }
                    }
                }
        }
        .presentationDetents([.large])
    }
}
