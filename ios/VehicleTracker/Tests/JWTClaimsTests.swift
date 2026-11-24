import Foundation
import Testing
@testable import VehicleTracker

/// The role read from the server's access token, which tells a viewer's
/// account from a driver's.
@Suite struct JWTClaimsTests {
    @Test func aViewerIsToldByTheRoleInTheServersToken() {
        #expect(StoredToken(token: fakeJWT(role: "viewer"), issuedAt: .now).isViewer)
        #expect(!StoredToken(token: fakeJWT(role: "driver"), issuedAt: .now).isViewer)
        #expect(!StoredToken(token: fakeJWT(role: "admin"), issuedAt: .now).isViewer)
        #expect(JWTClaims.string("sub", in: fakeJWT(role: "viewer")) == "7")
    }

    /// The server writes base64url without padding, which Foundation does not
    /// read as it is.
    @Test func thePayloadIsReadAsBase64urlWithoutPadding() throws {
        let token = fakeJWT(claims: #"{"sub":"7","email":"m~~~???@metro.example","role":"viewer"}"#)
        let payload = token.split(separator: ".")[1]
        try #require(payload.contains("-") && payload.contains("_") && payload.count % 4 != 0)
        #expect(JWTClaims.string("email", in: token) == "m~~~???@metro.example")
        #expect(StoredToken(token: token, issuedAt: .now).isViewer)
    }

    @Test(arguments: [
        "jwt",
        "header..signature",
        "header.not base64!.signature",
        fakeJWT(claims: "not json"),
        fakeJWT(claims: #"["role","viewer"]"#),
        fakeJWT(claims: #"{"sub":"7","role":7}"#),
        fakeJWT(claims: #"{"sub":"7"}"#),
    ])
    func aTokenWithNoReadableRoleIsNotAViewers(token: String) {
        #expect(JWTClaims.string("role", in: token) == nil)
        #expect(!StoredToken(token: token, issuedAt: .now).isViewer)
    }
}
