package org.onebusaway.vehicletracker.ui.tips

import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel

/** From this speed up, in metres a second, the vehicle is moving: a slow walk, and above a parked phone's GPS drift. */
internal const val MOVING_SPEED_MPS = 1.0

/** What the tip on the sent count may do, from where the vehicle is at. */
internal enum class SentCountRule {
    /** The vehicle has not moved: the tip may show. */
    MAY_SHOW,

    /** The last fix gave no speed: the tip does not come, and one already up stays. */
    HOLD,

    /** The vehicle is moving: the tip goes by itself, and the driver has seen it. */
    MOVING,
}

/**
 * Whether the tip on Tracking's sent count may show, from what the status card says and the last fix's speed in m/s,
 * null when the fix has none. Only before the vehicle moves: while the card waits for GPS, or while the speed is known
 * and under [MOVING_SPEED_MPS]. A speed that is not a number, or is below zero, is not known.
 */
internal fun sentCountRule(card: StatusCardModel, speedMps: Double?): SentCountRule {
    val speed = speedMps?.takeIf { it >= 0.0 }
    return when {
        speed != null && speed >= MOVING_SPEED_MPS -> SentCountRule.MOVING
        speed != null || card is StatusCardModel.WaitingForGps -> SentCountRule.MAY_SHOW
        else -> SentCountRule.HOLD
    }
}
