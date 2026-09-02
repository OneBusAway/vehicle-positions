import Foundation

/// Runs one ride at a time (spec §4.12): registers the device, starts the ride,
/// batches fixes to the server, watches for the conditions that end a ride, and
/// tells the host app about all of it through a single stream of ``RideEvent``.
///
/// Everything that can vary — the network, location, the keychain, time — is
/// injected, so the whole lifecycle is testable without a server or a device.
public actor RideReporter {
    private let configuration: RideReporterConfiguration
    private let locationSource: any LocationSource
    private let credentialStore: any CredentialStore
    private let clock: any RideClock
    private let client: RiderClient

    /// Everything that exists only while a ride is running. Held as a value so
    /// every mutation is written back deliberately, never across an `await`.
    private struct ActiveRide {
        let rideID: String
        let token: String
        let startedAt: Date
        let reportInterval: Duration
        let maxBatchSize: Int
        let continuation: AsyncStream<RideEvent>.Continuation
        let handle: any BackgroundActivityHandle
        var evaluator: EndConditionEvaluator
        var buffer: SampleBuffer
        var state: RideState
        /// Consecutive failed uploads; zero while uploads are succeeding.
        var failureAttempts = 0
        /// When the current run of failures began.
        var failingSince: Date?
        var warnedAccuracy = false
        var warnedInUse = false
        /// The batch an upload is carrying right now. An `end` that cancels
        /// that upload sends it again rather than losing it.
        var inFlight: [LocationFix] = []
        /// Set when the server last answered 429. The batch-full flush stands
        /// down until a periodic flush succeeds, so a backlog drains at the
        /// pace the server set instead of tripping its limit on every sample.
        var throttled = false
        /// Set for the duration of teardown so re-entrant `end` calls return.
        var ending = false
    }

    private var active: ActiveRide?
    /// True while ``flush()`` is waiting on the server. The upload loop and a
    /// full buffer can both ask for a flush, and two in flight would upload two
    /// batches the server then has to order for itself.
    private var isUploading = false
    private var locationTask: Task<Void, Never>?
    private var uploadTask: Task<Void, Never>?
    /// The most recent `start`, so the next one can queue behind it.
    private var startTask: Task<AsyncStream<RideEvent>, any Error>?

    public init(
        configuration: RideReporterConfiguration,
        transport: any RideTransport,
        locationSource: any LocationSource,
        credentialStore: any CredentialStore,
        clock: any RideClock = ContinuousRideClock()
    ) {
        self.configuration = configuration
        self.locationSource = locationSource
        self.credentialStore = credentialStore
        self.clock = clock
        client = RiderClient(serverURL: configuration.serverURL, transport: transport)
    }

    /// A reporter released mid-ride cannot end the ride properly, but it must
    /// not leave its loops running or its consumer waiting on a stream that
    /// will never produce another event.
    deinit {
        startTask?.cancel()
        locationTask?.cancel()
        uploadTask?.cancel()
        active?.continuation.finish()
        active?.handle.invalidate()
    }

    /// The server's latest verdict on the running ride, or `nil` when none is.
    public var currentState: RideState? { active?.state }

    public var isActive: Bool { active != nil }

    /// Fixes buffered for the next upload. Internal, and only tests read it:
    /// it is how they synchronise on the location loop having consumed a sample.
    var pendingFixCount: Int { active?.buffer.count ?? 0 }

    // MARK: - Lifecycle

    /// Starts reporting `trip`, superseding any ride already running.
    ///
    /// The returned stream carries every event from registration onwards: it is
    /// created before the first network call, and `AsyncStream` buffers, so the
    /// caller sees `.registered` and `.started` in order however late it begins
    /// iterating. On failure nothing is left running and the error is rethrown.
    public func start(_ trip: TripDescriptor) async throws -> AsyncStream<RideEvent> {
        // Starts are serialised. Two racing callers would each find no active
        // ride and the second would overwrite the first, leaving its loops
        // running, its stream unfinished and its background handle held. Queued
        // behind the one in flight, this start supersedes a whole ride instead.
        let predecessor = startTask
        let task = Task { [weak self] () -> AsyncStream<RideEvent> in
            _ = try? await predecessor?.value
            guard let self else { throw CancellationError() }
            return try await self.performStart(trip)
        }
        startTask = task
        // Only the newest start owns the slot; an older one must not clear it.
        defer { if startTask == task { startTask = nil } }
        // The work is unstructured, so the caller's cancellation — a SwiftUI
        // `.task` going away, say — has to be handed on explicitly. Without
        // this a cancelled `.task` would leave a ride running and a background
        // handle held with nobody left to end them.
        return try await withTaskCancellationHandler {
            try await task.value
        } onCancel: {
            task.cancel()
        }
    }

    private func performStart(_ trip: TripDescriptor) async throws -> AsyncStream<RideEvent> {
        // A start that was cancelled before it ran must change nothing: ending
        // the ride already running would be the one thing worse than doing
        // nothing at all.
        try Task.checkCancellation()

        // Superseding now suspends — it flushes what the old ride buffered — so
        // this must stay behind the serialisation above to remain re-entrant.
        if active != nil { await end(reason: .superseded) }

        let (stream, continuation) = AsyncStream<RideEvent>.makeStream()
        do {
            let (token, response) = try await withAuthorizedToken(into: continuation) { token in
                try await client.startRide(token: token, trip: trip)
            }

            // The caller may have gone away while the server was answering. A
            // start nobody is waiting on installs nothing: no ride, no location
            // subscription, no background handle.
            try Task.checkCancellation()

            // Subscribe before the first sample can arrive, so nothing emitted
            // between here and the first loop iteration is lost.
            let updates = locationSource.updates()
            let rideID = response.rideID
            active = ActiveRide(
                rideID: rideID,
                token: token,
                startedAt: clock.now,
                reportInterval: .seconds(max(1, response.reportIntervalSeconds)),
                maxBatchSize: max(1, response.maxBatchSize),
                continuation: continuation,
                handle: locationSource.beginBackgroundActivity(),
                evaluator: EndConditionEvaluator(
                    configuration: configuration,
                    destination: response.destination?.coordinate
                ),
                buffer: SampleBuffer(retention: configuration.sampleRetention),
                state: response.state
            )
            locationTask = Task { [weak self] in await self?.runLocationLoop(rideID: rideID, updates: updates) }
            uploadTask = Task { [weak self] in await self?.runUploadLoop(rideID: rideID) }
            continuation.yield(.started(rideID: rideID))
            return stream
        } catch {
            // Nothing after the ride is installed throws today, but a failure
            // that ever did would leave it reporting into a stream nobody
            // holds. It has no owner left: end it — and not as the rider's
            // doing, since the rider asked for the opposite.
            if active != nil { await end(reason: .networkFailure) }
            // Nobody will ever read this stream; leave no consumer hanging.
            continuation.finish()
            throw error
        }
    }

    /// Ends the running ride. Idempotent, and safe to call from either loop.
    public func end(reason: RideEndReason = .userRequested) async {
        guard active?.ending == false,
              let rideID = active?.rideID, let token = active?.token,
              let maxBatchSize = active?.maxBatchSize,
              // The stream and the background handle are this ride's, and the
              // teardown below suspends: a `start` may install another ride
              // meanwhile, and these two must still be the old one's.
              let continuation = active?.continuation, let handle = active?.handle
        else { return }
        active?.ending = true

        locationTask?.cancel()
        uploadTask?.cancel()
        locationTask = nil
        uploadTask = nil

        // A ride the server ended itself has nothing left to learn from us;
        // every other reason — superseded and abandoned rides included — sends
        // what is still buffered before it goes.
        var batches: [[PositionUpload]] = []
        if reason.disposition != .silent {
            // The upload cancelled just above may have been carrying a batch
            // the server never saw. It goes first; if the server did see it,
            // it ignores points it already has, so a duplicate costs nothing.
            if let inFlight = active?.inFlight, !inFlight.isEmpty {
                active?.buffer.restore(inFlight)
                active?.inFlight = []
            }
            // Drained in place, through `active`: see `consume`.
            while (active?.buffer.count ?? 0) > 0, batches.count < Self.endFlushBatchLimit {
                batches.append((active?.buffer.take(max: maxBatchSize) ?? []).map(Self.upload))
            }
        }
        // Reasons only the server may reach are already known to it: reporting
        // one back would be rejected, and the ride is over there either way.
        let reportsEnd = reason.isClientReportable
        var summary: RideSummary?
        if !batches.isEmpty || reportsEnd {
            let pending = batches
            let client = self.client
            let clock = self.clock
            // Unstructured so this teardown outlives the cancellation of the
            // loop it may have been called from; every call is best-effort.
            let teardown = Task { () -> RideSummary? in
                for batch in pending {
                    do {
                        _ = try await client.uploadPositions(token: token, rideID: rideID, positions: batch)
                    } catch RideError.server(let status, _) where status == 429 {
                        // Paced, not refused: the periodic flush and this final
                        // one can exhaust the server's burst. One wait, one
                        // more try; a second refusal means the budget is
                        // better spent on the end-ride call.
                        try? await clock.sleep(for: Self.endThrottleRetryDelay)
                        guard (try? await client.uploadPositions(token: token, rideID: rideID, positions: batch)) != nil else { break }
                    } catch {
                        break // A network that just failed will not do better.
                    }
                }
                guard reportsEnd else { return nil }
                return try? await client.endRide(token: token, rideID: rideID, reason: reason).summary
            }
            summary = await withinEndBudget(teardown)
        }

        continuation.yield(.ended(reason, summary: summary))
        continuation.finish()
        handle.invalidate()
        // A `start` that ran while this teardown awaited the server owns the
        // slot now; clearing it would orphan the ride it just installed.
        if active?.rideID == rideID { active = nil }
    }

    /// The most batches a final flush may send. A device that has been offline
    /// can hold ten minutes of fixes; uploading all of them would blow the
    /// budget below, and the ride is over either way.
    private static let endFlushBatchLimit = 2
    /// The whole of `end` — final flush and end-ride call — gets this long.
    private static let endBudget: Duration = .seconds(10)
    /// How long a final batch the server paced waits before its one retry:
    /// the server refills one report every two seconds.
    private static let endThrottleRetryDelay: Duration = .seconds(2)

    /// Waits for `teardown`, or for the budget to run out, whichever comes
    /// first. A teardown that loses the race is not cancelled: it keeps trying
    /// in the background, so the server still hears about the ride if the
    /// network recovers, while `end` returns to its caller now.
    ///
    /// The race runs in a task of its own on purpose. `end` is often called
    /// from the location or upload loop, and it has just cancelled both — so
    /// the task it is running on is cancelled, and anything structured under
    /// it (a task group, a stream iterator) would return at once. That would
    /// hand `end` back an empty summary, release the background handle and
    /// clear the ride while the final flush and the end-ride call were still
    /// in flight. An unstructured task inherits no cancellation and waits the
    /// budget out.
    ///
    /// Not a task group: a group waits for every child before it returns, and
    /// the child awaiting `teardown` cannot be cancelled out of that await, so
    /// a server that never answers would hold `end` for as long as it liked.
    private func withinEndBudget(_ teardown: Task<RideSummary?, Never>) async -> RideSummary? {
        let clock = self.clock
        let race = Task { () -> RideSummary? in
            let (results, continuation) = AsyncStream<RideSummary?>.makeStream()
            let finish = Task {
                let summary = await teardown.value
                continuation.yield(summary)
                continuation.finish()
            }
            let budget = Task {
                try? await clock.sleep(for: Self.endBudget)
                continuation.yield(nil)
                continuation.finish()
            }
            defer {
                // Whoever lost the race has nothing left to report. Cancelling
                // `finish` does not reach `teardown`, which keeps trying.
                finish.cancel()
                budget.cancel()
            }
            var iterator = results.makeAsyncIterator()
            return await iterator.next() ?? nil
        }
        return await race.value
    }

    /// How the server sees `tripID` right now, registering first if this device
    /// has never registered.
    public func tripStatus(tripID: String, startDate: String?) async throws -> TripStatus {
        try await withAuthorizedToken(into: nil) { token in
            try await client.tripStatus(token: token, tripID: tripID, startDate: startDate)
        }.result
    }

    // MARK: - Credentials

    /// Runs `body` with the stored token — and once more with a freshly
    /// registered one if the server no longer honours it. Every call made
    /// outside a ride goes through here, so an expired or revoked token is
    /// recovered from in one place rather than at whichever call site
    /// happened to think of it.
    private func withAuthorizedToken<T: Sendable>(
        into continuation: AsyncStream<RideEvent>.Continuation?,
        _ body: (String) async throws -> T
    ) async throws -> (token: String, result: T) {
        var token = try await authorizedToken(into: continuation)
        do {
            return (token, try await body(token))
        } catch RideError.notAuthorized {
            clearStoredToken()
            token = try await authorizedToken(into: continuation)
            return (token, try await body(token))
        }
    }

    /// The token to call the API with: the one this device has stored, or the
    /// one a fresh registration issues when it has none. `into` receives the
    /// `.registered` event when a registration happens.
    private func authorizedToken(into continuation: AsyncStream<RideEvent>.Continuation?) async throws -> String {
        let credentials = try loadOrCreateCredentials()
        if let stored = credentials.token { return stored }
        return try await register(installationID: credentials.installationID, into: continuation)
    }

    private func loadOrCreateCredentials() throws -> RiderCredentials {
        if let existing = try credentialStore.load() { return existing }
        let fresh = RiderCredentials(installationID: UUID().uuidString)
        try credentialStore.save(fresh)
        return fresh
    }

    /// Registers and saves the result against the same installation id — the
    /// device's identity survives every token it is issued — returning the token.
    private func register(
        installationID: String,
        into continuation: AsyncStream<RideEvent>.Continuation?
    ) async throws -> String {
        let response = try await client.register(
            installationID: installationID,
            appID: configuration.appID,
            appVersion: configuration.appVersion
        )
        try credentialStore.save(
            RiderCredentials(installationID: installationID, riderID: response.riderID, token: response.token)
        )
        continuation?.yield(.registered(riderID: response.riderID))
        return response.token
    }

    /// Forgets the token but not the installation id, so the next `start`
    /// re-registers as the same device.
    private func clearStoredToken() {
        guard var credentials = try? credentialStore.load() else { return }
        credentials.token = nil
        try? credentialStore.save(credentials)
    }

    // MARK: - Location

    private func runLocationLoop(
        rideID: String,
        updates: AsyncThrowingStream<LocationSample, any Error>
    ) async {
        do {
            for try await sample in updates {
                if await consume(sample, rideID: rideID) { return }
            }
        } catch {
            // Cancellation is teardown, not a failure; fall through otherwise.
        }
        guard !Task.isCancelled else { return }
        // The stream ended on its own: without positions there is no ride.
        await endIfCurrent(rideID: rideID, reason: .locationUnavailable)
    }

    /// Applies one sample. Returns true when the loop should stop.
    ///
    /// The ride is mutated in place, through `active`, rather than read into a
    /// local and written back: `ActiveRide` holds the sample buffer, and while
    /// a copy of the ride is alive the buffer's array is shared, so appending
    /// to the copy would deep-copy every buffered fix on every single sample.
    /// Nothing between the first read and the last write suspends, so no other
    /// call can interleave and see the ride half-updated.
    private func consume(_ sample: LocationSample, rideID: String) async -> Bool {
        guard active?.rideID == rideID, active?.ending == false else { return true }

        // Buffered before the end conditions are judged: the fix that shows
        // the rider at their stop is the one the server most needs to see,
        // and `end` sends only what is buffered.
        if let fix = sample.fix {
            active?.buffer.append(fix, now: clock.now)
        }
        if let reason = active?.evaluator.evaluate(sample, now: clock.now) {
            await end(reason: reason)
            return true
        }

        switch sample.diagnostic {
        case .accuracyLimited where active?.warnedAccuracy == false:
            active?.warnedAccuracy = true
            active?.continuation.yield(.warning(.accuracyLimited))
        case .insufficientlyInUse where active?.warnedInUse == false:
            active?.warnedInUse = true
            active?.continuation.yield(.warning(.insufficientlyInUse))
        default:
            break
        }

        // A full batch goes now rather than waiting out the interval — but not
        // while uploads are failing, which would ride straight over the
        // backoff, and not while the server is pacing us, which would trip
        // its limit on every sample of a backlog.
        let batchIsFull = active.map {
            $0.buffer.count >= $0.maxBatchSize && $0.failureAttempts == 0 && !$0.throttled
        } ?? false
        if batchIsFull { await flush() }
        return false
    }

    // MARK: - Uploading

    private func runUploadLoop(rideID: String) async {
        while true {
            guard let ride = active, ride.rideID == rideID, !ride.ending else { return }
            let delay = ride.failureAttempts == 0 ? ride.reportInterval : Self.backoff(attempt: ride.failureAttempts)
            do {
                try await clock.sleep(for: delay)
            } catch {
                return // Cancelled: the ride is being torn down.
            }
            guard !Task.isCancelled, let current = active, current.rideID == rideID, !current.ending else { return }

            if clock.now.timeIntervalSince(current.startedAt) >= configuration.maxRideDuration.timeInterval {
                await end(reason: .maxDuration)
                return
            }
            // Core Location pauses `liveUpdates` while the device is still, so
            // the sample that would have proved the timeout may never arrive.
            // The clock has to decide it instead.
            if let since = current.evaluator.stationarySince,
               clock.now.timeIntervalSince(since) >= configuration.stationaryTimeout.timeInterval {
                await end(reason: .stationary)
                return
            }
            await flush()
        }
    }

    /// Doubling backoff, capped at 30 s.
    private static func backoff(attempt: Int) -> Duration {
        .seconds(min(30, 1 << max(0, min(attempt - 1, 5))))
    }

    /// Sends one batch, if there is one, and folds the answer back into the ride.
    private func flush() async {
        guard !isUploading, active?.ending == false,
              let rideID = active?.rideID, let token = active?.token,
              let maxBatchSize = active?.maxBatchSize
        else { return }
        // Taken in place, through `active`: see `consume`.
        let batch = active?.buffer.take(max: maxBatchSize) ?? []
        guard !batch.isEmpty else { return }
        isUploading = true
        active?.inFlight = batch
        defer {
            isUploading = false
            if active?.rideID == rideID { active?.inFlight = [] }
        }

        do {
            let response = try await client.uploadPositions(
                token: token,
                rideID: rideID,
                positions: batch.map(Self.upload)
            )
            guard var current = active, current.rideID == rideID, !current.ending else { return }
            current.failureAttempts = 0
            current.failingSince = nil
            current.throttled = false
            current.state = response.state
            current.continuation.yield(.progress(RideProgress(
                state: response.state,
                published: response.published,
                corroboration: response.corroboration,
                pointsAccepted: response.accepted,
                offRouteStreak: response.offRouteStreak
            )))
            active = current
            if response.ended {
                await end(reason: response.endReasonValue ?? .idle)
            }
        } catch is CancellationError {
            restore(batch, rideID: rideID)
        } catch RideError.alreadyEnded {
            // The server has no such ride any more; a new one must be started.
            await endIfCurrent(rideID: rideID, reason: .serverRestart)
        } catch RideError.notAuthorized {
            clearStoredToken()
            await endIfCurrent(rideID: rideID, reason: .serverRestart)
        } catch RideError.server(let status, _) where status == 429 {
            throttle(batch, rideID: rideID)
        } catch RideError.server(let status, _) where Self.isPermanentRejection(status) {
            dropBatch(status: status, rideID: rideID)
        } catch RideError.decoding {
            // The server answered something this build cannot read. Retrying
            // sends the same bytes again; the batch goes.
            dropBatch(status: 0, rideID: rideID)
        } catch {
            await recordUploadFailure(batch, rideID: rideID)
        }
    }

    /// A 4xx other than 401, 409 and 429 is the server saying this batch is
    /// wrong, not that it is busy: putting it back would resend it every cycle
    /// for the rest of the ride.
    private static func isPermanentRejection(_ status: Int) -> Bool {
        (400..<500).contains(status) && status != 401 && status != 409 && status != 429
    }

    /// Drops a batch the server will never accept: not restored to the buffer,
    /// and not counted against the retry budget, since nothing is being retried.
    private func dropBatch(status: Int, rideID: String) {
        guard let ride = active, ride.rideID == rideID, !ride.ending else { return }
        ride.continuation.yield(.warning(.batchRejected(status: status)))
    }

    private func restore(_ batch: [LocationFix], rideID: String) {
        // A ride being ended has already reclaimed the in-flight batch itself.
        guard active?.rideID == rideID, active?.ending == false else { return }
        // Restored in place, through `active`: see `consume`.
        active?.buffer.restore(batch)
    }

    /// The server is pacing this ride, not failing it: the batch waits for the
    /// next periodic flush, which is neither a retry nor a warning to the host,
    /// and the batch-full flush stands down until that one succeeds.
    private func throttle(_ batch: [LocationFix], rideID: String) {
        guard active?.rideID == rideID, active?.ending == false else { return }
        active?.buffer.restore(batch)
        active?.throttled = true
    }

    private func recordUploadFailure(_ batch: [LocationFix], rideID: String) async {
        guard active?.rideID == rideID, active?.ending == false else { return }
        // Restored in place, through `active`: see `consume`.
        active?.buffer.restore(batch)
        let attempt = (active?.failureAttempts ?? 0) + 1
        let since = active?.failingSince ?? clock.now
        active?.failureAttempts = attempt
        active?.failingSince = since
        active?.continuation.yield(.warning(.uploadRetrying(attempt: attempt)))

        if clock.now.timeIntervalSince(since) >= configuration.uploadFailureTimeout.timeInterval {
            await end(reason: .networkFailure)
        }
    }

    private func endIfCurrent(rideID: String, reason: RideEndReason) async {
        guard active?.rideID == rideID else { return }
        await end(reason: reason)
    }

    /// Core Location's `-1` for "not measured" becomes an omitted field.
    private static func upload(_ fix: LocationFix) -> PositionUpload {
        PositionUpload(
            latitude: fix.latitude,
            longitude: fix.longitude,
            accuracy: measured(fix.horizontalAccuracy),
            speed: measured(fix.speed),
            bearing: measured(fix.course),
            timestamp: Int(fix.timestamp.timeIntervalSince1970)
        )
    }

    private static func measured(_ value: Double) -> Double? {
        value < 0 ? nil : value
    }
}

