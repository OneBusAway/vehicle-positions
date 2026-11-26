package org.onebusaway.vehicletracker.data.map

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.onebusaway.vehicletracker.data.AppLog
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
 * streets, so it leaves the screen and the server signed in is asked at once. Until that server's
 * map replaces it, signing in to the first server again puts it back on the screen.
 *
 * A file a map may have open is never changed or deleted (maplibre-native #3658: MapLibre crashes
 * if it is). A newer version downloads under a new name and the state moves to it, so the next
 * map to open reads it while an open one keeps the old. Files nothing points to any more are
 * deleted when the app next starts, before any map opens, and when the driver deletes the map
 * while no map is open.
 *
 * The driver can [delete] the map, and it stays off the phone for that server, whatever later
 * refreshes find, until they [getAgain]. A delete that can't be saved leaves the map, and says so.
 */
@Singleton
class MapRepository @Inject constructor(
    private val server: MapServer,
    private val store: MapFileStore,
    private val fetcher: MapFileFetcher,
    private val scope: CoroutineScope,
    private val sessionStore: SessionStore,
    @param:EpochSecondsClock private val now: () -> Long,
) : OfflineMapSource {
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
    override fun refresh() = runNext(nudges = true) { signIn -> keepTrying(signIn) }

    /**
     * Takes the server's map off the phone, and keeps it off: every refresh leaves it until [getAgain]. What is under way
     * stops first, so no download can bring it back. The file goes at once if no map is open, and otherwise when the app
     * next starts. One that can't be saved leaves the map here, saying so, for the driver to try again.
     */
    override fun delete() = runNext { signIn ->
        // Started, it is done: a refresh for the next sign-in waits for it.
        withContext(NonCancellable) {
            signIn?.let { orLogged("Deleting the map", otherwise = { deleteFailed() }) { deleteFor(it.server) } }
        }
    }

    /**
     * Downloads the server's map again, as a new file, and switches to it as an update does. One the driver deleted comes
     * back. Until it is here, a broken connection is tried again as for the first copy.
     */
    override fun getAgain() = runNext { signIn ->
        signIn ?: return@runNext
        val cleared = withContext(NonCancellable) {
            orLogged("Reading the map record", otherwise = { false }) {
                val record = store.load()
                if (signIn.server in record.deleted) store.save(record.copy(deleted = record.deleted - signIn.server))
                true
            }
        }
        if (cleared) keepTrying(signIn, asked = true)
    }

    override suspend fun details(): MapDetails? = orLogged("Reading the map record", otherwise = { null }) {
        val server = signIn()?.server
        val map = store.load().current?.takeIf { server != null && (it.server == server || it.server == null) }
        map?.let { store.stat(it.fileName) }?.let { (bytes, fetchedAtMs) -> MapDetails(bytes, fetchedAtMs, map.checkedAtEpochSec) }
    }

    /**
     * Runs [work] for the driver signed in, once what is under way has stopped. With [nudges], one for the same sign-in
     * already under way is left to go on, and one waiting to retry asks at once instead.
     */
    private fun runNext(nudges: Boolean = false, work: suspend (SignIn?) -> Unit) {
        scope.launch {
            val signIn = orLogged("Reading the session", otherwise = { null }) { signIn() }
            synchronized(lock) {
                if (nudges && running?.isActive == true && runningFor == signIn) {
                    waiting?.complete(Unit)
                    return@launch
                }
                val previous = running
                runningFor = signIn
                running = scope.launch {
                    // Even stopped itself, it waits, so what comes after it never runs beside a delete still saving.
                    withContext(NonCancellable) { previous?.cancelAndJoin() }
                    started.join()
                    work(signIn)
                }
            }
        }
    }

    private suspend fun deleteFor(server: String) {
        val record = store.load()
        // Another server's map is left for when it is signed in again.
        val left = record.current?.takeIf { it.server != server && it.server != null }
        val without = record.copy(current = left, pending = null, deleted = record.deleted + server)
        store.save(without)
        _state.value = MapFileState.Deleted
        if (OpenMaps.none) store.deleteUnlisted(without)
    }

    private fun deleteFailed() {
        val ready = _state.value as? MapFileState.Ready ?: return
        _state.value = MapFileState.Ready(ready.file, deleteFailed = true)
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

    /** [asked] is a download the driver asked for, of a map that may be here already. */
    private suspend fun keepTrying(signIn: SignIn?, asked: Boolean = false) {
        // With nobody signed in there is no server to ask for a map.
        signIn ?: return
        var wait = FIRST_RETRY_MS
        while (orLogged("The map check", otherwise = { failed(MapFailure.UNEXPECTED, retry = false, asked = asked) }) { check(signIn.server, asked) } == Outcome.RETRY) {
            val nudged = try {
                waitToRetry(wait)
            } catch (e: CancellationException) {
                // Stopped, as by a refresh for another driver: nothing tries again for this copy now.
                dropAgainFailed()
                throw e
            }
            // After a sign-out, or with another driver or server, this retry is no longer theirs: the next sign-in's own
            // refresh asks.
            if (orLogged("Reading the session", otherwise = { null }) { signIn() } != signIn) return dropAgainFailed()
            wait = if (nudged) FIRST_RETRY_MS else (wait * 2).coerceAtMost(LAST_RETRY_MS)
        }
    }

    /** Takes back that a delete could not be saved: the map's state is a later check's to say. */
    private fun dropDeleteFailed() {
        val ready = _state.value as? MapFileState.Ready ?: return
        if (ready.deleteFailed) _state.value = ready.copy(deleteFailed = false)
    }

    /** Takes back why a new copy the driver asked for did not come, once nothing tries for it again by itself. */
    private fun dropAgainFailed() {
        val ready = _state.value as? MapFileState.Ready ?: return
        if (ready.againFailed != null) _state.value = ready.copy(againFailed = null)
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

    /**
     * Checks the map for [signedInTo], the server signed in. [asked] downloads it again even when the one here is current,
     * showing its progress, and tries again after a broken connection.
     */
    private suspend fun check(signedInTo: String, asked: Boolean): Outcome {
        // A delete that failed before this check is old news once it runs, whatever it finds.
        dropDeleteFailed()
        val record = store.load()
        if (signedInTo in record.deleted) {
            _state.value = MapFileState.Deleted
            return Outcome.DONE
        }
        val kept = record.current
        // One from an older build, with no server, is most likely this server's, so it stays on screen while this
        // server is asked about it. One from another server shows that agency's streets, so it goes from the screen.
        // Its file stays until the next start either way, as a map on screen may be reading it.
        val current = kept?.takeIf { it.server == signedInTo || it.server == null }
        if (kept != null && current == null && _state.value is MapFileState.Ready) _state.value = MapFileState.None
        // Back from a server whose check left nothing in its place, this server's map goes on the screen again.
        if (current != null && _state.value !is MapFileState.Ready) _state.value = MapFileState.Ready(store.file(current.fileName))
        if (!asked && current?.server == signedInTo && record.pending == null && isRecent(current.checkedAtEpochSec)) return Outcome.DONE

        val firstCopy = current == null
        var shown: MapFileState? = null
        /** Shows how far [bytes] of [total] have got, as the first copy or as an update of the map here. */
        fun show(bytes: Long, total: Long) {
            val progress = MapFileState.Downloading(bytes, total)
            shown = (if (firstCopy) progress else MapFileState.Ready(store.file(current!!.fileName), progress)).also { _state.value = it }
        }
        // Asked for, it shows at once.
        if (asked) show(0, -1)
        try {
            return fetchFor(signedInTo, asked, record, kept, current, firstCopy, ::show)
        } finally {
            // A check that ends saying nothing, as when it is signed out, or is stopped for the next sign-in or by the
            // driver, maybe while downloading or saving what came, leaves no progress showing. Only its own is cleared.
            if (shown != null && _state.value == shown) _state.value = if (firstCopy) MapFileState.None else MapFileState.Ready(store.file(current!!.fileName))
        }
    }

    private suspend fun fetchFor(
        signedInTo: String,
        asked: Boolean,
        record: MapRecord,
        kept: StoredMap?,
        current: StoredMap?,
        firstCopy: Boolean,
        show: (Long, Long) -> Unit,
    ): Outcome {
        val url = when (val answer = server.ask(signedInTo)) {
            is MapServerAnswer.File -> answer.url
            MapServerAnswer.NoMap -> {
                // This server's map goes. Another server's is kept for when that server is signed in again. The files
                // stay until the next start: a map on screen may be reading one.
                val left = MapRecord(current = kept?.takeIf { current == null }, deleted = record.deleted)
                if (record != left) store.save(left)
                _state.value = MapFileState.None
                return Outcome.DONE
            }
            MapServerAnswer.SignedOut -> return Outcome.DONE
            MapServerAnswer.Unreachable -> return failed(MapFailure.NETWORK, asked = asked)
            // The client catches up with the sign-in in a moment: asked again then, with nothing shown meanwhile.
            MapServerAnswer.OtherServer -> return Outcome.RETRY
        }

        val rejected = record.rejected?.takeIf { it.url == url }
        // Asked for, it is fetched and checked again: the agency may have put it right.
        if (!asked && rejected != null && isRecent(rejected.atEpochSec)) return failed(MapFailure.INVALID_FILE, retry = false)

        val pending = record.pending?.takeIf { it.url == url } ?: PendingDownload(url, store.newFileName())
        // A download from a URL the server no longer gives is not coming back.
        record.pending?.takeIf { it.url != url }?.let { store.partFile(it.fileName).delete() }
        // The same URL is the same file whichever server gave it, so another server's copy is only checked. Asked for
        // again, it is downloaded whatever version it is.
        val known = if (asked) null else if (kept?.url == url) kept.version else rejected?.version
        var shownPercent = -1

        val result = fetcher.fetch(
            FetchRequest(url, store.partFile(pending.fileName), store.file(pending.fileName), pending.version, known),
            onVersion = { version -> store.save(store.load().copy(pending = pending.copy(version = version))) },
            onProgress = { bytes, total ->
                // An update keeps the map here on screen, and only Settings shows how it is getting on.
                val percent = downloadPercent(bytes, total)
                if (percent != shownPercent) {
                    shownPercent = percent
                    show(bytes, total)
                }
            },
        )
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
                    failed(MapFailure.INVALID_FILE, retry = false, asked = asked)
                }
            }
            is FetchResult.Complete -> {
                // The servers whose map was deleted as they are now, not as when the check began.
                store.save(MapRecord(current = StoredMap(url, pending.fileName, result.version, now(), signedInTo), deleted = store.load().deleted))
                _state.value = MapFileState.Ready(store.file(pending.fileName))
                Log.i(TAG, "Map file ready, ${result.length} bytes")
                Outcome.DONE
            }
            is FetchResult.Failed -> {
                AppLog.w(TAG, "Map file not downloaded: ${result.reason}")
                when (result.reason) {
                    MapFailure.INVALID_FILE -> {
                        store.save(store.load().copy(pending = null, rejected = RejectedMap(url, result.version ?: FileVersion(), now())))
                        failed(MapFailure.INVALID_FILE, retry = false, asked = asked)
                    }
                    MapFailure.NO_SPACE, MapFailure.CLEARTEXT_BLOCKED, MapFailure.UNEXPECTED -> failed(result.reason, retry = false, size = result.size, asked = asked)
                    MapFailure.NETWORK -> failed(MapFailure.NETWORK, asked = asked)
                }
            }
        }
    }

    /**
     * Shows [reason] while the phone has no map, with the file's [size] when known. A phone that has one keeps showing
     * it, and does not retry: the check is still due, so the next [refresh] tries again. A check the app makes by itself
     * says nothing more. A new copy the driver [asked] for says why it did not come, beside the map here, and is tried
     * for again after a broken connection as the first copy is.
     */
    private fun failed(reason: MapFailure, retry: Boolean = true, size: Long? = null, asked: Boolean = false): Outcome {
        val ready = _state.value as? MapFileState.Ready
        if (ready != null) {
            _state.value = if (asked) MapFileState.Ready(ready.file, againFailed = MapFileState.Failed(reason, size)) else ready.copy(update = null)
            return if (retry && asked) Outcome.RETRY else Outcome.DONE
        }
        _state.value = MapFileState.Failed(reason, size)
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
        AppLog.w(TAG, "$what failed", e)
        otherwise()
    }
}
