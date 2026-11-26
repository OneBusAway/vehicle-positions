import OSLog
import SwiftUI

private let groupCorner: CGFloat = 26

/// Settings, opened from the account button on the pickers: whether this
/// phone can report a trip, who is signed in and Sign out, what the app is
/// allowed and does during a trip, its language and theme, Help, Share
/// diagnostics, Privacy, Licenses and the build. Android's `SettingsScreen`.
struct SettingsView: View {
    let access: any LocationAccess
    let openPermissionCheck: () -> Void

    @Environment(TripSession.self) private var session
    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var location: LocationAccessState
    /// What the header shows, a moment behind `readiness` as that changes.
    @State private var shown: Readiness?
    @State private var fold = HeaderFold()
    /// The header has folded as far as its second title line.
    @State private var isFolded = false
    /// How far the page can scroll.
    @State private var furthest: CGFloat = 0

    init(access: any LocationAccess, openPermissionCheck: @escaping () -> Void) {
        self.access = access
        self.openPermissionCheck = openPermissionCheck
        _location = State(initialValue: access.current())
    }

    private var readiness: Readiness {
        Readiness(access: location, waiting: session.reportsWaiting.count)
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                ReadinessHeader(readiness: readiness, fold: $fold, onFix: fix, openCheck: openPermissionCheck, onShow: { shown = $0 })
                    // Over the rows, which go under it once it has folded.
                    .zIndex(1)
                VStack(alignment: .leading, spacing: 0) {
                    AccountCard()
                    SettingsGroup(title: SettingsText.phoneSection) {
                        PermissionCheckRow(readiness: readiness, open: openPermissionCheck)
                        SettingsDivider()
                        TripSettingsRow()
                    }
                    SettingsGroup(title: SettingsText.appSection) {
                        LanguageSettingsRow()
                        SettingsDivider()
                        ThemeSettingsRow()
                    }
                    SettingsGroup(title: SettingsText.accountSection) {
                        NavigationLink(value: PickerRoute.changePassword) {
                            SettingsRow(symbol: "key", title: ChangePasswordText.title, end: .opens)
                        }
                        .buttonStyle(SettingsRowStyle())
                    }
                    AboutGroup()
                }
                .padding(EdgeInsets(top: 30, leading: 16, bottom: 32, trailing: 16))
            }
        }
        .background(Color(.systemGroupedBackground))
        .scrollEdgeEffectHidden(true, for: .top)
        .scrollTargetBehavior(FoldSettling(fold: fold, furthest: furthest, isOn: !reduceMotion))
        .onScrollGeometryChange(for: Bool.self) { [fold] scroll in
            fold.showsBarLine(scroll.contentOffset.y + scroll.contentInsets.top)
        } action: { _, folded in
            isFolded = folded
        }
        .onScrollGeometryChange(for: CGFloat.self) { scroll in
            scroll.contentSize.height + scroll.contentInsets.top + scroll.contentInsets.bottom - scroll.containerSize.height
        } action: { _, reach in
            furthest = reach
        }
        .navigationTitle(SettingsText.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                SettingsBarTitle(readiness: shown ?? readiness, isFolded: isFolded)
                    .contentShape(.rect)
                    // The folded bar is the header, for VoiceOver as well.
                    .onTapGesture { if isFolded { openPermissionCheck() } }
                    .accessibilityElement(children: .combine)
                    .accessibilityAddTraits(isFolded ? .isButton : [])
                    .accessibilityHint(isFolded ? PermissionCheckText.title : "")
                    .accessibilityAction(.default) { if isFolded { openPermissionCheck() } }
            }
        }
        // A driver back from the Settings app, or from iOS's own prompt, may
        // have changed the answer there.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { read() }
        }
        // So may Permission check.
        .onAppear(perform: read)
    }

    private func read() {
        location = access.current()
    }

    private func fix(_ fix: Readiness.Fix) {
        switch fix {
        case .openSettings: openURL(.appSettings)
        case .readAgain: read()
        case .ask: openPermissionCheck()
        }
    }
}

/// The bar's title, and under it, once the header has folded, what the header
/// said: drawn here, as the bar's own second line is too faint on the block.
private struct SettingsBarTitle: View {
    let readiness: Readiness
    let isFolded: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(spacing: 0) {
            Text(SettingsText.title)
                .font(.headline)
                .accessibilityAddTraits(.isHeader)
            if isFolded {
                Text(ReadinessWords(readiness).title)
                    .font(.footnote)
                    // The header says it, folded or not.
                    .accessibilityHidden(true)
                    .transition(.opacity)
            }
        }
        .lineLimit(1)
        .foregroundStyle(readiness.ink)
        // As the bar holds its own titles.
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
        .animation(reduceMotion ? nil : .smooth(duration: 0.2), value: isFolded)
    }
}

/// Let go in the middle of the fold, the header settles to the nearer end.
private struct FoldSettling: ScrollTargetBehavior {
    let fold: HeaderFold
    let furthest: CGFloat
    let isOn: Bool

    func updateTarget(_ target: inout ScrollTarget, context: TargetContext) {
        guard isOn else { return }
        target.rect.origin.y = fold.settled(target.rect.minY, furthest: furthest)
    }
}

/// Who is signed in, and to which server, beside Sign out: phones go from
/// driver to driver. Sign out asks first, and shows a spinner while the
/// server is told.
private struct AccountCard: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var confirming = false

    /// Less room than this for the email, and Sign out goes under it.
    private static let leastWords: CGFloat = 186

    private var email: String { session.settings.email }
    private var server: String { AccountText.serverText(session.settings.serverURL) }

    var body: some View {
        Group {
            if typeSize.isAccessibilitySize {
                stacked
            } else {
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 12) {
                        words.frame(minWidth: Self.leastWords, idealWidth: Self.leastWords, maxWidth: .infinity, alignment: .leading)
                        signOut
                    }
                    stacked
                }
            }
        }
        .padding(EdgeInsets(top: 13, leading: 20, bottom: 13, trailing: 14))
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemGroupedBackground), in: .rect(cornerRadius: groupCorner))
    }

    private var stacked: some View {
        VStack(alignment: .leading, spacing: 10) {
            words
            signOut
        }
    }

    private var words: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(AccountAddress.breakable(email).bidiIsolated)
                .font(.headline)
            Text(AccountAddress.breakable(server).bidiIsolated)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: "\(email.bidiIsolated), \(server.bidiIsolated)"))
    }

    private var signOut: some View {
        Button { confirming = true } label: {
            Text(AccountText.signOut)
                .fontWeight(.medium)
                .foregroundStyle(Color(.label))
                .frame(minHeight: 30)
                .opacity(session.isSigningOut ? 0 : 1)
                .overlay {
                    if session.isSigningOut { ProgressView() }
                }
        }
        .buttonStyle(.bordered)
        .buttonBorderShape(.capsule)
        .tint(Color(.systemGray))
        // A start from the car can be in flight while the phone shows this;
        // a sign-out would do nothing until it is answered.
        .disabled(session.isSigningOut || session.phase.isStarting)
        .confirmationDialog(AccountText.signOutTitle, isPresented: $confirming, titleVisibility: .visible) {
            Button(AccountText.signOut, role: .destructive) {
                Task { await session.signOut() }
            }
            Button(AccountText.cancel, role: .cancel) {}
        } message: {
            Text(AccountText.signOutMessage)
        }
    }
}

nonisolated enum AccountAddress {
    /// An email or a host that breaks after its @ or before a dot when it is
    /// too long for its line.
    static func breakable(_ address: String) -> String {
        address.replacing("@", with: "@\u{200B}").replacing(".", with: "\u{200B}.")
    }
}

/// How what the app is allowed stands, in a few words, and the page that says
/// more.
private struct PermissionCheckRow: View {
    let readiness: Readiness
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            SettingsRow(symbol: readiness.canReport ? "checkmark.shield" : "exclamationmark.shield.fill",
                        tint: readiness.canReport ? .secondary : .amberOnSurface,
                        title: PermissionCheckText.title, value: SettingsText.permissions(readiness), end: .opens)
        }
        .buttonStyle(SettingsRowStyle())
    }
}

/// What Tracking does during a trip: when the screen stays on, and the page
/// with the rest.
private struct TripSettingsRow: View {
    @Environment(TripSession.self) private var session

    var body: some View {
        NavigationLink(value: PickerRoute.tripSettings) {
            SettingsRow(symbol: "location", title: TripSettingsText.title, value: SettingsText.screen(session.settings.screenOn), end: .opens)
        }
        .buttonStyle(SettingsRowStyle())
    }
}

/// The language the app is shown in. iOS keeps each app's language on the
/// app's own page in the Settings app (UIPrefersShowingLanguageSettings), so
/// a tap opens that page.
private struct LanguageSettingsRow: View {
    @Environment(\.openURL) private var openURL

    var body: some View {
        Button { openURL(.appSettings) } label: {
            SettingsRow(symbol: "globe", title: SettingsText.language, value: AppLanguage.ownName(AppLanguage.shownCode), end: .opens)
        }
        .buttonStyle(SettingsRowStyle())
        .accessibilityHint(TripStatusText.opensSettings)
    }
}

/// The app's theme, and the menu a tap opens, the chosen one checked.
private struct ThemeSettingsRow: View {
    @Environment(TripSession.self) private var session

    var body: some View {
        @Bindable var settings = session.settings
        let chosen = SettingsText.name(of: settings.theme)
        SettingsRow(symbol: "circle.lefthalf.filled", title: SettingsText.theme, value: chosen, end: .chooses)
            .accessibilityHidden(true)
            // Over the row rather than with the row as its label: iOS turns a
            // menu's label into the menu, and the row would go.
            .overlay {
                Menu {
                    Picker(SettingsText.theme, selection: $settings.theme) {
                        ForEach(ThemeChoice.allCases, id: \.self) { Text(SettingsText.name(of: $0)) }
                    }
                } label: {
                    Color.clear.contentShape(.rect)
                }
                .accessibilityLabel(SettingsText.theme)
                .accessibilityValue(chosen)
            }
    }
}

/// Help, Share diagnostics, Privacy, Licenses and the build.
private struct AboutGroup: View {
    @Environment(TripSession.self) private var session
    /// The file being written for the share sheet.
    @State private var writing = false
    /// The file the share sheet has, until it closes.
    @State private var shared: URL?
    private let log = Logger(subsystem: DiagnosticsReport.subsystem, category: "settings-page")

    var body: some View {
        SettingsGroup(title: SettingsText.aboutSection, footer: SettingsText.shareDiagnosticsDetail) {
            NavigationLink(value: PickerRoute.help) {
                SettingsRow(symbol: "questionmark.circle", title: HelpText.title, end: .opens)
            }
            .buttonStyle(SettingsRowStyle())
            SettingsDivider()
            Button(action: share) {
                SettingsRow(symbol: "square.and.arrow.up", title: SettingsText.shareDiagnostics, end: writing ? .busy : nil)
            }
            .buttonStyle(SettingsRowStyle())
            .disabled(writing || shared != nil)
            .background(ShareSheet(file: $shared))
            SettingsDivider()
            NavigationLink(value: PickerRoute.privacy) {
                SettingsRow(symbol: "hand.raised", title: PrivacyText.title, end: .opens)
            }
            .buttonStyle(SettingsRowStyle())
            SettingsDivider()
            NavigationLink(value: PickerRoute.licenses) {
                SettingsRow(symbol: "doc.text", title: PrivacyText.licensesTitle, end: .opens)
            }
            .buttonStyle(SettingsRowStyle())
            SettingsDivider()
            SettingsRow(symbol: "info.circle", title: SettingsText.version, value: AccountText.versionNumber(Bundle.main.infoDictionary))
                .accessibilityElement(children: .combine)
        }
    }

    /// Writes the file, then hands it to the share sheet. Location access is
    /// read here, on the main thread, and the rest off it.
    private func share() {
        writing = true
        let grants = LocationGrants.read()
        let file = DiagnosticsFile(host: AccountText.serverText(session.settings.serverURL))
        Task {
            defer { writing = false }
            do {
                shared = try await file.write(grants: grants)
            } catch {
                log.error("could not write the diagnostics file: \(String(describing: error), privacy: .public)")
            }
        }
    }
}

/// The system share sheet with one file. UIKit's, not SwiftUI's ShareLink:
/// only UIKit's says when the sheet has closed, and then the file is
/// deleted.
private struct ShareSheet: UIViewControllerRepresentable {
    @Binding var file: URL?

    func makeUIViewController(context: Context) -> UIViewController {
        UIViewController()
    }

    func updateUIViewController(_ host: UIViewController, context: Context) {
        guard let url = file, host.presentedViewController == nil else { return }
        let sheet = UIActivityViewController(activityItems: [url], applicationActivities: nil)
        sheet.popoverPresentationController?.sourceView = host.view
        // Called once the sheet has closed, whether the driver sent the file
        // or not.
        sheet.completionWithItemsHandler = { _, _, _, _ in
            DiagnosticsFile.remove(url)
            file = nil
        }
        host.present(sheet, animated: true)
    }
}

/// Rows joined under a title, as the system's inset grouped lists. Not a
/// `List`: the header above them has to scroll with them and fold.
private struct SettingsGroup<Rows: View>: View {
    let title: String
    var footer: String?
    @ViewBuilder let rows: Rows

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            SettingsGroupTitle(title: title)
                .padding(.top, 28)
            VStack(spacing: 0) { rows }
                .settingsCard()
            if let footer {
                Text(footer)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .padding(EdgeInsets(top: 8, leading: 20, bottom: 0, trailing: 20))
            }
        }
    }
}

/// A group's title, marked as a heading so VoiceOver's rotor can jump to it.
struct SettingsGroupTitle: View {
    let title: String

    var body: some View {
        Text(title)
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(.secondary)
            .accessibilityAddTraits(.isHeader)
            .padding(EdgeInsets(top: 0, leading: 20, bottom: 8, trailing: 20))
    }
}

extension View {
    /// The card a group's rows are on.
    func settingsCard() -> some View {
        background(Color(.secondarySystemGroupedBackground))
            .clipShape(.rect(cornerRadius: groupCorner))
    }
}

private struct SettingsDivider: View {
    var body: some View {
        Divider().padding(.leading, SettingsRow.wordsInset)
    }
}

/// A row: its symbol, its title, its value at the end or, where both don't
/// fit, under the title, and what is at its end.
private struct SettingsRow: View {
    enum End {
        /// An arrow: it opens something.
        case opens
        /// Two small arrows: it opens a menu to choose from.
        case chooses
        case busy
    }

    let symbol: String
    var tint = Color.secondary
    let title: String
    var value: String?
    var end: End?

    @Environment(\.dynamicTypeSize) private var typeSize

    private static let symbolSide: CGFloat = 24
    static let wordsInset: CGFloat = 16 + symbolSide + 14

    var body: some View {
        // At the largest sizes the words run to many lines, and the symbol
        // stays by the first.
        HStack(alignment: typeSize.isAccessibilitySize ? .firstTextBaseline : .center, spacing: 14) {
            Image(systemName: symbol)
                .font(.system(size: 19))
                .foregroundStyle(tint)
                .frame(width: Self.symbolSide, height: Self.symbolSide)
                .accessibilityHidden(true)
            words
                .frame(maxWidth: .infinity, alignment: .leading)
            switch end {
            case .opens: arrow("chevron.forward")
            case .chooses: arrow("chevron.up.chevron.down")
            case .busy: ProgressView()
            case nil: EmptyView()
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .frame(minHeight: 52)
    }

    private func arrow(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.footnote.weight(.semibold))
            .foregroundStyle(.tertiary)
            .accessibilityHidden(true)
    }

    @ViewBuilder private var words: some View {
        if let value {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) {
                    Text(title).fixedSize()
                    Spacer(minLength: 0)
                    Text(value).fixedSize().foregroundStyle(.secondary)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                    Text(value).font(.subheadline).foregroundStyle(.secondary)
                }
            }
        } else {
            Text(title)
        }
    }
}

struct SettingsRowStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(.primary)
            .contentShape(.rect)
            .background(configuration.isPressed ? Color(.systemFill) : .clear)
    }
}
