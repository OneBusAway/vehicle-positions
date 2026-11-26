package org.onebusaway.vehicletracker.ui.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.map.MapDetails
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import java.io.File
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.Locale

/** The map as Settings shows it, from where it stands on the phone, and the dates on its page. */
class OfflineMapTest {
    private val file = File("map-a.pmtiles")
    private val details = MapDetails(bytes = 48_000_000, fetchedAtMs = 1_789_000_000_000L, checkedAtEpochSec = 1_790_000_000L)

    @Test fun `each state is the page's`() {
        assertNull("the agency has none", offlineMap(MapFileState.None, null))
        assertEquals(OfflineMap.Ready(details), offlineMap(MapFileState.Ready(file), details))
        assertEquals(OfflineMap.Downloading(20, 48), offlineMap(MapFileState.Downloading(20, 48), null))
        assertEquals(OfflineMap.NoSpace(48_000_000), offlineMap(MapFileState.Failed(MapFailure.NO_SPACE, 48_000_000), null))
        assertEquals(OfflineMap.NoSpace(null), offlineMap(MapFileState.Failed(MapFailure.NO_SPACE), null))
        assertEquals(OfflineMap.NotDownloaded(byItself = true), offlineMap(MapFileState.Failed(MapFailure.NETWORK), null))
        assertEquals(OfflineMap.NotDownloaded(byItself = false), offlineMap(MapFileState.Failed(MapFailure.UNEXPECTED), null))
        assertEquals(OfflineMap.Unusable(needsHttps = false), offlineMap(MapFileState.Failed(MapFailure.INVALID_FILE), null))
        assertEquals(OfflineMap.Unusable(needsHttps = true), offlineMap(MapFileState.Failed(MapFailure.CLEARTEXT_BLOCKED), null))
        assertEquals(OfflineMap.Deleted, offlineMap(MapFileState.Deleted, null))
    }

    @Test fun `a new copy on its way is shown downloading, though the map here stays on Tracking`() {
        assertEquals(OfflineMap.Downloading(10, 100), offlineMap(MapFileState.Ready(file, update = MapFileState.Downloading(10, 100)), details))
    }

    @Test fun `a new copy that did not come is shown beside the map here`() {
        val failed = MapFileState.Failed(MapFailure.NO_SPACE, 48_000_000)
        assertEquals(OfflineMap.Ready(details, failed), offlineMap(MapFileState.Ready(file, againFailed = failed), details))
    }

    @Test fun `a map here that is not this server's is not shown as its`() {
        assertNull(offlineMap(MapFileState.Ready(file), details = null))
    }

    @Test fun `only what the driver may want to do something about needs attention`() {
        val attention = listOf(OfflineMap.NoSpace(null), OfflineMap.NotDownloaded(true), OfflineMap.Unusable(false))
        val fine = listOf(OfflineMap.Ready(details), OfflineMap.Downloading(1, 2), OfflineMap.Deleted)
        for (map in attention) assertTrue("$map", map.needsAttention)
        for (map in fine) assertFalse("$map", map.needsAttention)
    }

    @Test fun `percent of a download, and 0 while its size is not known`() {
        assertEquals(41, OfflineMap.Downloading(20_000_000, 48_000_000).percent)
        assertEquals(0, OfflineMap.Downloading(0, -1).percent)
    }

    private val zone = ZoneId.of("America/Los_Angeles")
    private fun at(day: Int, hour: Int, minute: Int) = ZonedDateTime.of(2026, 11, day, hour, minute, 0, 0, zone).toEpochSecond()

    /** With the spaces the JDK's locale data may put before AM, as Android's does, made plain. */
    private fun plain(text: String) = text.replace('\u202F', ' ').replace('\u00A0', ' ')

    @Test fun `the day it was fetched`() {
        assertEquals("Nov 12, 2026", fetchedOn(at(12, 9, 30) * 1000, zone, Locale.US))
    }

    @Test fun `checked today says today, with the time`() {
        assertEquals("Today, 6:40 AM", plain(checkedAt(at(26, 6, 40), nowEpochSec = at(26, 23, 59), zone, Locale.US) { "Today, $it" }))
    }

    @Test fun `checked before today says the day and time`() {
        assertEquals("Nov 25, 2026, 11:50 PM", plain(checkedAt(at(25, 23, 50), nowEpochSec = at(26, 0, 10), zone, Locale.US) { "Today, $it" }))
    }
}
