package org.onebusaway.vehicletracker.data.map

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.normalizedServerUrl
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import javax.inject.Inject
import javax.inject.Singleton

private const val TAG = "MapRepository"

/** How long a map stays current before its host is asked whether there is a newer one. */
internal const val CHECK_EVERY_S = 24L * 60 * 60

internal const val FIRST_RETRY_MS = 5_000L
internal const val LAST_RETRY_MS = 5 * 60_000L

/**
 * Keeps the agency's offline street map on the phone: asks the server where the file is,
 * downloads it, and once it is here asks the file's host once a day whether it has changed.
 *
 * [refresh] starts that and returns at once. The work runs in the app's scope, so it carries on
 * across screens, and for a whole shift while the tracking service keeps the app alive. Until the
 * first copy is here, a network failure is tried again after 5 s, then twice as long each time up
 * to every 5 min. Anything else waits for the next [refresh]: a phone too full for the file rarely
 * empties itself, and each try would cost the driver data. So does a failed check once there is a
 * map. Retries are for the driver and server signed in when the refresh began: they stop once
 * that sign-in ends, and a refresh for the next one starts afresh. A refresh for the same sign-in
 * while a retry waits, as when the driver signs out and back in, asks at once and retries from 5 s.
 *
 * The map belongs to the server that gave it. One from another server shows another agency's
 * streets, so it leaves the screen and the server signed in is asked at once.
 *
 * A file a map may have open is never changed or deleted (maplibre-native #3658: MapLibre crashes
 * if it is). A newer version downloads under a new name and the state moves to it, so the next
 * map to open reads it while an open one keeps the old. Files nothing points to any more are
 * deleted when the app next starts, before any map opens.
 */
@Singleton
class MapRepository @Inject constructor(
    private val server: MapServer,
    private val store: MapFileStore,
    private val fetcher: MapFileFetcher,
    private val scope: CoroutineScope,
    private val sessionStore: SessionStore,
    @param:EpochSecondsClock private val now: () -> Long,
) : MapFileSource {
    private val _state = MutableStateFlow<MapFileState>(MapFileState.None)
    override val state: StateFlow<MapFileState> = _state.asStateFlow()

    private val started: Job = scope.launch { orLogged("Reading the map record", otherwise = {}) { start() } }
    private val lock = Any()
    private var running: Job? = null
    private var runningFor: SignIn? = null

    /** Completed to cut short the wait before the running refresh's next retry. */
    private var waiting: CompletableDeferred<Unit>? = null

    /**
     * Asks for the map, or checks the one here is current, unless a refresh for the same sign-in is already under
     * way. One waiting to retry asks at once instead. One for an earlier sign-in is stopped first.
     */
    fun refresh() {
        scope.launch {
            val signIn = orLogged("Reading the session", otherwise = { null }) { signIn() }
            synchronized(lock) {
                if (running?.isActive == true && runningFor == signIn) {
                    waiting?.complete(Unit)
                    return@launch
                }
                val previous = running
                runningFor = signIn
                running = scope.launch {
                    previous?.cancelAndJoin()
                    started.join()
                    keepTrying(signIn)
                }
            }
        }
    }

    /** Who the map is being asked for: the server, as [normalizedServerUrl] gives it, and the driver signed in. */
    private data class SignIn(val server: String, val email: String?)

    /** Null when nobody is signed in. */
    private suspend fun signIn(): SignIn? {
        val session = sessionStore.session.first()
        val serverUrl = session.serverUrl?.takeIf { session.token != null } ?: return null
        return SignIn(normalizedServerUrl(serverUrl), session.email)
    }

    private suspend fun start() {
        val record = store.load()
        store.deleteUnlisted(record)
        val current = record.current ?: return
        val file = store.file(current.fileName)
        if (file.exists()) {
            _state.value = MapFileState.Ready(file)
        } else {
            store.save(record.copy(current = null))
        }
    }

    private enum class Outcome { DONE, RETRY }

    private suspend fun keepTrying(signIn: SignIn?) {
        // With nobody signed in there is no server to ask for a map.
        signIn ?: return
        var wait = FIRST_RETRY_MS
        while (orLogged("The map check", otherwise = { failed(MapFailure.UNEXPECTED, retry = false) }) { check(signIn.server) } == Outcome.RETRY) {
            val nudged = waitToRetry(wait)
            // The server asks whoever is signed in by now. After a sign-out, or with another driver or server, this
            // retry is no longer theirs: the next sign-in's own refresh asks.
            if (orLogged("Reading the session", otherwise = { null }) { signIn() } != signIn) return
            wait = if (nudged) FIRST_RETRY_MS else (wait * 2).coerceAtMost(LAST_RETRY_MS)
        }
    }

    /** Waits [wait] ms, or less if a [refresh] for the same sign-in comes meanwhile. True if one did. */
    private suspend fun waitToRetry(wait: Long): Boolean {
        val nudge = CompletableDeferred<Unit>()
        synchronized(lock) { waiting = nudge }
        return try {
            withTimeoutOrNull(wait) { nudge.await() } != null
        } finally {
            synchronized(lock) { if (waiting === nudge) waiting = null }
        }
    }

    /** Checks the map for [signedInTo], the server signed in. */
    private suspend fun check(signedInTo: String): Outcome {
        val record = store.load()
        val kept = record.current
        // One from an older build, with no server, is most likely this server's, so it stays on screen while this
        // server is asked about it. One from another server shows that agency's streets, so it goes from the screen.
        // Its file stays until the next start either way, as a map on screen may be reading it.
        val current = kept?.takeIf { it.server == signedInTo || it.server == null }
        if (kept != null && current == null && _state.value is MapFileState.Ready) _state.value = MapFileState.None
        if (current?.server == signedInTo && record.pending == null && isRecent(current.checkedAtEpochSec)) return Outcome.DONE

        val url = when (val answer = server.ask()) {
            is MapServerAnswer.File -> answer.url
            MapServerAnswer.NoMap -> {
                // The files stay until the next start: a map on screen may be reading one.
                if (record != MapRecord()) store.save(MapRecord())
                _state.value = MapFileState.None
                return Outcome.DONE
            }
            MapServerAnswer.SignedOut -> return Outcome.DONE
            MapServerAnswer.Unreachable -> return failed(MapFailure.NETWORK)
        }

        val rejected = record.rejected?.takeIf { it.url == url }
        if (rejected != null && isRecent(rejected.atEpochSec)) return failed(MapFailure.INVALID_FILE, retry = false)

        val pending = record.pending?.takeIf { it.url == url } ?: PendingDownload(url, store.newFileName())
        // A download from a URL the server no longer gives is not coming back.
        record.pending?.takeIf { it.url != url }?.let { store.partFile(it.fileName).delete() }
        // The same URL is the same file whichever server gave it, so another server's copy is only checked.
        val known = if (kept?.url == url) kept.version else rejected?.version
        val firstCopy = current == null
        var shownPercent = -1

        val result = try {
            fetcher.fetch(
                FetchRequest(url, store.partFile(pending.fileName), store.file(pending.fileName), pending.version, known),
                onVersion = { version -> store.save(store.load().copy(pending = pending.copy(version = version))) },
                onProgress = { bytes, total ->
                    // An update downloads quietly: the map already here is still good.
                    val percent = downloadPercent(bytes, total)
                    if (firstCopy && percent != shownPercent) {
                        shownPercent = percent
                        _state.value = MapFileState.Downloading(bytes, total)
                    }
                },
            )
        } catch (e: CancellationException) {
            // Stopped for the next sign-in, whose check may end without saying anything, as when it is signed out.
            if (_state.value is MapFileState.Downloading) _state.value = MapFileState.None
            throw e
        }
        return when (result) {
            FetchResult.Unchanged -> {
                val latest = store.load().copy(pending = null)
                if (kept?.url == url) {
                    store.save(latest.copy(current = kept.copy(checkedAtEpochSec = now(), server = signedInTo)))
                    _state.value = MapFileState.Ready(store.file(kept.fileName))
                    Outcome.DONE
                } else {
                    // The host still serves the file that was turned down.
                    store.save(latest.copy(rejected = rejected?.copy(atEpochSec = now())))
                    failed(MapFailure.INVALID_FILE, retry = false)
                }
            }
            is FetchResult.Complete -> {
                store.save(MapRecord(current = StoredMap(url, pending.fileName, result.version, now(), signedInTo)))
                _state.value = MapFileState.Ready(store.file(pending.fileName))
                Log.i(TAG, "Map file ready, ${result.length} bytes")
                Outcome.DONE
            }
            is FetchResult.Failed -> {
                Log.w(TAG, "Map file not downloaded: ${result.reason}")
                when (result.reason) {
                    MapFailure.INVALID_FILE -> {
                        store.save(store.load().copy(pending = null, rejected = RejectedMap(url, result.version ?: FileVersion(), now())))
                        failed(MapFailure.INVALID_FILE, retry = false)
                    }
                    MapFailure.NO_SPACE, MapFailure.CLEARTEXT_BLOCKED, MapFailure.UNEXPECTED -> failed(result.reason, retry = false)
                    MapFailure.NETWORK -> failed(MapFailure.NETWORK)
                }
            }
        }
    }

    /**
     * Shows [reason] while the phone has no map. A phone that has one keeps showing it, and does
     * not retry: the check is still due, so the next [refresh] tries again.
     */
    private fun failed(reason: MapFailure, retry: Boolean = true): Outcome {
        if (_state.value is MapFileState.Ready) return Outcome.DONE
        _state.value = MapFileState.Failed(reason)
        return if (retry) Outcome.RETRY else Outcome.DONE
    }

    private fun isRecent(epochSec: Long): Boolean = now() - epochSec in 0 until CHECK_EVERY_S

    /**
     * Runs [block], logging what it throws, such as an IOException from a save on a full phone,
     * and answering [otherwise] instead. Nothing above this catches it, and an exception that
     * leaves the app's scope crashes the app. Cancellation passes through.
     */
    private inline fun <T> orLogged(what: String, otherwise: () -> T, block: () -> T): T = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Log.w(TAG, "$what failed", e)
        otherwise()
    }
}
