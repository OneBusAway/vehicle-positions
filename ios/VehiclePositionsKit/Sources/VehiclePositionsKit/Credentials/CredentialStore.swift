import Foundation

/// What a device needs to keep between launches to stay the same rider.
/// `riderID` and `token` are absent until registration succeeds.
public struct RiderCredentials: Sendable, Codable, Equatable {
    public var installationID: String
    public var riderID: String?
    public var token: String?

    public init(installationID: String, riderID: String? = nil, token: String? = nil) {
        self.installationID = installationID
        self.riderID = riderID
        self.token = token
    }

    private enum CodingKeys: String, CodingKey {
        case installationID = "installation_id"
        case riderID = "rider_id"
        case token
    }
}

/// Where credentials live. The Keychain-backed store is the shipping
/// implementation; tests use ``InMemoryCredentialStore``.
public protocol CredentialStore: Sendable {
    func load() throws -> RiderCredentials?
    func save(_ credentials: RiderCredentials) throws
    func clear() throws
}

/// A credential store that forgets everything when the process exits.
public final class InMemoryCredentialStore: CredentialStore, @unchecked Sendable {
    private let lock = NSLock()
    private var credentials: RiderCredentials?

    public init(_ initial: RiderCredentials? = nil) {
        credentials = initial
    }

    public func load() throws -> RiderCredentials? {
        lock.withLock { credentials }
    }

    public func save(_ credentials: RiderCredentials) throws {
        lock.withLock { self.credentials = credentials }
    }

    public func clear() throws {
        lock.withLock { credentials = nil }
    }
}
