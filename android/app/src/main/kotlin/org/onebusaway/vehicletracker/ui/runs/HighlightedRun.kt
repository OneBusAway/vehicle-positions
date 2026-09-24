package org.onebusaway.vehicletracker.ui.runs

import org.onebusaway.vehicletracker.data.api.RouteTripsDto
import org.onebusaway.vehicletracker.data.api.TripSummaryDto
import java.time.Instant
import java.time.LocalTime
import java.time.OffsetDateTime
import java.time.ZoneId

/** One run of a route on a service date, with its wire times resolved to instants. */
data class TripRun(
    val id: String,
    val headsign: String,
    val firstStop: String,
    val lastStop: String,
    val startsAt: Instant,
    val endsAt: Instant,
)

/** One route's runs on one service date, with the zone their clock times are read in. */
data class RunPage(
    val serviceDate: String,
    val timezone: ZoneId,
    val runs: List<TripRun>,
)

/** Whether the highlighted run is the one under way or the next one to start. */
enum class RunHighlight { NOW, NEXT }

/**
 * The run the driver is most likely about to drive: the one whose window contains [now],
 * inclusive at both ends, and otherwise the earliest still to start. A port of
 * `TripRun.highlighted` in `ios/VehicleTracker/UI/Formatters.swift`.
 */
fun highlightedRun(runs: List<TripRun>, now: Instant): TripRun? =
    runs.firstOrNull { it.startsAt <= now && now <= it.endsAt }
        ?: runs.filter { it.startsAt > now }.minByOrNull { it.startsAt }

/** Which badge [run] carries: it is either already under way or still to come. */
fun highlightOf(run: TripRun, now: Instant): RunHighlight =
    if (run.startsAt <= now) RunHighlight.NOW else RunHighlight.NEXT

/**
 * Reads a route's runs off the wire. Go writes `time.Time` as RFC 3339 with the agency's zone
 * offset — `+03:00` unless that zone is UTC — and `Instant.parse` rejects an offset, so the
 * times are read as an [OffsetDateTime]. Throws when the server names a zone or a time this
 * platform cannot read; the caller turns that into a failed load.
 */
fun RouteTripsDto.toRunPage(): RunPage = RunPage(
    serviceDate = serviceDate,
    timezone = ZoneId.of(timezone),
    runs = trips.map { it.toRun() },
)

private fun TripSummaryDto.toRun(): TripRun = TripRun(
    id = id,
    headsign = headsign,
    firstStop = firstStop,
    lastStop = lastStop,
    startsAt = OffsetDateTime.parse(startsAt).toInstant(),
    endsAt = OffsetDateTime.parse(endsAt).toInstant(),
)

/**
 * A schedule time as the agency's clock shows it. The zone is the one the response named, never
 * the device's: a driver whose phone is in the wrong zone still has to read the agency's 07:02.
 */
fun clockTime(instant: Instant, zone: ZoneId): LocalTime = instant.atZone(zone).toLocalTime()

private const val SERVICE_DATE_LENGTH = 8

/** `"20260922"` as `"2026-09-22"`, the header `TripsView` puts over the day's runs. */
fun serviceDateLabel(serviceDate: String): String =
    if (serviceDate.length == SERVICE_DATE_LENGTH) {
        "${serviceDate.take(4)}-${serviceDate.substring(4, 6)}-${serviceDate.takeLast(2)}"
    } else {
        serviceDate
    }
