import Foundation

/// The claims in a JWT's payload, read without checking the signature: the
/// token came from the server the phone signed in to, and what is read here
/// only decides what the phone tells the person holding it. The server checks
/// every token it is sent.
nonisolated enum JWTClaims {
    /// The string claim `name` in `token`. Nil when the token has no payload,
    /// the payload is not base64url JSON, or the claim is missing or not a
    /// string.
    static func string(_ name: String, in token: String) -> String? {
        let parts = token.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count > 1 else { return nil }
        // Base64url without padding (RFC 7515 §2), which Foundation reads only
        // as plain base64 with its padding.
        var payload = String(parts[1]).replacing("-", with: "+").replacing("_", with: "/")
        payload += String(repeating: "=", count: (4 - payload.count % 4) % 4)
        guard let data = Data(base64Encoded: payload),
              let claims = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        return claims[name] as? String
    }
}

extension StoredToken {
    /// The server's name for the admin site's read-only role.
    nonisolated static let viewerRole = "viewer"

    /// Whether the account signed in is a viewer's: one the server lets look
    /// at its admin site and refuses every driver call with a 403. Read from
    /// the access token's `role` claim.
    nonisolated var isViewer: Bool {
        JWTClaims.string("role", in: token) == Self.viewerRole
    }
}
