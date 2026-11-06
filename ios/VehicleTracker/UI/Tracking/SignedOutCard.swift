import SwiftUI

/// Takes the status card's place once the driver's sign-in has expired
/// (DESIGN-iOS §4.7.3): ink by day and amber by night, with a full-width
/// Sign in that opens the sign-in sheet.
struct SignedOutCard: View {
    let onSignIn: () -> Void

    @ScaledMetric(relativeTo: .largeTitle) private var titleSize = 40.0

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top, spacing: 14) {
                ZStack {
                    Circle().fill(.signedOutAccent)
                    Image(systemName: "lock.fill")
                        .font(.system(size: 24, weight: .semibold))
                        .foregroundStyle(.signedOutOnAccent)
                }
                .frame(width: 48, height: 48)
                VStack(alignment: .leading, spacing: 4) {
                    Text(TripStatusText.signedOut)
                        .font(.system(size: titleSize, weight: .semibold))
                        .lineLimit(1)
                        .minimumScaleFactor(0.6)
                    Text(TripStatusText.signedOutMessage)
                        .font(.title3)
                }
                .foregroundStyle(.signedOutContent)
            }
            Button(action: onSignIn) {
                Text(TripStatusText.signIn)
                    .font(.headline)
                    .foregroundStyle(.signedOutOnAccent)
                    .frame(maxWidth: .infinity, minHeight: 52)
            }
            .buttonStyle(.borderedProminent)
            .tint(.signedOutAccent)
        }
        .padding(16)
        .background(.signedOutContainer, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
    }
}
