import SwiftUI
import UIKit

/// Sign in (DESIGN-iOS §4.1): the app's icon and title, the server as one
/// line once the phone knows it, Email and Password, then the button. An
/// error shows under the field it is about, or in a banner above the button
/// when no one field is to blame. Nothing the system says is shown: the
/// session logs the cause, and the driver reads one fixed sentence.
///
/// Signing in again during a trip (§4.9) is the same screen, as on Android:
/// the open trip takes the icon's place, the server and email are the trip's
/// own, and only the password is left to type.
struct SignInView: View {
    @Environment(TripSession.self) private var session
    @Environment(ServerSetup.self) private var serverSetup
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    let isAgain: Bool
    /// Signing in again succeeded.
    var onSignedIn: () -> Void = {}
    @State private var form: SignInForm
    @State private var server = ""
    @State private var email = ""
    @State private var password = ""
    @State private var signing: Task<Void, Never>?
    @FocusState private var focus: SignInField?

    init(isAgain: Bool = false, onSignedIn: @escaping () -> Void = {}) {
        self.isAgain = isAgain
        self.onSignedIn = onSignedIn
        _form = State(initialValue: SignInForm(isAgain: isAgain))
    }

    private var settings: AppSettings { session.settings }

    /// The server to sign in to: on Sign in, the agency's, the last one or the
    /// built-in one; signing in again, the open trip's.
    private var savedServer: URL? { isAgain ? session.currentServerURL : settings.signInServerURL }

    var body: some View {
        ScrollViewReader { proxy in
            Form {
                Section {
                    if isAgain, let trip = session.activeTrip?.trip {
                        againHeader(trip)
                    } else {
                        header
                    }
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
                serverSection
                    .disabled(form.isSigningIn)
                Section {
                    LabeledField(label: SignInText.email, error: form.fieldError(for: .email)) {
                        TextField(SignInText.email, text: $email, prompt: Text(verbatim: ""))
                            .accessibilityLabel(SignInText.email)
                            .textContentType(.username)
                            .keyboardType(.emailAddress)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .submitLabel(.next)
                            .focused($focus, equals: .email)
                            .onSubmit { focus = .password }
                            .fieldError(form.fieldError(for: .email))
                            // The trip and its pending ends belong to the
                            // driver who started it, so another driver can't
                            // take it over here. It stays a field for
                            // Password AutoFill.
                            .disabled(isAgain)
                    }
                    LabeledField(label: SignInText.password, error: form.fieldError(for: .password)) {
                        PasswordField(text: $password, focus: $focus, error: form.fieldError(for: .password), onSubmit: signIn)
                    }
                }
                .disabled(form.isSigningIn)
                if let banner = form.banner {
                    Section {
                        AttentionBanner(symbol: banner.symbol, text: banner.text)
                    }
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                }
                Section {
                    WideButton(title: isAgain ? SignInText.signInAndKeepReporting : SignInText.signIn,
                               busyTitle: form.isSigningIn ? SignInText.signingIn : nil, action: signIn)
                        .id(Anchor.button)
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            }
            .listSectionSpacing(.compact)
            // There is no bar over the form, so the header fades under the
            // status bar as it scrolls, rather than running into the clock.
            .scrollEdgeEffectStyle(.soft, for: .top)
            // The Form keeps the focused field above the keyboard by itself;
            // this keeps the button under Password there too, as Android does.
            .onChange(of: focus) { _, focused in
                if focused == .password { showButton(proxy) }
            }
            .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardDidShowNotification)) { _ in
                if focus == .password { showButton(proxy) }
            }
        }
        .background {
            EditWatcher(server: $server, email: $email, password: $password) {
                if form.failure != nil { animated { form.edited() } }
            }
        }
        .onChange(of: form.failure) { _, failure in
            if failure != nil, let said = form.banner?.text ?? SignInField.allCases.lazy.compactMap(form.fieldError).first {
                AccessibilityNotification.Announcement(said).post()
            }
        }
        .onChange(of: savedServer, initial: true) { _, url in
            server = url?.absoluteString ?? ""
        }
        // A setup link's server is filled in for this sign-in, in the open
        // field, so the driver sees the address before using it.
        .onChange(of: serverSetup.linkedServer) { _, url in
            guard !isAgain, let url else { return }
            server = url.absoluteString
            form.changeServer()
        }
        .onAppear {
            email = settings.email
            if isAgain { focus = .password }
        }
        .onDisappear { signing?.cancel() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            SignInIcon()
                .padding(.bottom, 16)
            Text(SignInText.title)
                .font(.largeTitle.bold())
                .accessibilityAddTraits(.isHeader)
            Text(SignInText.subtitle)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.bottom, 16)
    }

    /// The trip that is still open, then whose sign-in it is.
    private func againHeader(_ trip: TripGeometry) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            TripStillOpenCard(trip: trip)
            Text(SignInText.signingInAs(settings.email))
                .foregroundStyle(.secondary)
        }
        .padding(.bottom, 8)
    }

    @ViewBuilder private var serverSection: some View {
        let managed = settings.serverIsManaged
        if isAgain {
            // The open trip is on this server, so it isn't for changing here.
            Section {
                SavedServerRow(label: SignInForm.serverLabel(server))
            }
        } else if form.showsServerField(savedServer: savedServer, managedInvalid: settings.managedServerURLIsInvalid) {
            Section {
                VStack(alignment: .leading, spacing: 6) {
                    TextField(SignInText.serverAddress, text: $server, prompt: Text(verbatim: SignInText.serverPlaceholder))
                        .accessibilityLabel(SignInText.serverAddress)
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.next)
                        .focused($focus, equals: .server)
                        .onSubmit { focus = .email }
                        .disabled(managed)
                        .foregroundStyle(managed ? .secondary : .primary)
                        .fieldError(form.fieldError(for: .server))
                    if let error = form.fieldError(for: .server) {
                        FieldError(text: error)
                    }
                }
            } header: {
                Text(SignInText.serverAddress)
            } footer: {
                managedFooter
            }
        } else {
            Section {
                SavedServerRow(label: SignInForm.serverLabel(server), onChange: managed ? nil : {
                    form.changeServer()
                    focus = .server
                })
            } footer: {
                managedFooter
            }
        }
    }

    @ViewBuilder private var managedFooter: some View {
        if settings.serverIsManaged {
            Text(SignInText.setByAgency)
        } else if settings.managedServerURLIsInvalid {
            Text(SignInText.managedServerInvalid)
        }
    }

    private func signIn() {
        let url = animated { form.begin(server: server, email: email, password: password) }
        guard let url else {
            if let field = form.focus { focus = field }
            return
        }
        AccessibilityNotification.Announcement(SignInText.signingIn).post()
        let typed = server
        signing = Task {
            do {
                if isAgain {
                    try await session.reauthenticate(password: password)
                } else {
                    try await session.signIn(serverURL: url, email: email, password: password)
                }
                form.succeeded()
                onSignedIn()
            } catch {
                // The screen went away mid-sign-in; there is nothing to show.
                guard !Task.isCancelled else { return }
                animated { form.failed(error, server: typed) }
            }
        }
    }

    private func showButton(_ proxy: ScrollViewProxy) {
        withAnimation(reduceMotion ? nil : .smooth(duration: 0.25)) {
            proxy.scrollTo(Anchor.button, anchor: .bottom)
        }
    }

    /// Errors and the banner come and go with a short move of what is below
    /// them; with Reduce Motion, at once.
    private func animated<Result>(_ change: () -> Result) -> Result {
        withAnimation(reduceMotion ? nil : .smooth(duration: 0.25), change)
    }

    private enum Anchor {
        case button
    }
}

/// Watches the three fields as one, in a view of its own: an edit to any of
/// them clears the error, and a keystroke re-runs only this and the field,
/// never the form around them.
private struct EditWatcher: View {
    @Binding var server: String
    @Binding var email: String
    @Binding var password: String
    let onEdit: () -> Void

    var body: some View {
        Color.clear
            .onChange(of: Typed(server: server, email: email, password: password)) { onEdit() }
    }

    private struct Typed: Equatable {
        let server: String
        let email: String
        let password: String
    }
}

/// A field with its name on the leading side, so the name stays in view once
/// the field is filled, and its error under both. At accessibility sizes the
/// name goes above the field rather than squeezing it.
private struct LabeledField<Field: View>: View {
    let label: String
    let error: String?
    @ViewBuilder let field: Field

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        let stacked = dynamicTypeSize.isAccessibilitySize
        let layout = stacked ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(spacing: 8))
        VStack(alignment: .leading, spacing: 6) {
            layout {
                // The field carries the same name for VoiceOver.
                Text(label)
                    .frame(width: stacked ? nil : 84, alignment: .leading)
                    .accessibilityHidden(true)
                field
            }
            if let error {
                FieldError(text: error)
            }
        }
    }
}
