import Foundation
import Security

/// A driver JWT and when it was issued. The server's tokens live 24 hours and
/// there is no refresh, so freshness is judged locally to skip the login
/// screen (spec §5.2).
nonisolated struct StoredToken: Sendable, Codable, Equatable {
    var token: String
    var issuedAt: Date

    func isFresh(now: Date, lifetime: TimeInterval = 24 * 3600) -> Bool {
        now.timeIntervalSince(issuedAt) < lifetime
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
