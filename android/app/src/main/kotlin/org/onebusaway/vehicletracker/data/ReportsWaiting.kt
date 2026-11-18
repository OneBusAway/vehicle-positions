package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.onStart
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import javax.inject.Inject
import kotlin.time.Duration.Companion.seconds

/** Whether anything will send the reports waiting on the phone. */
enum class WaitingOutlook {
    /**
     * They were saved under this sign-in. The backlog upload runs as soon as there is a connection, and each live
     * report that gets through sends a batch.
     */
    WILL_SEND,

    /**
     * They were saved under another sign-in, or before the queue had an owner. Nothing uploads them under this one,
     * which would store them as this driver's, and its next sign-in drops them.
     */
    NEVER,
}

/** Reports saved for want of a connection, waiting on the phone to go to the server for location history. */
data class ReportsWaiting(val count: Int, val outlook: WaitingOutlook)

/**
 * What the queue holds at [nowEpochSec], as the status card and the notification say it: null while it holds no fix
 * young enough to upload. Older fixes are left out, as the uploads drop them rather than send them. The account is the one the uploads
 * check. An expired sign-in doesn't count against them: signing in again as the same driver keeps them, and the
 * Signed out card covers the status card meanwhile.
 */
internal fun reportsWaiting(contents: QueueContents, session: Session, nowEpochSec: Long): ReportsWaiting? {
    val count = contents.timestamps.count { !tooOldToUpload(it, nowEpochSec) }
    if (count == 0) return null
    val serverUrl = session.serverUrl
    val email = session.email
    val ours = serverUrl != null && email != null && contents.owner == offlineQueueAccount(serverUrl, email)
    return ReportsWaiting(count, if (ours) WaitingOutlook.WILL_SEND else WaitingOutlook.NEVER)
}

/** The reports waiting in [OfflineFixQueue] for the driver signed in, as they change. */
class WaitingReports @Inject constructor(
    queue: OfflineFixQueue,
    sessionStore: SessionStore,
    @param:EpochSecondsClock private val clock: () -> Long,
) {
    /** Null at first, so nothing waits for the queue's file to be read, and whenever no fix young enough is queued. */
    val waiting: Flow<ReportsWaiting?> = channelFlow {
        combine(queue.contents, sessionStore.session, ::Pair).collectLatest { (contents, session) ->
            while (true) {
                val now = clock()
                send(reportsWaiting(contents, session, now))
                // Counted again as the oldest fix counted ages out, so a card left on screen with no new fixes
                // still says what the uploads would send.
                val next = contents.timestamps.filterNot { tooOldToUpload(it, now) }.minOrNull() ?: break
                delay((tooOldToUploadFrom(next) - now).seconds)
            }
        }
    }
        .onStart { emit(null) }
        .distinctUntilChanged()
}
