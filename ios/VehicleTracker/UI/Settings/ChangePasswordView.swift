import SwiftUI

/// Change password (the design's sheet 09): the current password, the new one
/// twice, and one button at the foot, over the keyboard. A line under a field
/// says what is wrong with it, and a banner on top what no field is to blame
/// for. While the change is out the fields and Back wait.
struct ChangePasswordView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.dynamicTypeSize) private var typeSize
    /// The password was changed; Settings says so.
    let onChanged: () -> Void
    @State private var form = PasswordChange()
    @State private var current = ""
    @State private var new = ""
    @State private var again = ""
    @State private var changing: Task<Void, Never>?
    @FocusState private var focus: PasswordChangeField?
    /// The field typed in last. The eye moves the focus into a field of its
    /// own, so it is no field of the form's for a moment, and that is not
    /// leaving New password.
    @State private var typingIn: PasswordChangeField?

    var body: some View {
        Form {
            if let banner = form.banner {
                Section {
                    AttentionBanner(symbol: banner.symbol, text: banner.text)
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            }
            Section {
                field(.current, ChangePasswordText.current, text: $current, contentType: .password, submit: .next)
            } footer: {
                if let error = form.error(for: .current) {
                    FieldError(text: error)
                }
            }
            Section {
                field(.new, ChangePasswordText.new, text: $new, contentType: .newPassword, submit: .next)
                field(.again, ChangePasswordText.again, text: $again, contentType: .newPassword, submit: .go)
            } footer: {
                VStack(alignment: .leading, spacing: 8) {
                    let errors = [form.error(for: .new), form.error(for: .again)].compactMap { $0 }
                    if errors.isEmpty {
                        Text(ChangePasswordText.rule)
                    } else {
                        ForEach(errors, id: \.self) { FieldError(text: $0) }
                    }
                    Text(ChangePasswordText.othersSignedOut)
                }
            }
        }
        .disabled(form.isChanging)
        .safeAreaInset(edge: .bottom) {
            WideButton(title: ChangePasswordText.title, busyTitle: form.isChanging ? ChangePasswordText.changing : nil, action: change)
                .disabled(!PasswordChange.canSend(current: current, new: new, again: again) && !form.isChanging)
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
        }
        .navigationTitle(ChangePasswordText.title)
        // A long title wraps rather than shrinking at accessibility sizes.
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
        .navigationBarBackButtonHidden(form.isChanging)
        .onChange(of: current) { edited(.current) }
        .onChange(of: new) { edited(.new) }
        .onChange(of: again) { edited(.again) }
        .onChange(of: focus) { _, now in
            guard let now else { return }
            if typingIn == .new && now != .new { animated { form.leftNew(new) } }
            typingIn = now
        }
        .onChange(of: form.said) { _, said in
            if let said { AccessibilityNotification.Announcement(said).post() }
        }
        .onDisappear { changing?.cancel() }
    }

    private func field(_ field: PasswordChangeField, _ name: String, text: Binding<String>, contentType: UITextContentType,
                       submit: SubmitLabel) -> some View {
        PasswordField(name: name, text: text, focus: $focus, field: field, contentType: contentType, prompted: true,
                      submitLabel: submit, error: form.error(for: field)) {
            switch field {
            case .current: focus = .new
            case .new: focus = .again
            case .again: change()
            }
        }
    }

    private func edited(_ field: PasswordChangeField) {
        var after = form
        after.edited(field)
        if after != form { animated { form = after } }
    }

    private func change() {
        let begun = animated { form.begin(current: current, new: new, again: again) }
        guard begun else { return }
        focus = nil
        let server = SignInForm.serverLabel(session.currentServerURL?.absoluteString ?? "")
        changing = Task {
            do {
                try await session.changePassword(current: current, new: new)
                guard !Task.isCancelled else { return }
                form.succeeded()
                onChanged()
            } catch {
                guard !Task.isCancelled, !session.handleIfUnauthorized(error) else { return }
                animated { form.failed(error, server: server) }
            }
        }
    }

    /// Lines and the banner come and go with a short move of what is below
    /// them; with Reduce Motion, at once.
    private func animated<Result>(_ change: () -> Result) -> Result {
        withAnimation(reduceMotion ? nil : .smooth(duration: 0.25), change)
    }
}
