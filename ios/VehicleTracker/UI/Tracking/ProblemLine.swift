import SwiftUI

/// One ink line under the status card while reports are not getting through:
/// what happened, then what it means (DESIGN-iOS §4.7.2). Location stopped
/// carries a Resume button; the two Settings problems make the whole line a
/// button that opens the app's page in Settings.
struct ProblemLine: View {
    let problem: TripStatus.Problem
    let onResume: () -> Void

    @Environment(\.openURL) private var openURL

    var body: some View {
        switch problem.action {
        case .openSettings:
            Button { openURL(.appSettings) } label: { line }
                .buttonStyle(.plain)
                .accessibilityHint("Opens Settings")
        case .resume, nil:
            line
        }
    }

    private var line: some View {
        HStack(spacing: 12) {
            Image(systemName: problem.symbol)
                .font(.system(size: 22))
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.amber)
            (Text(problem.what).font(.headline) + Text(verbatim: " · ") + Text(problem.meaning))
                .font(.body)
                .foregroundStyle(.problemContent)
                .frame(maxWidth: .infinity, alignment: .leading)
            if problem.action == .resume {
                Button(TripStatusText.resume, action: onResume)
                    .buttonStyle(.glass)
                    .controlSize(.small)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .frame(minHeight: 52)
        .background(.problemContainer, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
    }
}
