import Foundation
import Testing
@testable import VehicleTracker

@Suite struct ServerURLPolicyTests {
    @Test(arguments: [
        ("https://positions.example.org", true),
        ("https://positions.example.org/prefix", true),
        ("http://localhost:8080", true),
        ("http://127.0.0.1:8080", true),
        ("http://aarons-mac.local:8080", true),
        ("http://positions.example.org", false),
        ("http://10.0.0.5:8080", false),
        ("ftp://positions.example.org", false),
    ])
    func policy(_ raw: String, _ allowed: Bool) {
        #expect(ServerURLPolicy.isAllowed(URL(string: raw)!) == allowed, Comment(rawValue: raw))
    }
}
