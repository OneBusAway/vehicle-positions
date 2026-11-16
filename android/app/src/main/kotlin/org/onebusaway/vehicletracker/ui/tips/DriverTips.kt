package org.onebusaway.vehicletracker.ui.tips

import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverFlagsStore
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.SessionStore
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

private const val TAG = "DriverTips"

/** What a driver is shown once after the welcome: a tip on two of the real screens, and a sheet after the first trip. */
enum class Tip(internal val seen: DriverFlag) {
    /** On "Choose a run", pointing at the run the driver is most likely to tap. */
    RUN_BOARD(DriverFlag.RunBoardTipSeen),

    /** On Tracking, pointing at the sent count, before the vehicle moves on the driver's first trip. */
    SENT_COUNT(DriverFlag.SentCountTipSeen),

    /** "First trip done", on the step that follows the first End trip. */
    FIRST_TRIP_DONE(DriverFlag.FirstTripDoneSeen),
}

/** A driver's first trip, just ended, and the reports it sent: what "First trip done" shows them. */
data class FinishedTrip(val driver: DriverKey, val sent: Int)

/**
 * Each [Tip] is shown once to each driver, under the welcome's own per-driver record, and only one is on screen at a
 * time: [onScreen]. What a driver has seen is written on [scope], the app's, so a tip closed by the tap that leaves its
 * screen, as a run tap leaves the run board, is still recorded once that screen has gone.
 */
@Singleton
class DriverTips @Inject constructor(
    private val sessionStore: SessionStore,
    private val flags: DriverFlagsStore,
    private val scope: CoroutineScope,
) {
    private val _onScreen = MutableStateFlow<Tip?>(null)

    /** The tip showing now, or null. */
    val onScreen: StateFlow<Tip?> = _onScreen.asStateFlow()

    private val _finishedFirstTrip = MutableStateFlow<FinishedTrip?>(null)

    /** A first trip whose "First trip done" is still to show, or null. */
    val finishedFirstTrip: StateFlow<FinishedTrip?> = _finishedFirstTrip.asStateFlow()

    /** The driver signed in now, or null when nobody is, or the session is one an older build stored without the email. */
    suspend fun signedIn(): DriverKey? = DriverKey.of(sessionStore.session.first())

    /**
     * Whether [driver] is still to be shown [tip]. Not when the phone can't read its record: a tip is never worth an
     * error, and the trip goes on without it.
     */
    suspend fun isDue(driver: DriverKey, tip: Tip): Boolean = try {
        !flags.isSet(driver, tip.seen)
    } catch (e: IOException) {
        Log.w(TAG, "Could not read whether $tip was seen; leaving it out", e)
        false
    }

    /** [driver] has seen [tip], and is not shown it again. */
    fun markSeen(driver: DriverKey, tip: Tip) {
        scope.launch {
            try {
                flags.set(driver, tip.seen)
            } catch (e: IOException) {
                // As on a full disk. The driver is shown it once more.
                Log.w(TAG, "Could not record that $tip was seen; it will show again", e)
            }
        }
    }

    /** Puts [tip] on screen. False while another one is there, which keeps it until it has gone. */
    fun take(tip: Tip): Boolean = _onScreen.compareAndSet(null, tip) || _onScreen.value == tip

    /** [tip] has gone from the screen, and the next one may come. */
    fun leave(tip: Tip) {
        _onScreen.compareAndSet(tip, null)
    }

    /** [trip] was the driver's first, and sent reports: the step that follows shows "First trip done". */
    fun firstTripEnded(trip: FinishedTrip) {
        _finishedFirstTrip.value = trip
    }

    /** "First trip done" has been closed, or is not going to show. */
    fun forgetFinishedTrip() {
        _finishedFirstTrip.value = null
    }
}
