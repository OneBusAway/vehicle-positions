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
        /// Set for the duration of teardown so re-entrant `end` calls return.
        var ending = false
    }

    private var active: ActiveRide?
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
            let credentials = try loadOrCreateCredentials()
            var token: String
            if let stored = credentials.token {
                token = stored
            } else {
                token = try await register(installationID: credentials.installationID, into: continuation)
            }

            let response: StartRideResponse
            do {
                response = try await client.startRide(token: token, trip: trip)
            } catch RideError.notAuthorized {
                // The stored token has expired: one fresh registration, one retry.
                token = try await register(installationID: credentials.installationID, into: continuation)
                response = try await client.startRide(token: token, trip: trip)
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
        guard var ride = active, !ride.ending else { return }
        ride.ending = true
        active = ride

        locationTask?.cancel()
        uploadTask?.cancel()
        locationTask = nil
        uploadTask = nil

        // A ride the server ended itself has nothing left to learn from us;
        // every other reason — superseded and abandoned rides included — sends
        // what is still buffered before it goes.
        var batches: [[PositionUpload]] = []
        if !reason.isServerInitiated {
            while ride.buffer.count > 0 {
                batches.append(ride.buffer.take(max: ride.maxBatchSize).map(Self.upload))
            }
        }
        // Reasons only the server may reach are already known to it: reporting
        // one back would be rejected, and the ride is over there either way.
        let reportsEnd = reason.isClientReportable
        var summary: RideSummary?
        if !batches.isEmpty || reportsEnd {
            let pending = batches
            let client = self.client
            let token = ride.token
            let rideID = ride.rideID
            // Unstructured so this teardown outlives the cancellation of the
            // loop it may have been called from; every call is best-effort.
            summary = await Task { () -> RideSummary? in
                for batch in pending {
                    do {
                        _ = try await client.uploadPositions(token: token, rideID: rideID, positions: batch)
                    } catch {
                        break // A network that just failed will not do better.
                    }
                }
                guard reportsEnd else { return nil }
                return try? await client.endRide(token: token, rideID: rideID, reason: reason).summary
            }.value
        }

        ride.continuation.yield(.ended(reason, summary: summary))
        ride.continuation.finish()
        ride.handle.invalidate()
        // A `start` that ran while this teardown awaited the server owns the
        // slot now; clearing it would orphan the ride it just installed.
        if active?.rideID == ride.rideID { active = nil }
    }

    /// How the server sees `tripID` right now, registering first if this device
    /// has never registered.
    public func tripStatus(tripID: String, startDate: String?) async throws -> TripStatus {
        let credentials = try loadOrCreateCredentials()
        let token: String
        if let stored = credentials.token {
            token = stored
        } else {
            token = try await register(installationID: credentials.installationID, into: nil)
        }
        return try await client.tripStatus(token: token, tripID: tripID, startDate: startDate)
    }

    // MARK: - Credentials

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
    private func consume(_ sample: LocationSample, rideID: String) async -> Bool {
        guard var ride = active, ride.rideID == rideID, !ride.ending else { return true }

        if let reason = ride.evaluator.evaluate(sample, now: clock.now) {
            active = ride
            await end(reason: reason)
            return true
        }

        switch sample.diagnostic {
        case .accuracyLimited where !ride.warnedAccuracy:
            ride.warnedAccuracy = true
            ride.continuation.yield(.warning(.accuracyLimited))
        case .insufficientlyInUse where !ride.warnedInUse:
            ride.warnedInUse = true
            ride.continuation.yield(.warning(.insufficientlyInUse))
        default:
            break
        }

        if let fix = sample.fix {
            ride.buffer.append(fix, now: clock.now)
        }
        // A full batch goes now rather than waiting out the interval — but not
        // while uploads are failing, which would ride straight over the backoff.
        let batchIsFull = ride.buffer.count >= ride.maxBatchSize && ride.failureAttempts == 0
        active = ride
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
            await flush()
        }
    }

    /// Doubling backoff, capped at 30 s.
    private static func backoff(attempt: Int) -> Duration {
        .seconds(min(30, 1 << max(0, min(attempt - 1, 5))))
    }

    /// Sends one batch, if there is one, and folds the answer back into the ride.
    private func flush() async {
        guard var ride = active, !ride.ending, ride.buffer.count > 0 else { return }
        let rideID = ride.rideID
        let token = ride.token
        let batch = ride.buffer.take(max: ride.maxBatchSize)
        active = ride

        do {
            let response = try await client.uploadPositions(
                token: token,
                rideID: rideID,
                positions: batch.map(Self.upload)
            )
            guard var current = active, current.rideID == rideID, !current.ending else { return }
            current.failureAttempts = 0
            current.failingSince = nil
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
        } catch {
            await recordUploadFailure(batch, rideID: rideID)
        }
    }

    private func restore(_ batch: [LocationFix], rideID: String) {
        guard var ride = active, ride.rideID == rideID else { return }
        ride.buffer.restore(batch)
        active = ride
    }

    private func recordUploadFailure(_ batch: [LocationFix], rideID: String) async {
        guard var ride = active, ride.rideID == rideID, !ride.ending else { return }
        ride.buffer.restore(batch)
        ride.failureAttempts += 1
        let since = ride.failingSince ?? clock.now
        ride.failingSince = since
        ride.continuation.yield(.warning(.uploadRetrying(attempt: ride.failureAttempts)))
        active = ride

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

extension RideEndReason {
    /// Reasons the server reaches on its own. It ended the ride before we knew,
    /// so there is nothing left worth sending it.
    var isServerInitiated: Bool {
        switch self {
        case .offRoute, .contradicted, .implausible, .offSchedule, .serverRestart, .idle:
            true
        case .userRequested, .arrived, .stationary, .maxDuration, .locationUnavailable,
             .authorizationDenied, .networkFailure, .appTerminated, .superseded:
            false
        }
    }
}
