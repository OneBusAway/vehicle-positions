package org.onebusaway.vehicletracker.data

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * GTFS-RT `start_date` for a trip that began at [epochSec]: the calendar date in
 * [zone], formatted YYYYMMDD. The phone's zone stands in for the agency's — the
 * driver is physically in the agency's region. Trips that start after midnight
 * on an overnight schedule are dated by clock time, a known v1 limitation.
 */
fun serviceDate(epochSec: Long, zone: ZoneId): String =
    Instant.ofEpochSecond(epochSec).atZone(zone).toLocalDate().format(DateTimeFormatter.BASIC_ISO_DATE)
