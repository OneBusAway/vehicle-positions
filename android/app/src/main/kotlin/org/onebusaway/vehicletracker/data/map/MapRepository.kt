package org.onebusaway.vehicletracker.data.map

import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
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
 * map.
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
    @param:EpochSecondsClock private val now: () -> Long,
) : MapFileSource {
    private val _state = MutableStateFlow<MapFileState>(MapFileState.None)
    override val state: StateFlow<MapFileState> = _state.asStateFlow()

    private val started: Job = scope.launch { start() }
    private val lock = Any()
    private var running: Job? = null

    /** Asks for the map, or checks the one here is current, unless a refresh is already under way. */
    fun refresh() {
        synchronized(lock) {
            if (running?.isActive == true) return
            running = scope.launch {
                started.join()
                keepTrying()
            }
        }
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

    private suspend fun keepTrying() {
        var wait = FIRST_RETRY_MS
        while (check() == Outcome.RETRY) {
            delay(wait)
            wait = (wait * 2).coerceAtMost(LAST_RETRY_MS)
        }
    }

    private suspend fun check(): Outcome {
        val record = store.load()
        val current = record.current
        if (current != null && record.pending == null && isRecent(current.checkedAtEpochSec)) return Outcome.DONE

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
        val known = if (current?.url == url) current.version else rejected?.version
        val firstCopy = current == null
        var shownPercent = -1

        val result = fetcher.fetch(
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
        return when (result) {
            FetchResult.Unchanged -> {
                val latest = store.load().copy(pending = null)
                if (current?.url == url) {
                    store.save(latest.copy(current = current.copy(checkedAtEpochSec = now())))
                    Outcome.DONE
                } else {
                    // The host still serves the file that was turned down.
                    store.save(latest.copy(rejected = rejected?.copy(atEpochSec = now())))
                    failed(MapFailure.INVALID_FILE, retry = false)
                }
            }
            is FetchResult.Complete -> {
                store.save(MapRecord(current = StoredMap(url, pending.fileName, result.version, now())))
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
                    MapFailure.NO_SPACE -> failed(MapFailure.NO_SPACE, retry = false)
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
}
