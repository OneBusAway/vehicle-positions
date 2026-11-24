import Foundation

/// A token shaped like the server's: a JWT whose payload is `claims`, in
/// base64url without padding. Nothing reads the header or the signature.
nonisolated func fakeJWT(claims: String) -> String {
    let payload = Data(claims.utf8).base64EncodedString()
        .replacing("+", with: "-").replacing("/", with: "_").replacing("=", with: "")
    return "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.\(payload).c2lnbmF0dXJl"
}

/// The claims the server signs into an access token, for an account with `role`.
nonisolated func fakeJWT(role: String) -> String {
    fakeJWT(claims: #"{"sub":"7","email":"d@test.com","role":"\#(role)","iss":"vehicle-positions-api"}"#)
}
