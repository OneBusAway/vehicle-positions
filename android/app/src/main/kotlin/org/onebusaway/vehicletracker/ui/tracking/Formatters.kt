package org.onebusaway.vehicletracker.ui.tracking

import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.engine.Adherence
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.ui.runs.clockTime
import org.onebusaway.vehicletracker.ui.theme.StatusBlue
import org.onebusaway.vehicletracker.ui.theme.StatusGreen
import org.onebusaway.vehicletracker.ui.theme.StatusGrey
import org.onebusaway.vehicletracker.ui.theme.StatusRed
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToInt

// Text the driver reads at a glance, ported from `ios/VehicleTracker/UI/Formatters.swift`: the
// arithmetic in plain functions, the words from strings.xml.

private const val SECONDS_PER_MINUTE = 60.0
private const val METRES_PER_KILOMETRE = 1000.0

/** Whole minutes off schedule, rounded to the nearest minute, and 0 within a minute either way. Positive is late. */
internal fun deviationMinutes(seconds: Double): Int {
    if (abs(seconds) < SECONDS_PER_MINUTE) return 0
    val minutes = (abs(seconds) / SECONDS_PER_MINUTE).roundToInt()
    return if (seconds > 0) minutes else -minutes
}

/** OneBusAway's convention (spec §5.5): green on time, red early, blue late, grey off route. */
internal fun adherenceColor(adherence: Adherence): Color = when (adherence.status) {
    AdherenceStatus.ON_TIME -> StatusGreen
    AdherenceStatus.EARLY -> StatusRed
    AdherenceStatus.LATE -> StatusBlue
    AdherenceStatus.OFF_SCHEDULE -> if (adherence.scheduleDeviationSec < 0) StatusRed else StatusBlue
    AdherenceStatus.OFF_ROUTE -> StatusGrey
}

/**
 * A schedule time as the agency's clock shows it, in the same short style as the run list. The
 * zone is the trip's, never the device's: a phone set to the wrong zone still has to read the
 * agency's 07:12.
 */
internal fun formatClock(instant: Instant, zone: ZoneId, locale: Locale = Locale.getDefault()): String =
    DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).format(clockTime(instant, zone))

internal fun formatDuration(totalSeconds: Long): String {
    val s = totalSeconds.coerceAtLeast(0)
    val hours = s / 3600
    val minutes = (s % 3600) / 60
    val seconds = s % 60
    return if (hours > 0) {
        String.format(Locale.getDefault(), "%d:%02d:%02d", hours, minutes, seconds)
    } else {
        String.format(Locale.getDefault(), "%02d:%02d", minutes, seconds)
    }
}

/** "On time" within a minute, otherwise whole minutes late or early. */
@Composable
internal fun deviationText(seconds: Double): String {
    val minutes = deviationMinutes(seconds)
    return when {
        minutes > 0 -> pluralStringResource(R.plurals.tracking_adherence_minutes_late, minutes, minutes)
        minutes < 0 -> pluralStringResource(R.plurals.tracking_adherence_minutes_early, -minutes, -minutes)
        else -> stringResource(R.string.tracking_adherence_on_time)
    }
}

/** Whole metres below a kilometre, kilometres to one decimal place from there. */
@Composable
internal fun distanceText(metres: Double): String =
    if (metres < METRES_PER_KILOMETRE) {
        stringResource(R.string.tracking_adherence_distance_m, metres.roundToInt())
    } else {
        stringResource(R.string.tracking_adherence_distance_km, metres / METRES_PER_KILOMETRE)
    }

@Composable
internal fun adherenceLabel(adherence: Adherence): String = when (adherence.status) {
    AdherenceStatus.ON_TIME, AdherenceStatus.EARLY, AdherenceStatus.LATE -> deviationText(adherence.scheduleDeviationSec)
    AdherenceStatus.OFF_SCHEDULE ->
        stringResource(R.string.tracking_adherence_off_schedule, deviationText(adherence.scheduleDeviationSec))
    AdherenceStatus.OFF_ROUTE ->
        stringResource(R.string.tracking_adherence_off_route, distanceText(adherence.projection.distanceToShape))
}
