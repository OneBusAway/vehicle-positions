package org.onebusaway.vehicletracker.data

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * GTFS-RT `start_date` for a trip that began at [epochSec]: the calendar date in
 * [zone], formatted YYYYMMDD. The phone's zone stands in for the agency's, since the
 * driver is physically in the agency's region. A new trip carries the catalog's
 * `service_date` instead, so only [TripStateStore] uses this, to date a restored
 * trip saved with no start date. It dates an after-midnight run on an overnight
 * schedule by clock time, the day after its service date.
 */
fun serviceDate(epochSec: Long, zone: ZoneId): String =
    Instant.ofEpochSecond(epochSec).atZone(zone).toLocalDate().format(DateTimeFormatter.BASIC_ISO_DATE)
