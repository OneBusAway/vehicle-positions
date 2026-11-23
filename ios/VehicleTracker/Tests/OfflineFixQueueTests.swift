import Foundation
import Testing
@testable import VehicleTracker

@Suite struct OfflineFixQueueTests {
    /// The real queue lives in "Application Support", which has a space in
    /// its name, so the tests' directory does too.
    let directory = URL.temporaryDirectory.appending(path: "Application Support \(UUID().uuidString)")
    let ana = OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ana@example.org")!

    func report(at timestamp: Int64, vehicle: String = "bus-1") -> LocationReport {
        LocationReport(vehicleID: vehicle, tripID: "T1", latitude: 47.6, longitude: -122.33,
                       bearing: 90, speed: 8, accuracy: 5, timestamp: timestamp)
    }

    func timestamps(_ queue: OfflineFixQueue, for owner: OfflineFixQueue.Owner) async throws -> [Int64] {
        try await queue.oldest(.max, for: owner).map(\.timestamp)
    }

    /// The lines in the owner's file, as a relaunch would find them.
    func linesOnDisk(for owner: OfflineFixQueue.Owner) throws -> Int {
        let file = directory.appending(path: "offline-fixes/\(owner.key).jsonl")
        return try String(contentsOf: file, encoding: .utf8).split(separator: "\n").count
    }

    /// What a relaunch finds: a queue made afresh over the same directory
    /// reads back every report, in the order they were kept, with every
    /// field, the occupancy and the absent ones too.
    @Test func keptReportsSurviveARelaunch() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        var full = report(at: 1001)
        full.routeID = "R1"
        full.startDate = "20260902"
        full.occupancyStatus = .full
        let bare = LocationReport(vehicleID: "bus-1", tripID: "T1", latitude: 47.61, longitude: -122.34,
                                  bearing: nil, speed: nil, accuracy: nil, timestamp: 1002)
        let queue = OfflineFixQueue(directory: directory)
        try await queue.add(full, for: ana)
        try await queue.add(bare, for: ana)
        try await queue.add(report(at: 1003), for: ana)

        let relaunched = OfflineFixQueue(directory: directory)
        #expect(try await relaunched.oldest(10, for: ana) == [full, bare, report(at: 1003)])
        #expect(try await relaunched.oldest(2, for: ana).map(\.timestamp) == [1001, 1002])
    }

    @Test func overCapacityTheOldestGoFirst() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let queue = OfflineFixQueue(directory: directory, capacity: 3)
        for t in Int64(1001)...1005 {
            try await queue.add(report(at: t), for: ana)
        }
        #expect(try await timestamps(queue, for: ana) == [1003, 1004, 1005])
        #expect(try await timestamps(OfflineFixQueue(directory: directory, capacity: 3), for: ana) == [1003, 1004, 1005],
                "and after a relaunch")
    }

    /// A phone out of coverage for longer than the queue holds drops one
    /// report for each it adds. The file may run a tenth past the cap before
    /// it is written anew, rather than once per report.
    @Test func aFullQueueIsWrittenAnewOnlyOnceItRunsATenthOver() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let queue = OfflineFixQueue(directory: directory, capacity: 10)
        for t in Int64(1)...11 {
            try await queue.add(report(at: t), for: ana)
        }
        #expect(try linesOnDisk(for: ana) == 11, "appended, not written anew")
        #expect(try await timestamps(queue, for: ana) == Array(2...11))

        try await queue.add(report(at: 12), for: ana)
        #expect(try linesOnDisk(for: ana) == 10, "written anew with only what it holds")
        #expect(try await timestamps(queue, for: ana) == Array(3...12))

        try await queue.add(report(at: 13), for: ana)
        #expect(try await timestamps(OfflineFixQueue(directory: directory, capacity: 10), for: ana) == Array(4...13),
                "a relaunch reads the newest ten of the eleven lines")
    }

    /// A phone shared between drivers, or signed in to another server: each
    /// one's reports are kept apart, so none can go as another's.
    @Test func eachDriverAndServerHasTheirOwnReports() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let ben = try #require(OfflineFixQueue.Owner(server: "https://transit.example.org", email: "ben@example.org"))
        let anaElsewhere = try #require(OfflineFixQueue.Owner(server: "https://other.example.org", email: "ana@example.org"))
        let queue = OfflineFixQueue(directory: directory)
        try await queue.add(report(at: 1001), for: ana)
        try await queue.add(report(at: 1002), for: ben)
        try await queue.add(report(at: 1003), for: anaElsewhere)

        #expect(try await timestamps(queue, for: ana) == [1001])
        #expect(try await timestamps(queue, for: ben) == [1002])
        #expect(try await timestamps(queue, for: anaElsewhere) == [1003])
        #expect(try await timestamps(OfflineFixQueue(directory: directory), for: ben) == [1002], "and after a relaunch")
    }

    /// The server is told apart as `isSameServer` tells it, and the email in
    /// any case: retyping either must not cost a driver their reports.
    @Test func anOwnerIsTheSameHoweverTheyTypedIt() throws {
        let typed = [
            ("https://transit.example.org/", "ana@example.org"),
            ("HTTPS://Transit.Example.org", "Ana@Example.org"),
            ("https://transit.example.org:443", " ana@example.org\n"),
        ]
        for (server, email) in typed {
            #expect(OfflineFixQueue.Owner(server: server, email: email) == ana, "\(server) \(email)")
        }
    }

    /// Two servers on one host are two servers: a trip id on one means
    /// nothing on the other.
    @Test func anotherPortOrPathIsAnotherServer() {
        #expect(OfflineFixQueue.Owner(server: "https://transit.example.org:8443", email: "ana@example.org") != ana)
        #expect(OfflineFixQueue.Owner(server: "https://transit.example.org/staging", email: "ana@example.org") != ana)
        #expect(OfflineFixQueue.Owner(server: "http://transit.example.org", email: "ana@example.org") != ana)
    }

    @Test func thereIsNoOwnerWithoutAServerAndAnEmail() {
        #expect(OfflineFixQueue.Owner(server: "", email: "ana@example.org") == nil)
        #expect(OfflineFixQueue.Owner(server: "not a server", email: "ana@example.org") == nil)
        #expect(OfflineFixQueue.Owner(server: "https://transit.example.org", email: "") == nil)
        #expect(OfflineFixQueue.Owner(server: "https://transit.example.org", email: "  ") == nil)
    }

    /// A phone handed on keeps no trace of who drove before in its file
    /// names.
    @Test func aFileNamesNeitherTheDriverNorTheServer() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        try await OfflineFixQueue(directory: directory).add(report(at: 1001), for: ana)
        let names = try FileManager.default.contentsOfDirectory(atPath: directory.appending(path: "offline-fixes").path(percentEncoded: false))
        try #require(names.count == 1)
        #expect(!names[0].contains("ana"))
        #expect(!names[0].contains("transit"))
    }

    /// A crash mid-append leaves a line cut short. It is skipped rather than
    /// allowed to cost the rest, and the file is put right, so the next
    /// report added does not join it and go too.
    @Test func aLineCutShortByACrashIsSkipped() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        try await OfflineFixQueue(directory: directory).add(report(at: 1001), for: ana)
        let file = directory.appending(path: "offline-fixes/\(ana.key).jsonl")
        let handle = try FileHandle(forWritingTo: file)
        try handle.seekToEnd()
        try handle.write(contentsOf: Data(#"{"vehicle_id":"bus-1","trip_id":"T1","lati"#.utf8))
        try handle.close()

        let relaunched = OfflineFixQueue(directory: directory)
        #expect(try await timestamps(relaunched, for: ana) == [1001])
        try await relaunched.add(report(at: 1002), for: ana)
        #expect(try await timestamps(OfflineFixQueue(directory: directory), for: ana) == [1001, 1002])
    }

    /// A file that holds no report at all, such as one an incompatible build
    /// wrote, reads as empty and takes the next report.
    @Test func aFileOfNoReportsReadsAsEmpty() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let folder = directory.appending(path: "offline-fixes")
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        try Data("not json\n\u{0}\u{1}\n{\"vehicle_id\":7}\n".utf8).write(to: folder.appending(path: "\(ana.key).jsonl"))

        let queue = OfflineFixQueue(directory: directory)
        #expect(try await timestamps(queue, for: ana) == [])
        try await queue.add(report(at: 1001), for: ana)
        #expect(try await timestamps(OfflineFixQueue(directory: directory), for: ana) == [1001])
    }

    /// A write that fails, as on a full disk, leaves the queue as the file
    /// is, so what is read next is what a relaunch would find.
    @Test func aReportThatCannotBeWrittenIsNotHeldEither() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let queue = OfflineFixQueue(directory: directory)
        try await queue.add(report(at: 1001), for: ana)
        let file = directory.appending(path: "offline-fixes/\(ana.key).jsonl").path(percentEncoded: false)
        try FileManager.default.setAttributes([.posixPermissions: 0o444], ofItemAtPath: file)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o644], ofItemAtPath: file) }

        await #expect(throws: (any Error).self) { try await queue.add(report(at: 1002), for: ana) }
        #expect(try await timestamps(queue, for: ana) == [1001])
    }

    /// A full queue makes room for a new report by dropping its oldest, which
    /// may be in the batch on its way. Removing the batch by its size would
    /// then take the new report too, though it was never sent.
    @Test func removingABatchTakesOnlyWhatIsStillAtTheFront() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let queue = OfflineFixQueue(directory: directory, capacity: 3)
        for t in Int64(1001)...1003 {
            try await queue.add(report(at: t), for: ana)
        }
        let batch = try await queue.oldest(3, for: ana)
        try await queue.add(report(at: 1004), for: ana)
        #expect(try await timestamps(queue, for: ana) == [1002, 1003, 1004], "the add dropped 1001")

        try await queue.remove(batch, for: ana)
        #expect(try await timestamps(queue, for: ana) == [1004], "only the unsent report is left")
        #expect(try await timestamps(OfflineFixQueue(directory: directory, capacity: 3), for: ana) == [1004],
                "and after a relaunch")
    }

    @Test func theLastRemovalTakesTheFileAway() async throws {
        defer { try? FileManager.default.removeItem(at: directory) }
        let queue = OfflineFixQueue(directory: directory)
        try await queue.add(report(at: 1001), for: ana)
        try await queue.remove(queue.oldest(10, for: ana), for: ana)
        let file = directory.appending(path: "offline-fixes/\(ana.key).jsonl").path(percentEncoded: false)
        #expect(!FileManager.default.fileExists(atPath: file))
        #expect(try await timestamps(queue, for: ana) == [])
    }
}
