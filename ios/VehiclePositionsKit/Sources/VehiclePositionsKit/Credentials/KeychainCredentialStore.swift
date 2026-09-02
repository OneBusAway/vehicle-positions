#if os(iOS)
import Foundation
import Security

/// A Keychain call that failed for a reason the SDK cannot act on.
public enum KeychainError: Error, Equatable {
    case status(OSStatus)
}

/// The shipping ``CredentialStore``: one generic-password item, readable after
/// the first unlock and never synced off the device, holding the JSON-encoded
/// ``RiderCredentials``.
public final class KeychainCredentialStore: CredentialStore {
    private let service: String
    private let account: String

    public init(
        service: String = "org.onebusaway.vehiclepositionskit",
        account: String = "rider-credentials"
    ) {
        self.service = service
        self.account = account
    }

    public func load() throws -> RiderCredentials? {
        var query = itemQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        switch status {
        case errSecSuccess:
            guard let data = item as? Data else { throw KeychainError.status(errSecInvalidData) }
            do {
                return try JSONDecoder().decode(RiderCredentials.self, from: data)
            } catch is DecodingError {
                // An item written by an incompatible build. Throwing would make
                // every `start` fail forever; treat it as no credentials and
                // clear it, so the next call registers the device afresh.
                try? clear()
                return nil
            }
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.status(status)
        }
    }

    /// Delete then add: `SecItemUpdate` would need the item to exist already,
    /// and the accessibility attribute is set once, on the item we write.
    public func save(_ credentials: RiderCredentials) throws {
        let data = try JSONEncoder().encode(credentials)
        try clear()

        var attributes = itemQuery
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError.status(status) }
    }

    public func clear() throws {
        let status = SecItemDelete(itemQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }

    /// Identifies the one item this store owns; every call starts from it.
    private var itemQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}
#endif
