package org.onebusaway.vehicletracker.service

import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel
import org.onebusaway.vehicletracker.ui.tracking.deviationMinutes
import org.onebusaway.vehicletracker.ui.tracking.statusCardModel
import java.time.ZoneId

/** The status card's answer, as the notification's title says it. */
internal sealed interface NotificationStatus {
    /** Whole minutes off schedule, as the card rounds them: positive is late, 0 is on time. */
    data class Minutes(val late: Int) : NotificationStatus
    data object OffRoute : NotificationStatus
    data object WaitingForGps : NotificationStatus
    data object NoSchedule : NotificationStatus
}

/**
 * What the tracking notification says, decided here and worded from strings.xml by [TrackingNotification]:
 * "Route 7 · On time" over "Next 3rd Ave & Seneca St · 7:18 AM · Reporting", or over the problem in place of the
 * reporting line. [route] is null before the stored trip has been read. Two of these are equal exactly when the
 * notification would read the same, so the service posts only when it changes.
 */
internal data class NotificationText(
    val route: String?,
    val status: NotificationStatus,
    val nextStop: TripStop?,
    val zone: ZoneId?,
    val problem: TrackingProblem,
)

internal fun notificationText(tracking: TrackingState, trip: ActiveTrip?): NotificationText {
    val model = statusCardModel(tracking, trip?.vehicleId)
    val status = when (model) {
        is StatusCardModel.Adherence -> NotificationStatus.Minutes(deviationMinutes(model.deviationSec))
        is StatusCardModel.OffSchedule -> NotificationStatus.Minutes(deviationMinutes(model.deviationSec))
        // Without the distance, which changes with every fix.
        is StatusCardModel.OffRoute -> NotificationStatus.OffRoute
        is StatusCardModel.WaitingForGps -> NotificationStatus.WaitingForGps
        StatusCardModel.NoSchedule -> NotificationStatus.NoSchedule
    }
    val (nextStop, zone) = when (model) {
        is StatusCardModel.Adherence -> model.nextStop to model.zone
        is StatusCardModel.OffSchedule -> model.nextStop to model.zone
        is StatusCardModel.OffRoute -> model.nextStop to model.zone
        else -> null to null
    }
    return NotificationText(
        route = tracking.geometry?.routeShortName?.ifEmpty { null } ?: trip?.routeId,
        status = status,
        nextStop = nextStop,
        zone = zone,
        problem = tracking.problem,
    )
}
