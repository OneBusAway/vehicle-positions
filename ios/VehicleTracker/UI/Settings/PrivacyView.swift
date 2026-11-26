import SwiftUI

/// In plain words: what the app sends, when and to whom, and what it keeps on
/// the iPhone. Android's `PrivacyScreen`.
struct PrivacyView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.openURL) private var openURL
    @Environment(\.dynamicTypeSize) private var typeSize
    /// The agency's privacy policy, once the server has given it.
    @State private var policy: URL?

    private var server: String? {
        let server = AccountText.serverText(session.settings.serverURL)
        return server.isEmpty ? nil : server
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 26) {
                group(PrivacyText.sentSection) {
                    PrivacyLine(symbol: "clock", title: PrivacyText.trip, detail: PrivacyText.tripDetail)
                    PrivacyDivider()
                    PrivacyLine(symbol: "location", title: PrivacyText.position, detail: PrivacyText.positionDetail)
                    PrivacyDivider()
                    PrivacyLine(symbol: "person.3", title: PrivacyText.occupancy, detail: PrivacyText.occupancyDetail)
                }
                group(PrivacyText.whoSection) {
                    PrivacyLine(symbol: "server.rack", title: PrivacyText.agency, detail: PrivacyText.agencyDetail(server: server?.bidiIsolated))
                    PrivacyDivider()
                    PrivacyLine(symbol: "figure.wave", title: PrivacyText.riders, detail: PrivacyText.ridersDetail)
                    PrivacyDivider()
                    PrivacyLine(symbol: "map", title: PrivacyText.appleMaps, detail: PrivacyText.appleMapsDetail)
                }
                group(PrivacyText.keptSection) {
                    PrivacyLine(symbol: "person.crop.circle", title: PrivacyText.account, detail: PrivacyText.accountDetail)
                    PrivacyDivider()
                    PrivacyLine(symbol: "icloud.slash", title: PrivacyText.kept, detail: PrivacyText.keptDetail)
                }
                // Not there without a link.
                if let policy {
                    group(PrivacyText.moreSection) {
                        // Safari, never a page in the app: the agency's policy is theirs.
                        Button { openURL(policy) } label: {
                            PrivacyLine(symbol: "doc.text", title: PrivacyText.policy, detail: PrivacyText.policyDetail, end: "arrow.up.forward")
                        }
                        .buttonStyle(SettingsRowStyle())
                    }
                }
            }
            .padding(EdgeInsets(top: 8, leading: 16, bottom: 32, trailing: 16))
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(PrivacyText.title)
        // A large title is cut short at the largest sizes.
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
        .task { policy = await session.privacyPolicyLink() }
    }

    private func group(_ title: String, @ViewBuilder rows: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            SettingsGroupTitle(title: title)
            VStack(spacing: 0) { rows() }
                .settingsCard()
        }
    }
}

/// One thing Privacy says: a symbol, and its words.
private struct PrivacyLine: View {
    let symbol: String
    let title: String
    let detail: String
    /// What the row's end shows, when a tap takes it somewhere.
    var end: String?

    @Environment(\.dynamicTypeSize) private var typeSize

    static let symbolSide: CGFloat = 24
    static let wordsInset: CGFloat = 16 + symbolSide + 14

    var body: some View {
        HStack(alignment: typeSize.isAccessibilitySize ? .firstTextBaseline : .center, spacing: 14) {
            Image(systemName: symbol)
                .font(.system(size: 19))
                .foregroundStyle(.secondary)
                .frame(width: Self.symbolSide, height: Self.symbolSide)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                Text(detail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if let end {
                Image(systemName: end)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .frame(minHeight: 52)
        .accessibilityElement(children: .combine)
    }
}

private struct PrivacyDivider: View {
    var body: some View {
        Divider().padding(.leading, PrivacyLine.wordsInset)
    }
}
