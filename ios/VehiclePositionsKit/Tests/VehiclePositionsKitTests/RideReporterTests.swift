import Foundation
import Testing
@testable import VehiclePositionsKit

@Suite(.serialized) struct RideReporterTests {
    struct Env {
        let transport = FakeRideTransport()
        let location = FakeLocationSource()
        let credentials = InMemoryCredentialStore()
        let clock = ManualRideClock()
        let reporter: RideReporter
        init(config: RideReporterConfiguration? = nil) {
            let cfg = config ?? RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "org.test", appVersion: "1")
            reporter = RideReporter(configuration: cfg, transport: transport, locationSource: location, credentialStore: credentials, clock: clock)
        }
        static let registerJSON = #"{"rider_id":"r1","token":"tok","report_interval_seconds":5,"max_batch_size":3}"#
        static let startJSON = #"{"ride_id":"ride1","state":"pending","report_interval_seconds":5,"max_batch_size":3,"destination":{"stop_id":"S","latitude":47.6090,"longitude":-122.33}}"#
        static func positionsJSON(state: String = "verified", published: Bool = false, ended: Bool = false, reason: String = "") -> String {
            #"{"state":"\#(state)","published":\#(published),"corroboration":"unavailable","accepted":3,"ignored":0,"off_route_streak":0,"ended":\#(ended),"end_reason":"\#(reason)"}"#
        }
        static let endJSON = #"{"status":"ride ended","summary":{"points":3,"matched":3,"corroborated":0,"duration_seconds":30}}"#
        func scriptHappyPath() {
            transport.script("POST register", FakeRideTransport.ok(201, json: Self.registerJSON))
            transport.script("POST rides", FakeRideTransport.ok(201, json: Self.startJSON))
            transport.script("POST positions", FakeRideTransport.ok(json: Self.positionsJSON()))
            transport.script("POST end", FakeRideTransport.ok(json: Self.endJSON))
        }
        /// Emits `n` fixes moving north from 47.6000 in 10 m steps, timestamps from the manual clock.
        func emitFixes(_ n: Int, startLat: Double = 47.6000) {
            for i in 0..<n {
                location.emitFix(lat: startLat + Double(i) * 0.00009, lon: -122.33, at: clock.now.addingTimeInterval(Double(i)))
            }
        }
        /// Polls (real time) until the reporter has buffered at least `count`
        /// fixes: the sync point tests need before they move the manual clock,
        /// since emitting a sample says nothing about it having been consumed.
        func waitForBuffered(_ count: Int, timeout: Duration = .seconds(5)) async -> Bool {
            let reporter = self.reporter
            return await poll(timeout: timeout) { await reporter.pendingFixCount >= count }
        }
    }

    @Test func registersStartsUploadsAndEnds() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        #expect(await events.wait { $0.contains(.registered(riderID: "r1")) && $0.contains(.started(rideID: "ride1")) })
        #expect(await env.reporter.currentState == .pending)
        #expect(try env.credentials.load()?.token == "tok")
        #expect(env.location.handles.count == 1)

        env.emitFixes(2)
        #expect(await env.waitForBuffered(2))
        #expect(await env.clock.waitForSleepers(atLeast: 1))
        env.clock.advance(by: .seconds(5))
        #expect(await events.wait { $0.contains { if case .progress(let p) = $0 { return p.state == .verified }; return false } })
        #expect(await env.reporter.currentState == .verified)
        let upload = try #require(env.transport.requests(matching: "POST positions").first)
        let body = try #require(JSONSerialization.jsonObject(with: upload.body!) as? [String: Any])
        #expect((body["positions"] as? [[String: Any]])?.count == 2)

        await env.reporter.end(reason: .userRequested)
        #expect(await events.waitForEnd() == .userRequested)
        let endReq = try #require(env.transport.requests(matching: "POST end").first)
        #expect(String(data: endReq.body!, encoding: .utf8)!.contains("user_requested"))
        #expect(env.location.lastHandleInvalidated)
        #expect(await env.reporter.isActive == false)
        #expect(await env.reporter.currentState == nil)
    }

    @Test func batchSizeTriggersImmediateUpload() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3) // max_batch_size = 3
        #expect(await events.wait { $0.contains { if case .progress = $0 { return true }; return false } })
        #expect(env.transport.requests(matching: "POST positions").count == 1)
        await env.reporter.end()
    }

    @Test func reusesStoredCredentialsAndReRegistersOn401() async throws {
        let env = Env()
        try env.credentials.save(RiderCredentials(installationID: "inst", riderID: "old", token: "expired"))
        env.transport.script("POST rides", FakeRideTransport.error(401, message: "invalid token"), FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST register", FakeRideTransport.ok(200, json: Env.registerJSON))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        #expect(await events.wait { $0.contains(.started(rideID: "ride1")) })
        #expect(env.transport.requests(matching: "POST register").count == 1)
        #expect(env.transport.requests(matching: "POST rides").count == 2)
        #expect(try env.credentials.load()?.token == "tok")
        #expect(try env.credentials.load()?.installationID == "inst", "installation id is stable across re-registration")
        await env.reporter.end()
    }

    @Test func serverEndsRideViaPositionsResponse() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.ok(json: Env.positionsJSON(state: "rejected", ended: true, reason: "off_route")))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3)
        #expect(await events.waitForEnd() == .offRoute)
        #expect(env.transport.requests(matching: "POST end").isEmpty, "server-initiated end sends no end request")
        #expect(await env.reporter.isActive == false)
    }

    @Test func conflictMeansServerRestart() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.error(409, message: "ride ended"))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3)
        #expect(await events.waitForEnd() == .serverRestart)
    }

    @Test func retriesWithBackoffThenGivesUp() async throws {
        var cfg = RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "a", appVersion: "1")
        cfg.uploadFailureTimeout = .seconds(60)
        let env = Env(config: cfg)
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.transportFailure)
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3) // immediate upload → fails → warning(attempt 1)
        #expect(await events.wait { $0.contains(.warning(.uploadRetrying(attempt: 1))) })
        // backoff 1 s, 2 s, 4 s, 8 s, 16 s, 32→30 s: after 60 s of failure the ride ends.
        for _ in 0..<8 {
            // Give the ride a moment to end before winding time on again, so a
            // finished test does not sit through eight sleeper timeouts.
            if await events.waitForEnd(timeout: .milliseconds(50)) != nil { break }
            guard await env.clock.waitForSleepers(atLeast: 1) else { break }
            env.clock.advance(by: .seconds(16))
        }
        #expect(await events.waitForEnd() == .networkFailure)
        #expect(env.transport.requests(matching: "POST positions").count >= 4)
    }

    @Test func arrivalEndsRide() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1", destinationStopID: "S"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emitFix(lat: 47.6000, lon: -122.33, at: env.clock.now)
        env.location.emitFix(lat: 47.6050, lon: -122.33, at: env.clock.now)
        env.location.emitFix(lat: 47.6087, lon: -122.33, at: env.clock.now) // ~33 m from the destination
        #expect(await events.waitForEnd() == .arrived)
        let endReq = try #require(env.transport.requests(matching: "POST end").first)
        #expect(String(data: endReq.body!, encoding: .utf8)!.contains("arrived"))
    }

    @Test func stationaryAndMaxDurationEndRide() async throws {
        var cfg = RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "a", appVersion: "1")
        cfg.stationaryTimeout = .seconds(20)
        let env = Env(config: cfg); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emitFix(lat: 47.6, lon: -122.33, at: env.clock.now, stationary: true)
        // Advancing before the reporter has consumed that fix would start the
        // stationary run 25 s late and the ride would never end.
        #expect(await env.waitForBuffered(1))
        env.clock.advance(by: .seconds(25))
        env.location.emitFix(lat: 47.6, lon: -122.33, at: env.clock.now, stationary: true)
        #expect(await events.waitForEnd() == .stationary)

        var cfg2 = cfg
        cfg2.maxRideDuration = .seconds(12)
        let env2 = Env(config: cfg2); env2.scriptHappyPath()
        let stream2 = try await env2.reporter.start(TripDescriptor(tripID: "T1"))
        let events2 = EventCollector(stream2)
        _ = await events2.wait { $0.contains(.started(rideID: "ride1")) }
        for _ in 0..<3 {
            _ = await env2.clock.waitForSleepers(atLeast: 1)
            env2.clock.advance(by: .seconds(5))
        }
        #expect(await events2.waitForEnd() == .maxDuration)
    }

    @Test func diagnosticsEndOrWarn() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.location.emit(LocationSample(fix: nil, diagnostic: .accuracyLimited))
        #expect(await events.wait { $0.contains(.warning(.accuracyLimited)) })
        env.location.emit(LocationSample(fix: nil, diagnostic: .authorizationDenied))
        #expect(await events.waitForEnd() == .authorizationDenied)
    }

    @Test func startingAgainSupersedes() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON), FakeRideTransport.ok(201, json: Env.startJSON.replacingOccurrences(of: "ride1", with: "ride2")))
        env.transport.script("POST positions", FakeRideTransport.ok(json: Env.positionsJSON()))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let s1 = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let e1 = EventCollector(s1)
        _ = await e1.wait { $0.contains(.started(rideID: "ride1")) }
        let s2 = try await env.reporter.start(TripDescriptor(tripID: "T2"))
        let e2 = EventCollector(s2)
        #expect(await e1.waitForEnd() == .superseded)
        #expect(await e2.wait { $0.contains(.started(rideID: "ride2")) })
        #expect(env.location.handles.count == 2)
        #expect(env.location.handles[0].invalidated)
        await env.reporter.end()
    }

    @Test func startFailureLeavesNoActiveRide() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.error(404, message: "unknown trip"))
        await #expect(throws: RideError.server(status: 404, message: "unknown trip")) {
            _ = try await env.reporter.start(TripDescriptor(tripID: "NOPE"))
        }
        #expect(await env.reporter.isActive == false)
        #expect(env.location.handles.isEmpty)
    }

    @Test func concurrentStartsSerialise() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON), FakeRideTransport.ok(201, json: Env.startJSON.replacingOccurrences(of: "ride1", with: "ride2")))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        async let first = env.reporter.start(TripDescriptor(tripID: "T1"))
        async let second = env.reporter.start(TripDescriptor(tripID: "T2"))
        let (s1, s2) = try await (first, second)
        let c1 = EventCollector(s1)
        let c2 = EventCollector(s2)
        let started: @Sendable ([RideEvent]) -> Bool = { $0.contains { if case .started = $0 { return true }; return false } }
        #expect(await c1.wait(started))
        #expect(await c2.wait(started))

        // Whichever start won the race, ride1 is the one that was superseded.
        let loser = await c1.events.contains(.started(rideID: "ride1")) ? c1 : c2
        let winner = loser === c1 ? c2 : c1
        #expect(await loser.waitForEnd() == .superseded)
        #expect(await winner.events.contains(.started(rideID: "ride2")))
        #expect(await env.reporter.isActive, "exactly one ride survives")
        #expect(env.transport.requests(matching: "POST rides").count == 2)
        #expect(env.location.handles.count == 2)
        #expect(env.location.handles.filter(\.invalidated).count == 1, "the superseded ride released its handle")

        await env.reporter.end()
        #expect(await winner.waitForEnd() == .userRequested)
        let allHandlesInvalidated = env.location.handles.allSatisfy(\.invalidated)
        #expect(allHandlesInvalidated)
        #expect(await env.reporter.isActive == false)
    }

    @Test func supersededRideUploadsWhatItBuffered() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON), FakeRideTransport.ok(201, json: Env.startJSON.replacingOccurrences(of: "ride1", with: "ride2")))
        env.transport.script("POST positions", FakeRideTransport.ok(json: Env.positionsJSON()))
        let s1 = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let e1 = EventCollector(s1)
        _ = await e1.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(2)
        #expect(await env.waitForBuffered(2))

        _ = try await env.reporter.start(TripDescriptor(tripID: "T2"))
        #expect(await e1.waitForEnd() == .superseded)
        let calls = env.transport.recorded.map { $0.request.method + " " + $0.request.path }
        let flushed = try #require(calls.firstIndex(of: "POST api/v1/rider/rides/ride1/positions"))
        let starts = calls.indices.filter { calls[$0] == "POST api/v1/rider/rides" }
        #expect(starts.count == 2)
        #expect(flushed < starts[1], "the old ride's fixes go up before the new ride starts")
        #expect(env.transport.requests(matching: "POST end").isEmpty, "superseded is not a reason a client may report")
        await env.reporter.end(reason: .appTerminated)
    }

    @Test func unauthorizedUploadClearsTokenAndEndsWithServerRestart() async throws {
        let env = Env()
        try env.credentials.save(RiderCredentials(installationID: "inst", riderID: "r1", token: "tok"))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.error(401, message: "token expired"))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3)
        #expect(await events.waitForEnd() == .serverRestart)
        #expect(try env.credentials.load()?.token == nil, "an expired token is forgotten")
        #expect(try env.credentials.load()?.installationID == "inst", "the device keeps its identity")
        #expect(env.transport.requests(matching: "POST end").isEmpty)
        #expect(await env.reporter.isActive == false)
    }

    @Test func fullBatchDoesNotBypassBackoff() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.transportFailure)
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }
        env.emitFixes(3) // a full batch uploads at once — and fails
        #expect(await events.wait { $0.contains(.warning(.uploadRetrying(attempt: 1))) })
        #expect(env.transport.requests(matching: "POST positions").count == 1)

        env.emitFixes(3, startLat: 47.6200) // the buffer is over the batch size again
        #expect(await env.waitForBuffered(6))
        #expect(env.transport.requests(matching: "POST positions").count == 1, "a running backoff is not bypassed by a full batch")
        await env.reporter.end()
    }

    @Test func endReturnsWithinItsBudgetWhenTheServerHangs() async throws {
        let env = Env(); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }

        // The end call never answers: `end` must still return, on the clock.
        env.transport.hold("POST end", .waitForRelease)
        let ending = Task { await env.reporter.end(reason: .userRequested) }
        #expect(await env.transport.waitForRequest(matching: "POST end"))
        #expect(await env.clock.waitForSleepers(atLeast: 1), "the 10 s budget is sleeping")
        env.clock.advance(by: .seconds(10))

        await ending.value
        #expect(await events.waitForEnd() == .userRequested)
        #expect(await env.reporter.isActive == false)
        env.transport.release("POST end") // let the detached teardown finish
    }

    @Test func finalFlushIsCappedAtTwoBatches() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        // The first upload fails, which parks the ride in backoff so the fixes
        // that follow pile up instead of going out a batch at a time.
        env.transport.script("POST positions", FakeRideTransport.transportFailure, FakeRideTransport.ok(json: Env.positionsJSON()))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }

        env.emitFixes(12) // four batches at max_batch_size 3
        #expect(await events.wait { $0.contains(.warning(.uploadRetrying(attempt: 1))) })
        #expect(await env.waitForBuffered(12))

        await env.reporter.end(reason: .appTerminated)
        #expect(await events.waitForEnd() == .appTerminated)
        // The failed mid-ride upload, plus exactly two from the teardown: the
        // rest is abandoned rather than sent past the budget.
        #expect(env.transport.requests(matching: "POST positions").count == 3)
    }

    @Test func stationaryTimeoutEndsRideWithNoFurtherSample() async throws {
        var cfg = RideReporterConfiguration(serverURL: URL(string: "https://vp.example.org")!, appID: "a", appVersion: "1")
        cfg.stationaryTimeout = .seconds(20)
        let env = Env(config: cfg); env.scriptHappyPath()
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }

        // Core Location pauses updates while the device is still, so this one
        // stationary sample may be the last one ever delivered.
        env.location.emitFix(lat: 47.6, lon: -122.33, at: env.clock.now, stationary: true)
        #expect(await env.waitForBuffered(1))
        #expect(await env.clock.waitForSleepers(atLeast: 1))
        env.clock.advance(by: .seconds(25))
        #expect(await events.waitForEnd() == .stationary)
    }

    @Test func rejectedBatchIsDroppedNotRetried() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.error(422, message: "batch too large"))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }

        env.emitFixes(3)
        #expect(await events.wait { $0.contains(.warning(.batchRejected(status: 422))) })
        #expect(await env.reporter.pendingFixCount == 0, "a rejected batch is dropped, not restored")
        #expect(await events.events.contains(.warning(.uploadRetrying(attempt: 1))) == false, "and it costs no retry budget")
        #expect(await env.reporter.isActive, "the ride carries on")
        await env.reporter.end()
    }

    @Test func undecodableResponseDropsTheBatch() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.script("POST positions", FakeRideTransport.ok(json: "not json at all"))
        env.transport.script("POST end", FakeRideTransport.ok(json: Env.endJSON))
        let stream = try await env.reporter.start(TripDescriptor(tripID: "T1"))
        let events = EventCollector(stream)
        _ = await events.wait { $0.contains(.started(rideID: "ride1")) }

        env.emitFixes(3)
        #expect(await events.wait { $0.contains(.warning(.batchRejected(status: 0))) })
        #expect(await env.reporter.pendingFixCount == 0)
        #expect(await env.reporter.isActive)
        await env.reporter.end()
    }

    @Test func tripStatusRegistersWhenNeeded() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("GET status", FakeRideTransport.ok(json: #"{"trip_id":"T1","start_date":"20260902","trusted":false,"rider_reported":true,"riders":1}"#))
        let s = try await env.reporter.tripStatus(tripID: "T1", startDate: nil)
        #expect(s.riderReported)
        #expect(env.transport.requests(matching: "GET status").first?.bearerToken == "tok")
    }

    @Test func cancellingStartInstallsNothing() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        env.transport.hold("POST rides") // the ride request stays in flight

        let start = Task { try await env.reporter.start(TripDescriptor(tripID: "T1")) }
        #expect(await env.transport.waitForRequest(matching: "POST rides"))
        start.cancel()

        await #expect(throws: CancellationError.self) { _ = try await start.value }
        #expect(await env.reporter.isActive == false)
        #expect(env.location.handles.isEmpty, "a cancelled start holds no background activity")
    }

    @Test func cancellingStartAfterServerAcceptsInstallsNothing() async throws {
        let env = Env()
        env.transport.script("POST register", FakeRideTransport.ok(201, json: Env.registerJSON))
        env.transport.script("POST rides", FakeRideTransport.ok(201, json: Env.startJSON))
        // This one answers even though the caller is gone, so the reporter has
        // a started ride in hand at the moment it notices the cancellation.
        env.transport.hold("POST rides", .waitForRelease)

        let start = Task { try await env.reporter.start(TripDescriptor(tripID: "T1")) }
        #expect(await env.transport.waitForRequest(matching: "POST rides"))
        start.cancel()
        env.transport.release("POST rides")

        await #expect(throws: CancellationError.self) { _ = try await start.value }
        #expect(await env.reporter.isActive == false)
        #expect(env.location.handles.isEmpty, "a cancelled start holds no background activity")
        #expect(env.transport.requests(matching: "POST positions").isEmpty, "nothing was ever reported")
    }

    @Test func cancelledStartDoesNotSupersedeTheRunningRide() async throws {
        let env = Env(); env.scriptHappyPath()
        // The first start stays in flight, so the second queues behind it and
        // is cancelled before it can run a single line of its own.
        env.transport.hold("POST rides", .waitForRelease)
        let first = Task { try await env.reporter.start(TripDescriptor(tripID: "T1")) }
        #expect(await env.transport.waitForRequest(matching: "POST rides"))

        let second = Task { try await env.reporter.start(TripDescriptor(tripID: "T2")) }
        second.cancel()
        env.transport.release("POST rides")

        let events = EventCollector(try await first.value)
        #expect(await events.wait { $0.contains(.started(rideID: "ride1")) })
        await #expect(throws: CancellationError.self) { _ = try await second.value }
        #expect(await env.reporter.isActive, "the running ride survives a cancelled start")
        #expect(env.transport.requests(matching: "POST rides").count == 1)
        #expect(env.location.handles.count == 1)
        await env.reporter.end()
        #expect(await events.waitForEnd() == .userRequested, "and it was never superseded")
    }
}
