import Foundation
import Testing
@testable import VehicleTracker

/// A port of the sign-in cases in Android's `ViewModelsTest` and the
/// `fieldErrorRes` / `bannerOf` tables, plus the iOS-only rows: 429, the
/// http:// rule and a server the agency sets.
@Suite struct SignInFormTests {
    let server = "https://tracker.example.org"
    let email = "jordan.lee@example.org"

    func checked(_ server: String, _ email: String, _ password: String) -> SignInFailure? {
        if case .failure(let failure) = SignInForm.check(server: server, email: email, password: password) { return failure }
        return nil
    }

    /// A form whose sign-in went out and came back with `error`.
    func failed(_ error: any Error, isAgain: Bool = false, server: String = "https://tracker.example.org") -> SignInForm {
        var form = SignInForm(isAgain: isAgain)
        #expect(form.begin(server: server, email: email, password: "pw") != nil)
        form.failed(error, server: server)
        return form
    }

    // MARK: Checked before anything is sent

    @Test func blankFieldsAreNamedInTheOrderServerEmailPassword() {
        #expect(checked("", "", "") == .missing(.server))
        #expect(checked(server, "", "pw") == .missing(.email))
        #expect(checked(server, email, " ") == .missing(.password))
        #expect(checked("   ", email, "pw") == .missing(.server))
        #expect(checked(server, " \n", "pw") == .missing(.email))
    }

    @Test func anAddressWithNoHttpOrHttpsIsUnreachableAndNothingIsSent() {
        var form = SignInForm()
        #expect(form.begin(server: "tracker.example.org", email: email, password: "pw") == nil)
        #expect(form.failure == .unreachable("tracker.example.org"))
        #expect(!form.isSigningIn)
        #expect(checked("ftp://tracker.example.org", email, "pw") == .unreachable("ftp://tracker.example.org"))
        #expect(checked("https://", email, "pw") == .unreachable("https://"))
    }

    @Test func plainHttpIsRefusedExceptOnThisPhoneOrTheLocalNetwork() {
        #expect(checked("http://tracker.example.org", email, "pw") == .insecure)
        #expect(SignInForm.check(server: "http://localhost:8080", email: email, password: "pw")
            == .success(URL(string: "http://localhost:8080")!))
        #expect(SignInForm.check(server: "http://dev.local", email: email, password: "pw")
            == .success(URL(string: "http://dev.local")!))
    }

    @Test func theServerIsTrimmedBeforeUse() {
        #expect(SignInForm.check(server: " https://tracker.example.org ", email: email, password: "pw")
            == .success(URL(string: "https://tracker.example.org")!))
    }

    @Test func aGoodFormIsSentOnceWhateverTheTaps() {
        var form = SignInForm()
        #expect(form.begin(server: server, email: email, password: "pw") == URL(string: server)!)
        #expect(form.isSigningIn)
        #expect(form.begin(server: server, email: email, password: "pw") == nil)
        form.succeeded()
        #expect(!form.isSigningIn)
    }

    // MARK: What came back

    @Test func eachFailureIsSortedByWhatTheDriverCanDo() {
        #expect(failed(APIError.status(401, message: "invalid email or password")).failure == .invalidCredentials)
        #expect(failed(APIError.status(429, message: "too many attempts")).failure == .tooManyTries)
        for code in [403, 409, 500, 503] {
            #expect(failed(APIError.status(code, message: "")).failure == .serverError)
        }
        #expect(failed(APIError.decoding("keyNotFound")).failure == .serverError)
        #expect(failed(APIError.transport("offline"), server: "https://tracker.example.org:8443/").failure
            == .unreachable("tracker.example.org:8443"))
        #expect(failed(APIError.insecureURL).failure == .insecure)
        #expect(failed(CocoaError(.fileWriteUnknown)).failure == .serverError)
    }

    @Test func aFailedSignInCanBeTriedAgain() {
        var form = failed(APIError.status(500, message: ""))
        #expect(!form.isSigningIn)
        #expect(form.begin(server: server, email: email, password: "pw") != nil)
        #expect(form.failure == nil)
    }

    @Test func anUnreachableServerOpensItsFieldOnSignInButNotOnSignInAgain() {
        #expect(failed(APIError.transport("offline")).changingServer)
        #expect(!failed(APIError.transport("offline"), isAgain: true).changingServer)
        #expect(!failed(APIError.status(401, message: "")).changingServer)
    }

    @Test func anEditClearsTheErrorButLeavesTheServerFieldOpen() {
        var form = failed(APIError.transport("offline"))
        form.edited()
        #expect(form.failure == nil)
        #expect(form.changingServer)
    }

    @Test func changeOpensTheServerField() {
        var form = SignInForm()
        #expect(!form.changingServer)
        form.changeServer()
        #expect(form.changingServer)
    }

    // MARK: Where each failure shows

    /// One line per failure: the error under each field, then the banner.
    func placement(_ failure: SignInFailure) -> String {
        var form = SignInForm()
        switch failure {
        case .missing(let field):
            let typed: [SignInField: String] = [.server: server, .email: email, .password: "pw"]
            _ = form.begin(server: field == .server ? "" : typed[.server]!, email: field == .email ? "" : typed[.email]!,
                           password: field == .password ? "" : typed[.password]!)
        case .invalidCredentials: form = failed(APIError.status(401, message: ""))
        case .unreachable: form = failed(APIError.transport("offline"))
        case .tooManyTries: form = failed(APIError.status(429, message: ""))
        case .insecure: form = failed(APIError.insecureURL)
        case .serverError: form = failed(APIError.status(500, message: ""))
        }
        #expect(form.failure == failure)
        let fields = SignInField.allCases.map { form.fieldError(for: $0) ?? "-" }.joined(separator: " | ")
        let banner = form.banner.map { "\($0.symbol) \($0.text)" } ?? "-"
        return "\(fields) || \(banner)"
    }

    @Test func eachFailureShowsUnderItsFieldOrInTheBanner() {
        #expect(placement(.missing(.server)) == "Enter the server address | - | - || -")
        #expect(placement(.missing(.email)) == "- | Enter your email | - || -")
        #expect(placement(.missing(.password)) == "- | - | Enter your password || -")
        #expect(placement(.invalidCredentials) == "- | - | Wrong email or password. Try again. || -")
        #expect(placement(.unreachable("tracker.example.org"))
            == "Check the address | - | - || icloud.slash.fill Can't reach tracker.example.org. Check your connection, or the server address.")
        #expect(placement(.tooManyTries)
            == "- | - | - || exclamationmark.circle.fill Too many tries. Wait a minute, then try again.")
        #expect(placement(.insecure)
            == "Use https://. Plain http:// is allowed only for localhost, 127.0.0.1 or a .local host. | - | - || -")
        #expect(placement(.serverError) == "- | - | - || exclamationmark.circle.fill Couldn't sign in. Try again.")
    }

    @Test func onlyAnEmptyFieldTakesTheFocus() {
        var form = SignInForm()
        _ = form.begin(server: server, email: "", password: "pw")
        #expect(form.focus == .email)
        #expect(failed(APIError.status(401, message: "")).focus == nil)
        #expect(failed(APIError.transport("offline")).focus == nil)
        #expect(SignInForm().focus == nil)
    }

    // MARK: The server line

    @Test func theServerIsOneLineOnceKnownUntilChangedOrUnusable() {
        let known = URL(string: server)!
        #expect(SignInForm().showsServerField(savedServer: nil, managedInvalid: false))
        #expect(!SignInForm().showsServerField(savedServer: known, managedInvalid: false))
        var changing = SignInForm()
        changing.changeServer()
        #expect(changing.showsServerField(savedServer: known, managedInvalid: false))
        // The agency's server is the saved one, and isn't typed over.
        #expect(!SignInForm().showsServerField(savedServer: URL(string: "https://agency.example.org")!, managedInvalid: false))
        #expect(SignInForm().showsServerField(savedServer: known, managedInvalid: true))
    }

    @Test func theServerIsNamedAsTheDriverKnowsIt() {
        #expect(SignInForm.serverLabel("https://tracker.example.org/") == "tracker.example.org")
        #expect(SignInForm.serverLabel("http://tracker.example.org:80") == "tracker.example.org")
        #expect(SignInForm.serverLabel("https://tracker.example.org:443") == "tracker.example.org")
        #expect(SignInForm.serverLabel("https://tracker.example.org:80") == "tracker.example.org:80")
        #expect(SignInForm.serverLabel("http://10.0.2.2:8081") == "10.0.2.2:8081")
        #expect(SignInForm.serverLabel(" tracker.example.org ") == "tracker.example.org")
        #expect(SignInForm.serverLabel("https://Tracker.Example.org/api") == "tracker.example.org")
    }
}
