import CarPlay
import Testing
@testable import VehicleTracker

@Suite struct CarPlayTemplatesTests {
    @Test func titleButtonIsDisabledText() {
        let b = CarPlayTemplates.titleButton("Sign in on iPhone")
        #expect(b.title == "Sign in on iPhone")
        #expect(!b.isEnabled)
    }

    @Test func signedOutHasNoStartButton() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .signedOut, onStart: {})
        #expect(leading.map(\.title) == ["Sign in on iPhone"])
        #expect(trailing.isEmpty)
    }

    @Test func idleOffersStart() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .idle, onStart: {})
        #expect(leading.map(\.title) == ["OBA Vehicle Tracker"])
        #expect(trailing.map(\.title) == ["Start trip"])
        #expect(trailing[0].isEnabled)
    }

    @Test func startingShowsProgressAndNoStart() {
        let (leading, trailing) = CarPlayTemplates.idleBarButtons(phase: .starting, onStart: {})
        #expect(leading.map(\.title) == ["Starting…"])
        #expect(trailing.isEmpty)
    }
}
