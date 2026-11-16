package org.onebusaway.vehicletracker.ui.tips

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.ui.tracking.statusCardModel
import javax.inject.Inject

/** Whether [tip] may come on screen: nothing is there, or it already is. */
private fun Tip?.leavesRoomFor(tip: Tip) = this == null || this == tip

/**
 * The run board's tip, for the driver signed in. It [show]s while they have still to see it and no other tip is up, and
 * it is seen once they close it, or tap a run, whether or not it had come yet.
 */
@HiltViewModel
class RunBoardTipViewModel @Inject constructor(private val tips: DriverTips) : ViewModel() {
    private val due = MutableStateFlow(false)
    private var driver: DriverKey? = null

    /** A run tapped before the record was read, which settles the tip as soon as it is. */
    private var tappedEarly = false

    val show: StateFlow<Boolean> = combine(due, tips.onScreen) { due, onScreen -> due && onScreen.leavesRoomFor(Tip.RUN_BOARD) }
        .stateIn(viewModelScope, SharingStarted.Eagerly, false)

    init {
        viewModelScope.launch {
            val signedIn = tips.signedIn() ?: return@launch
            val isDue = tips.isDue(signedIn, Tip.RUN_BOARD)
            driver = signedIn
            if (!tappedEarly) {
                due.value = isDue
            } else if (isDue) {
                tips.markSeen(signedIn, Tip.RUN_BOARD)
            }
        }
    }

    /** The tip is coming on screen. False while another one is there. */
    fun onShown(): Boolean = due.value && tips.take(Tip.RUN_BOARD)

    /** The tip went without the driver closing it, as when they go back: it shows again on their next visit. */
    fun onLeft() = tips.leave(Tip.RUN_BOARD)

    /** Got it, a tap elsewhere, or a run tapped. */
    fun onSeen() {
        val wasDue = due.value
        if (driver == null) tappedEarly = true
        due.value = false
        tips.leave(Tip.RUN_BOARD)
        if (wasDue) driver?.let { tips.markSeen(it, Tip.RUN_BOARD) }
    }

    override fun onCleared() = tips.leave(Tip.RUN_BOARD)
}

/**
 * The tip on Tracking's sent count, on the driver's first trip, and "First trip done" after it. The tip [show]s only
 * before the vehicle moves, as [sentCountRule] has it, and goes by itself once it does, which counts as seen: it never
 * comes on the road, and never comes back. The trip's end settles it too. Read off the tracking state here rather than
 * on screen, so a vehicle that moved off while Tracking was out of sight has still moved.
 */
@HiltViewModel
class SentCountTipViewModel @Inject constructor(
    private val tips: DriverTips,
    tracking: TrackingRepository,
) : ViewModel() {
    private val due = MutableStateFlow(false)
    private val shown = MutableStateFlow(false)
    private var driver: DriverKey? = null
    private var firstTripDoneDue = false

    /** The most this trip has sent. The tracking state starts again at zero as the service stops. */
    private var sentThisTrip = 0

    private val rule = tracking.state
        .map { sentCountRule(statusCardModel(it, vehicleId = null), it.adherence?.fix?.speed) }
        .distinctUntilChanged()

    val show: StateFlow<Boolean> = combine(due, rule, shown, tips.onScreen) { due, rule, shown, onScreen ->
        due && onScreen.leavesRoomFor(Tip.SENT_COUNT) && (rule == SentCountRule.MAY_SHOW || (rule == SentCountRule.HOLD && shown))
    }.stateIn(viewModelScope, SharingStarted.Eagerly, false)

    init {
        tracking.state.onEach { sentThisTrip = maxOf(sentThisTrip, it.fixesSent) }.launchIn(viewModelScope)
        viewModelScope.launch {
            val signedIn = tips.signedIn() ?: return@launch
            driver = signedIn
            firstTripDoneDue = tips.isDue(signedIn, Tip.FIRST_TRIP_DONE)
            due.value = tips.isDue(signedIn, Tip.SENT_COUNT)
        }
        combine(due, rule) { due, rule -> due && rule == SentCountRule.MOVING }
            .onEach { moving -> if (moving) settle() }
            .launchIn(viewModelScope)
    }

    /** The tip is coming on screen. False while another one is there. */
    fun onShown(): Boolean {
        if (!due.value || !tips.take(Tip.SENT_COUNT)) return false
        shown.value = true
        return true
    }

    /** The tip went without the driver closing it, as when the screen goes. */
    fun onLeft() {
        shown.value = false
        tips.leave(Tip.SENT_COUNT)
    }

    /** Got it, or a tap elsewhere. */
    fun onSeen() = settle()

    /**
     * The trip has ended. Its tip is settled, shown or not, and a first trip that sent reports is followed by "First
     * trip done".
     */
    fun onTripEnded() {
        settle()
        val driver = driver ?: return
        if (firstTripDoneDue && sentThisTrip > 0) {
            firstTripDoneDue = false
            tips.firstTripEnded(FinishedTrip(driver, sentThisTrip))
        }
    }

    private fun settle() {
        if (!due.value) return
        due.value = false
        shown.value = false
        tips.leave(Tip.SENT_COUNT)
        driver?.let { tips.markSeen(it, Tip.SENT_COUNT) }
    }

    override fun onCleared() = tips.leave(Tip.SENT_COUNT)
}

/** "First trip done", once a first trip has ended: [trip] while it is still to show, and then nothing. */
@HiltViewModel
class FirstTripDoneViewModel @Inject constructor(private val tips: DriverTips) : ViewModel() {
    /** The trip to show, while no tip is on screen; null otherwise. */
    val trip: StateFlow<FinishedTrip?> = combine(tips.finishedFirstTrip, tips.onScreen) { trip, onScreen ->
        trip?.takeIf { onScreen.leavesRoomFor(Tip.FIRST_TRIP_DONE) }
    }.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** The sheet is going up, and is seen from now on: it shows once, even if the app closes under it. */
    fun onShown(trip: FinishedTrip): Boolean {
        if (!tips.take(Tip.FIRST_TRIP_DONE)) return false
        tips.markSeen(trip.driver, Tip.FIRST_TRIP_DONE)
        return true
    }

    /** OK, a swipe down, Back, or a tap above the sheet. */
    fun onClosed() {
        tips.forgetFinishedTrip()
        tips.leave(Tip.FIRST_TRIP_DONE)
    }
}
