package org.onebusaway.vehicletracker.ui.settings

import org.onebusaway.vehicletracker.data.map.MapDetails
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.downloadPercent
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/** The agency's street map as Settings shows it. */
internal sealed interface OfflineMap {
    /** [againFailed] is why the new copy the driver asked for did not come. [deleteFailed] is a delete that did not happen. */
    data class Ready(val details: MapDetails, val againFailed: MapFileState.Failed? = null, val deleteFailed: Boolean = false) : OfflineMap

    /** The first copy, or a new one the map here waits for. [total] is -1 until the host says. */
    data class Downloading(val bytes: Long, val total: Long) : OfflineMap {
        val percent: Int get() = downloadPercent(bytes, total)
    }

    /** [needs] is the map's size, when its host said. */
    data class NoSpace(val needs: Long?) : OfflineMap

    /** Not here yet. The app tries again by itself, or with [byItself] false only when it is next opened. */
    data class NotDownloaded(val byItself: Boolean) : OfflineMap

    /** The agency's link gives no map the phone can use: not a map, or [needsHttps]. */
    data class Unusable(val needsHttps: Boolean) : OfflineMap

    data object Deleted : OfflineMap

    /** Something the driver may want to do about. */
    val needsAttention: Boolean get() = this is NoSpace || this is NotDownloaded || this is Unusable
}

/**
 * The map as Settings shows it, from [state] and, for a map that is here, its [details]. Null when there is none to show:
 * the agency has no map, or the one here is another server's, which this one's [details] leave out.
 */
internal fun offlineMap(state: MapFileState, details: MapDetails?): OfflineMap? = when (state) {
    MapFileState.None -> null
    MapFileState.Deleted -> OfflineMap.Deleted
    is MapFileState.Downloading -> OfflineMap.Downloading(state.bytes, state.total)
    is MapFileState.Ready -> state.update?.let { OfflineMap.Downloading(it.bytes, it.total) } ?: details?.let { OfflineMap.Ready(it, state.againFailed, state.deleteFailed) }
    is MapFileState.Failed -> when (state.reason) {
        MapFailure.NO_SPACE -> OfflineMap.NoSpace(state.size)
        MapFailure.NETWORK -> OfflineMap.NotDownloaded(byItself = true)
        MapFailure.UNEXPECTED -> OfflineMap.NotDownloaded(byItself = false)
        MapFailure.INVALID_FILE -> OfflineMap.Unusable(needsHttps = false)
        MapFailure.CLEARTEXT_BLOCKED -> OfflineMap.Unusable(needsHttps = true)
    }
}

/** The day the map was fetched, "Nov 12, 2026", in [locale]. */
internal fun fetchedOn(atMs: Long, zone: ZoneId, locale: Locale): String =
    DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale).format(Instant.ofEpochMilli(atMs).atZone(zone))

/**
 * When the app last asked about the map: [today] with the time in it, as "Today, %1$s" makes "Today, 6:40 AM", or the day
 * and time, "Nov 24, 2026, 6:40 AM".
 */
internal fun checkedAt(epochSec: Long, nowEpochSec: Long, zone: ZoneId, locale: Locale, today: (String) -> String): String {
    val at = Instant.ofEpochSecond(epochSec).atZone(zone)
    return if (at.toLocalDate() == Instant.ofEpochSecond(nowEpochSec).atZone(zone).toLocalDate()) {
        today(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).format(at))
    } else {
        DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT).withLocale(locale).format(at)
    }
}
