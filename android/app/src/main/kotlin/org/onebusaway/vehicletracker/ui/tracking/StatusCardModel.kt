package org.onebusaway.vehicletracker.ui.tracking

import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.Adherence
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import java.time.ZoneId

/**
 * What the status card says: the one answer the driver looks for, and the stop it was judged against. Decided
 * here, away from the words, so that every state can be checked without a device; the card reads the words
 * from strings.xml.
 */
internal sealed interface StatusCardModel {
    val tone: StatusTone

    /** On time, late or early: [status] is one of those three. */
    data class Adherence(
        override val tone: StatusTone,
        val status: AdherenceStatus,
        val deviationSec: Double,
        val nextStop: TripStop,
        val distanceToNextStopM: Double,
        val zone: ZoneId,
    ) : StatusCardModel

    /** Beyond the server's schedule window, which most often means the driver picked the wrong run. */
    data class OffSchedule(
        override val tone: StatusTone,
        val deviationSec: Double,
        val nextStop: TripStop,
        val zone: ZoneId,
    ) : StatusCardModel

    data class OffRoute(
        override val tone: StatusTone,
        val distanceFromRouteM: Double,
        val nextStop: TripStop,
        val distanceToNextStopM: Double,
        val zone: ZoneId,
    ) : StatusCardModel

    /**
     * No fix judged yet. The first stop and its zone are null only before the trip's shape and stops have
     * loaded; [vehicleId] is null when there is no stored trip to read it from.
     */
    data class WaitingForGps(val firstStop: TripStop?, val zone: ZoneId?, val vehicleId: String?) : StatusCardModel {
        override val tone get() = StatusTone.NEUTRAL
    }

    /** Reporting with nothing to judge against: the trip was started by an older build, or the phone lost it. */
    data object NoSchedule : StatusCardModel {
        override val tone get() = StatusTone.NEUTRAL
    }
}

internal fun statusCardModel(tracking: TrackingState, vehicleId: String?): StatusCardModel {
    val adherence = tracking.adherence
    val geometry = tracking.geometry
    return when {
        adherence != null && geometry != null -> judged(adherence, geometry.timezone)
        tracking.active && geometry == null -> StatusCardModel.NoSchedule
        else -> StatusCardModel.WaitingForGps(geometry?.stops?.firstOrNull(), geometry?.timezone, vehicleId)
    }
}

private fun judged(adherence: Adherence, zone: ZoneId): StatusCardModel {
    val tone = adherenceTone(adherence)
    return when (adherence.status) {
        AdherenceStatus.ON_TIME, AdherenceStatus.EARLY, AdherenceStatus.LATE -> StatusCardModel.Adherence(
            tone = tone,
            status = adherence.status,
            deviationSec = adherence.scheduleDeviationSec,
            nextStop = adherence.nextStop,
            distanceToNextStopM = adherence.distanceToNextStopM,
            zone = zone,
        )
        AdherenceStatus.OFF_SCHEDULE -> StatusCardModel.OffSchedule(
            tone = tone,
            deviationSec = adherence.scheduleDeviationSec,
            nextStop = adherence.nextStop,
            zone = zone,
        )
        AdherenceStatus.OFF_ROUTE -> StatusCardModel.OffRoute(
            tone = tone,
            distanceFromRouteM = adherence.projection.distanceToShape,
            nextStop = adherence.nextStop,
            distanceToNextStopM = adherence.distanceToNextStopM,
            zone = zone,
        )
    }
}
