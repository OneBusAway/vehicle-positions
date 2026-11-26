import SwiftUI

/// The app's code and the code it took from others, each with its license.
/// The app has no libraries or fonts of its own beyond these.
nonisolated struct License: Hashable {
    let name: String
    let license: String
    /// The text file with the license in full, in the app.
    let file: String

    static let all = [
        License(name: "OneBusAway for iOS", license: "Apache License 2.0", file: "OneBusAway-iOS-LICENSE"),
        License(name: "Vehicle Tracker", license: "Apache License 2.0", file: "VehicleTracker-LICENSE"),
    ]

    /// The license in full, from the app's bundle. Nil only if the file is
    /// missing, which a test rules out.
    func text(in bundle: Bundle = .main) -> String? {
        bundle.url(forResource: file, withExtension: "txt").flatMap { try? String(contentsOf: $0, encoding: .utf8) }
    }
}

/// Each with its license's name. A tap shows the license in full. Android's
/// `LicensesScreen`.
struct LicensesView: View {
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 8) {
                VStack(spacing: 0) {
                    ForEach(Array(License.all.enumerated()), id: \.offset) { index, license in
                        if index > 0 { Divider().padding(.leading, 16) }
                        NavigationLink(value: PickerRoute.license(license)) {
                            HStack(spacing: 14) {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(license.name)
                                    Text(license.license)
                                        .font(.subheadline)
                                        .foregroundStyle(.secondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                                Image(systemName: "chevron.forward")
                                    .font(.footnote.weight(.semibold))
                                    .foregroundStyle(.tertiary)
                                    .accessibilityHidden(true)
                            }
                            .padding(.horizontal, 16)
                            .padding(.vertical, 10)
                            .frame(minHeight: 52)
                        }
                        .buttonStyle(SettingsRowStyle())
                    }
                }
                .settingsCard()
                Text(PrivacyText.noOtherLibraries)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 20)
            }
            .padding(EdgeInsets(top: 8, leading: 16, bottom: 32, trailing: 16))
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(PrivacyText.licensesTitle)
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
    }
}

/// One license in full, a paragraph at a time.
struct LicenseView: View {
    let license: License

    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                ForEach(Array(LicenseText.paragraphs(license.text() ?? "").enumerated()), id: \.offset) { _, paragraph in
                    Text(paragraph)
                        .font(.subheadline)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(EdgeInsets(top: 8, leading: 20, bottom: 32, trailing: 20))
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(license.name)
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
    }
}

/// A license file as paragraphs that wrap to the screen, as Android's
/// `paragraphsOf` makes them.
nonisolated enum LicenseText {
    /// Shorter than this, a line was broken where it ends on purpose: the
    /// texts are kept to about 70 a line.
    static let wrappedLine = 50

    /// `text` cut at its blank lines. A paragraph's lines are joined again
    /// where its file broke them only to keep them short. A line stays on its
    /// own after a short one, which was broken there on purpose, after a rule,
    /// and when it starts a list item, a heading or a rule.
    static func paragraphs(_ text: String) -> [String] {
        text.replacing("\r\n", with: "\n")
            .split(separator: /\n[ \t]*\n+/)
            .map(String.init)
            .filter { !$0.allSatisfy(\.isWhitespace) }
            .map(unwrapped)
    }

    private static func unwrapped(_ paragraph: String) -> String {
        let lines = paragraph.split(separator: "\n", omittingEmptySubsequences: true)
            .map { String($0).replacing(/\s+$/, with: "") }
            .filter { !$0.isEmpty }
        guard var joined = lines.first?.trimmingCharacters(in: .whitespaces) else { return "" }
        for (before, line) in zip(lines, lines.dropFirst()) {
            let continues = before.count >= wrappedLine && !isRule(before) && !startsItsOwnLine(line)
            joined += (continues ? " " : "\n") + line.trimmingCharacters(in: .whitespaces)
        }
        return joined
    }

    private static func isRule(_ line: String) -> Bool {
        line.wholeMatch(of: /\s*(={3,}|-{3,})\s*/) != nil
    }

    private static func startsItsOwnLine(_ line: String) -> Bool {
        line.prefixMatch(of: /\s*(([-*+#]|\(?[0-9a-zA-Z]{1,3}[.)])\s|={3,}|-{3,})/) != nil
    }
}
