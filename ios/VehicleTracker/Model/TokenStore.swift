import Foundation
import Security

/// The driver's access token (a JWT), when it was issued and expires, the
/// refresh token that renews it, and the sign-in they belong to.
/// `refreshToken`, `expiresAt` and `signInID` are nil for a token saved by a
/// build that did not keep them.
nonisolated struct StoredToken: Sendable, Codable, Equatable {
    var token: String
    var issuedAt: Date
    var refreshToken: String? = nil
    var expiresAt: Date? = nil
    /// New for each sign-in and kept by every refresh after it, so a request
    /// can tell a token renewed for its own driver from the next driver's.
    /// Decoding never makes one up: each read of the same item would then be
    /// a different sign-in, and every retry would be refused.
    var signInID: UUID? = UUID()

    /// The same tokens with the same times. The sign-in id is left out: it is
    /// random, so tokens built from the same reply would never be equal.
    /// Whether two tokens are from one sign-in is asked of `signInID`.
    static func == (lhs: StoredToken, rhs: StoredToken) -> Bool {
        lhs.token == rhs.token && lhs.issuedAt == rhs.issuedAt
            && lhs.refreshToken == rhs.refreshToken && lhs.expiresAt == rhs.expiresAt
    }

    /// How long builds before refresh took a token to last. A token they saved
    /// has no expiry and no refresh token, so it is held to the same 24 hours
    /// from sign-in.
    static let oldBuildLifetime: TimeInterval = 24 * 3600

    /// Whether requests can go out without the driver signing in again: there
    /// is a refresh token to renew the access token with, or time left on it.
    /// A refresh token the server will refuse still counts until a request
    /// finds that out and drops it.
    func isSignedIn(now: Date) -> Bool {
        refreshToken != nil || now < (expiresAt ?? issuedAt.addingTimeInterval(Self.oldBuildLifetime))
    }

    /// The earliest a refresh goes out before the access token expires.
    static let maxEarlyRefresh: TimeInterval = 60

    /// When to renew the access token before it expires, so requests don't
    /// meet a 401: a minute early, or halfway through a lifetime shorter than
    /// two minutes. Nil without a refresh token or an expiry.
    var refreshDueAt: Date? {
        guard refreshToken != nil, let expiresAt else { return nil }
        return expiresAt.addingTimeInterval(-min(Self.maxEarlyRefresh, expiresAt.timeIntervalSince(issuedAt) / 2))
    }
}

extension StoredToken {
    /// The tokens a sign-in or a refresh handed out, timed from `receivedAt`,
    /// the moment the reply arrived. `expires_in` is relative, so a phone
    /// clock that disagrees with the server's does not move the expiry.
    nonisolated init(_ issued: AuthTokens, receivedAt: Date) {
        self.init(token: issued.accessToken, issuedAt: receivedAt, refreshToken: issued.refreshToken,
                  expiresAt: receivedAt.addingTimeInterval(TimeInterval(issued.expiresIn)))
    }
}

nonisolated protocol TokenStoring: Sendable {
    func load() throws -> StoredToken?
    func save(_ token: StoredToken) throws
    func clear() throws
}

nonisolated enum KeychainError: Error, Equatable {
    case status(OSStatus)
}

/// One generic-password item, readable after first unlock, never synced.
nonisolated final class KeychainTokenStore: TokenStoring, Sendable {
    private let service: String
    private let account: String

    init(service: String = "org.onebusaway.vehicletracker", account: String = "driver-token") {
        self.service = service
        self.account = account
    }

    func load() throws -> StoredToken? {
        var query = itemQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        switch status {
        case errSecSuccess:
            guard let data = item as? Data else { throw KeychainError.status(errSecInvalidData) }
            do {
                return try JSONCoding.decoder.decode(StoredToken.self, from: data)
            } catch {
                // Written by an incompatible build: treat as signed out.
                try? clear()
                return nil
            }
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.status(status)
        }
    }

    func save(_ token: StoredToken) throws {
        let data = try JSONCoding.encoder.encode(token)
        let updated = SecItemUpdate(itemQuery as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        switch updated {
        case errSecSuccess:
            return
        case errSecItemNotFound:
            var attributes = itemQuery
            attributes[kSecValueData as String] = data
            attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let added = SecItemAdd(attributes as CFDictionary, nil)
            guard added == errSecSuccess else { throw KeychainError.status(added) }
        default:
            throw KeychainError.status(updated)
        }
    }

    func clear() throws {
        let status = SecItemDelete(itemQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError.status(status) }
    }

    private var itemQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}

/// Forgets everything when the process exits; for tests and previews.
nonisolated final class InMemoryTokenStore: TokenStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var token: StoredToken?

    init(_ initial: StoredToken? = nil) { token = initial }

    func load() throws -> StoredToken? { lock.withLock { token } }
    func save(_ token: StoredToken) throws { lock.withLock { self.token = token } }
    func clear() throws { lock.withLock { token = nil } }
}
