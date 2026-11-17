import SwiftUI

extension EnvironmentValues {
    /// Pushes Help on the pickers' stack. Set by `PickerFlow` only, so the
    /// account menu offers Help there and nowhere else: never on Tracking,
    /// while the vehicle moves.
    @Entry var openHelp: OpenHelpAction?
}

/// Opens Help, as `dismiss` closes a screen. Any two are the same: each
/// pushes Help on the one stack a view is in, so the menu need not be drawn
/// again each time `PickerFlow` is.
struct OpenHelpAction: Equatable {
    let open: () -> Void

    func callAsFunction() {
        open()
    }

    static func == (_: OpenHelpAction, _: OpenHelpAction) -> Bool {
        true
    }
}

/// Help, opened from the account menu: the welcome again, what each line on
/// Tracking means, the build and the server, and Share diagnostics. A plain
/// grouped list in the system's look. Nothing on it moves by itself, and its
/// rows are worked out once (`HelpPage`).
struct HelpView: View {
    @Environment(TripSession.self) private var session
    @State private var showingWelcome = false

    private var host: String { AccountMenu.serverText(session.settings.serverURL) }

    var body: some View {
        List {
            Section {
                Button { showingWelcome = true } label: { WelcomeAgainRow() }
                    .tint(.primary)
            }
            Section {
                ForEach(HelpPage.statusEntries) { HelpEntryRow(entry: $0) }
            } header: {
                SectionTitle(text: HelpText.statusSection)
            }
            Section {
                ForEach(HelpPage.problemEntries) { HelpEntryRow(entry: $0) }
            } header: {
                SectionTitle(text: HelpText.problemSection)
            }
            Section {
                LabeledContent(HelpText.version, value: AccountMenu.versionNumber(Bundle.main.infoDictionary))
                LabeledContent(SignInText.server, value: host)
            } header: {
                SectionTitle(text: HelpText.aboutSection)
            }
            Section {
                ShareLink(item: DiagnosticsFile(host: host), preview: SharePreview(DiagnosticsReport.title)) {
                    Label(HelpText.shareDiagnostics, systemImage: "square.and.arrow.up")
                }
            } footer: {
                Text(HelpText.shareDiagnosticsDetail)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(HelpText.title)
        .navigationBarTitleDisplayMode(.large)
        // Skip, Continue and back come back here, and nothing is marked
        // seen: the driver saw the welcome after the first sign-in.
        .fullScreenCover(isPresented: $showingWelcome) {
            WelcomeView { showingWelcome = false }
        }
    }
}

/// "See the welcome again", which opens the three welcome screens.
private struct WelcomeAgainRow: View {
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        HStack(alignment: typeSize.isAccessibilitySize ? .top : .center, spacing: 14) {
            HelpDisc(symbol: "play.fill", disc: .accentContainer, ink: .onAccentContainer)
            VStack(alignment: .leading, spacing: 2) {
                Text(HelpText.welcomeAgain)
                    .font(.headline)
                Text(HelpText.welcomeAgainDetail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "chevron.right")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A status or a problem line: its symbol on a disc, the words Tracking
/// shows, and what they mean. VoiceOver reads it as one: "On time, You are
/// on the timetable."
private struct HelpEntryRow: View {
    let entry: HelpEntry

    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        // At the largest sizes the words run to many lines, and the disc
        // stays by the title rather than in the middle of them.
        HStack(alignment: typeSize.isAccessibilitySize ? .top : .center, spacing: 14) {
            HelpDisc(symbol: entry.symbol, disc: entry.disc, ink: entry.ink)
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.title)
                    .font(.headline)
                Text(entry.meaning)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// A row's symbol on its disc. It keeps its size at every text size, as the
/// status card's does.
private struct HelpDisc: View {
    let symbol: String
    let disc: Color
    let ink: Color

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: 15, weight: .semibold))
            .symbolRenderingMode(.monochrome)
            .foregroundStyle(ink)
            .frame(width: 32, height: 32)
            .background(disc, in: Circle())
            .accessibilityHidden(true)
    }
}

/// A group's title, marked as a heading so VoiceOver's rotor can jump to it.
private struct SectionTitle: View {
    let text: String

    var body: some View {
        Text(text)
            .accessibilityAddTraits(.isHeader)
    }
}
