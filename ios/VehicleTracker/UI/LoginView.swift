import SwiftUI

struct LoginView: View {
    @Environment(TripSession.self) private var session
    @State private var serverURL = ""
    @State private var email = ""
    @State private var password = ""
    @State private var error: String?
    @State private var signingIn = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    TextField("https://positions.example.org", text: $serverURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                Section("Driver") {
                    TextField("Email", text: $email)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("Password", text: $password)
                }
                if let error {
                    Section { Text(error).foregroundStyle(.red) }
                }
                Section {
                    Button(action: signIn) {
                        HStack {
                            Spacer()
                            if signingIn { ProgressView() } else { Text("Sign In").bold() }
                            Spacer()
                        }
                        .frame(minHeight: 44)
                    }
                    .disabled(signingIn || serverURL.isEmpty || email.isEmpty || password.isEmpty)
                }
            }
            .navigationTitle("OBA Vehicle Tracker")
            .onAppear {
                serverURL = session.settings.serverURLString
                email = session.settings.email
            }
        }
    }

    private func signIn() {
        guard let url = URL(string: serverURL.trimmingCharacters(in: .whitespaces)), ServerURLPolicy.isAllowed(url) else {
            error = ServerURLPolicy.explanation
            return
        }
        error = nil
        signingIn = true
        Task {
            defer { signingIn = false }
            do {
                try await session.signIn(serverURL: url, email: email, password: password)
            } catch APIError.status(401, _) {
                error = String(localized: "Wrong email or password.")
            } catch APIError.status(let code, let message) {
                error = "Server error \(code): \(message)"
            } catch {
                self.error = String(localized: "Could not reach the server: \(error.localizedDescription)")
            }
        }
    }
}
