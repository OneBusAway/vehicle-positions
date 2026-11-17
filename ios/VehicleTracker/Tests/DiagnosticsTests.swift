import Foundation
import OSLog
import Testing
@testable import VehicleTracker

/// What Share diagnostics sends: the app, the iPhone, the server, the
/// location grants and recent errors, and never a position, a token, the
/// email, or a vehicle or trip id (walkthrough spec §9).
@Suite struct DiagnosticsTests {
    let device = DeviceFacts(version: "1.0.0 (7)", system: "26.4", model: "iPhone17,3", location: .allowed,
                             precise: false, locationServicesOn: true)
    let made = Date(timeIntervalSince1970: 1_794_355_200) // 2026-11-11 00:00 UTC
    let india = TimeZone(identifier: "Asia/Kolkata")!

    func error(_ pieces: LoggedError.Piece..., category: String = "trip-session", after seconds: TimeInterval = 0) -> LoggedError {
        LoggedError(date: made.addingTimeInterval(seconds), category: category, pieces: pieces)
    }

    @Test func theTextNamesTheAppTheIPhoneTheServerAndWhatTheAppMayDo() {
        let text = DiagnosticsReport.text(made: made, timeZone: india, device: device, host: "tracker.example.org",
                                          errors: [error(.words("could not load the vehicles: "), .shown("status(500, message: \"\")"))])
        #expect(text == """
            Vehicle Tracker diagnostics
            Made: 2026-11-11 05:30 +05:30
            App: 1.0.0 (7)
            iOS: 26.4
            iPhone: iPhone17,3
            Server: tracker.example.org
            Location: allowed
            Precise location: not allowed
            Location services: on
            Errors since the app opened: 1
            2026-11-11 05:30:00 +05:30 trip-session: could not load the vehicles: status(500, message: "")

            """)
    }

    /// The rows of "Before you start", each in its own words.
    @Test(arguments: [
        (DeviceFacts.LocationGrant.allowed, "Location: allowed"),
        (.notAllowed, "Location: not allowed"),
        (.notAskedYet, "Location: not asked yet"),
        (.restricted, "Location: restricted"),
    ])
    func eachLocationGrantHasItsWords(grant: DeviceFacts.LocationGrant, line: String) {
        var facts = device
        facts.location = grant
        facts.precise = true
        facts.locationServicesOn = false
        let lines = DiagnosticsReport.text(made: made, timeZone: india, device: facts, host: "", errors: []).split(separator: "\n")
        #expect(lines.contains { $0 == line })
        #expect(lines.contains("Precise location: allowed"))
        #expect(lines.contains("Location services: off"))
        #expect(lines.contains("Server: none"), "signed out on a phone with no server")
        #expect(lines.contains("Errors since the app opened: none"))
    }

    /// Before location is allowed, iOS's precision means nothing.
    @Test func precisionIsNotApplicableUntilLocationIsAllowed() {
        var facts = device
        facts.location = .notAskedYet
        facts.precise = nil
        let lines = DiagnosticsReport.text(made: made, timeZone: india, device: facts, host: "h", errors: []).split(separator: "\n")
        #expect(lines.contains("Precise location: n/a"))
    }

    @Test func aLogThatCannotBeReadIsSaidSo() {
        let text = DiagnosticsReport.text(made: made, timeZone: india, device: device, host: "h", errors: nil)
        #expect(text.hasSuffix("Location services: on\nErrors: the app's log could not be read\n"))
    }

    /// The newest 50, oldest first, each with its time.
    @Test func onlyTheNewestFiftyErrorsGo() {
        let errors = (0..<60).map { error(.words("error \($0)"), after: TimeInterval($0)) }
        let lines = DiagnosticsReport.text(made: made, timeZone: india, device: device, host: "h", errors: errors)
            .split(separator: "\n")
        #expect(lines.contains("Errors since the app opened: 50"))
        let errorLines = lines.filter { $0.contains("trip-session: error ") }
        #expect(errorLines.count == 50)
        #expect(errorLines.first == "2026-11-11 05:30:10 +05:30 trip-session: error 10")
        #expect(errorLines.last == "2026-11-11 05:30:59 +05:30 trip-session: error 59")
    }

    @Test func eachErrorIsOneLineAndShort() {
        let long = error(.words("could not read: "), .shown("first\nsecond " + String(repeating: "word ", count: 100)))
        let line = DiagnosticsReport.line(long, timeZone: india)
        #expect(!line.contains("\n"))
        #expect(line.contains("first second"))
        #expect(line.count == DiagnosticsReport.lineLimit)
        #expect(line.hasSuffix("…"))
    }

    /// What the app logs privately (an id, a number) never shows; what it
    /// marks public does, scrubbed.
    @Test func onlyWhatTheAppMarkedPublicShows() {
        let line = DiagnosticsReport.line(error(.words("could not start trip "), .hidden, .words(" on vehicle "), .hidden,
                                                .words(": "), .shown("status(403, message: \"vehicle not assigned\")")),
                                          timeZone: india)
        #expect(line.hasSuffix("trip-session: could not start trip <private> on vehicle <private>: status(403, message: \"vehicle not assigned\")"))
    }

    /// The scrub, fed what an error's description could carry: none of it
    /// comes out, and the useful rest does.
    @Test func noPositionTokenEmailOrIdComesOut() {
        let secrets = [
            "47.6062", "-122.3321",
            "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI0MiJ9.c2lnbmF0dXJl", "8f14e45fceea167a5a36dedd4bea2543", "rT9xQ2mZ7vLp4Kw8sYb3",
            "maria@metro.example",
            "4317", "trip_8812", "1d2c",
        ]
        let offline = """
            Error Domain=NSURLErrorDomain Code=-1009 "The Internet connection appears to be offline." \
            UserInfo={NSErrorFailingURLStringKey=https://tracker.example.org/api/v1/vehicles/4317/trips/trip_8812?run=1d2c, \
            NSLocalizedDescription=offline}
            """
        let carrying = """
            at 47.6062,-122.3321 for maria@metro.example with Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI0MiJ9.c2lnbmF0dXJl, \
            refresh 8f14e45fceea167a5a36dedd4bea2543 key rT9xQ2mZ7vLp4Kw8sYb3
            """
        let errors = [
            error(.words("could not sign in to "), .shown("tracker.example.org"), .words(": "), .shown(offline)),
            error(.words("could not read: "), .shown(carrying)),
            error(.words("could not drop trip "), .hidden, .words(" of vehicle "), .hidden, .words(": "), .shown("status(409, message: \"\")")),
        ]
        let text = DiagnosticsReport.text(made: made, timeZone: india, device: device, host: "tracker.example.org", errors: errors)

        for secret in secrets {
            #expect(!text.contains(secret), "\(secret)")
        }
        #expect(text.contains("https://tracker.example.org,"), "a URL keeps its host only")
        #expect(text.contains("Code=-1009 \"The Internet connection appears to be offline.\""))
        #expect(text.contains("NSErrorFailingURLStringKey="))
        #expect(text.contains("at <number>,<number> for <email> with Bearer <token>, refresh <token> key <token>"))
        #expect(text.contains("could not drop trip <private> of vehicle <private>: status(409"))
    }

    /// An address or a version has more than one dot, and is not a position.
    @Test func addressesAndVersionsStay() {
        #expect(DiagnosticsReport.scrub("192.168.1.100 iOS 26.4.1 waited 0.25 s") == "192.168.1.100 iOS 26.4.1 waited 0.25 s")
        #expect(DiagnosticsReport.scrub("at 47.606 and 7.1234") == "at <number> and <number>")
        #expect(DiagnosticsReport.scrub("NSErrorFailingURLStringKey") == "NSErrorFailingURLStringKey", "a long word with no digit stays")
    }

    /// A short token with no Bearer before it is still a token, and so is
    /// whatever follows Bearer, however short.
    @Test func shortTokensAreScrubbed() {
        #expect(DiagnosticsReport.scrub("id_token=eyJhbGci.eyJzdWIi.c2ln was refused") == "id_token=<token> was refused")
        #expect(DiagnosticsReport.scrub("Authorization: bearer k3y-9Zq, then 401") == "Authorization: Bearer <token>, then 401")
    }

    // MARK: Reading the app's own log

    /// The app's error lines once the one carrying `probe` can be read: the
    /// log takes a moment to hand back a line just written.
    func readBack(_ probe: String) async throws -> [LoggedError] {
        for _ in 0..<20 {
            let read = try #require(DiagnosticsFile.recentErrors())
            if read.contains(where: { $0.pieces.contains(.shown(probe)) }) { return read }
            try await Task.sleep(for: .milliseconds(100))
        }
        return try #require(DiagnosticsFile.recentErrors())
    }

    /// The app reads its own error lines back from the system's log, with
    /// what it logged privately left out, however the simulator redacts.
    @Test func theAppReadsItsOwnErrorLinesBack() async throws {
        let probe = UUID().uuidString
        let log = Logger(subsystem: DiagnosticsReport.subsystem, category: "diagnostics-test")
        log.error("probe \(probe, privacy: .public) for vehicle \("4317") trip \(42)")
        log.notice("not an error \(probe, privacy: .public)")

        let read = try await readBack(probe)
        let mine = read.filter { $0.category == "diagnostics-test" && $0.pieces.contains(.shown(probe)) }
        let line = try #require(mine.last)
        #expect(line.pieces == [.words("probe "), .shown(probe), .words(" for vehicle "), .hidden, .words(" trip "), .hidden])
        #expect(mine.count == 1, "a notice is not an error")
        #expect(abs(line.date.timeIntervalSinceNow) < 60)
    }

    /// The newest lines, in the order they were logged, even in one instant.
    @Test func theAppsOwnLinesComeOldestFirst() async throws {
        let probe = UUID().uuidString
        let log = Logger(subsystem: DiagnosticsReport.subsystem, category: "diagnostics-order")
        for step in ["first", "second", "third"] {
            log.error("\(step, privacy: .public) of \(probe, privacy: .public)")
        }
        let read = try await readBack(probe)
        let steps = read.filter { $0.pieces.contains(.shown(probe)) }.compactMap { line -> String? in
            if case .shown(let step) = line.pieces.first { step } else { nil }
        }
        #expect(steps == ["first", "second", "third"])
        #expect(read.last?.pieces.contains(.shown(probe)) == true, "the newest line is last")
        #expect(read.count <= DiagnosticsReport.errorLimit)
    }

    /// With more lines than the file takes, the newest are the ones kept.
    @Test func theNewestFiftyOfTheAppsOwnLinesAreKept() async throws {
        let probe = UUID().uuidString
        let log = Logger(subsystem: DiagnosticsReport.subsystem, category: "diagnostics-many")
        for step in 1...(DiagnosticsReport.errorLimit + 5) {
            log.error("\(String(step), privacy: .public) of \(probe, privacy: .public)")
        }
        let read = try await readBack(probe)
        #expect(read.count == DiagnosticsReport.errorLimit)
        let steps = read.filter { $0.pieces.contains(.shown(probe)) }.compactMap { line -> String? in
            if case .shown(let step) = line.pieces.first { step } else { nil }
        }
        #expect(steps.last == String(DiagnosticsReport.errorLimit + 5))
        #expect(!steps.contains("1"), "the oldest went")
    }

    /// The file the share sheet gets: its name, and the text.
    @Test func theShareSheetGetsOneSmallTextFile() async throws {
        let folder = URL.temporaryDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let url = try await DiagnosticsFile(host: "tracker.example.org").export(to: folder, contentType: .utf8PlainText)
        let text = try String(contentsOf: url, encoding: .utf8)
        #expect(url.lastPathComponent == "Vehicle Tracker diagnostics.txt")
        #expect(text.hasPrefix("Vehicle Tracker diagnostics\nMade: "))
        #expect(text.contains("\nServer: tracker.example.org\n"))
        #expect(text.contains("\nApp: \(AccountMenu.versionNumber(Bundle.main.infoDictionary))\n"))
        #expect(text.utf8.count < 20_000)
        // Precision says nothing until location is allowed.
        let allowed = text.contains("\nLocation: allowed\n")
        #expect(text.contains("\nPrecise location: n/a\n") != allowed)
    }
}
