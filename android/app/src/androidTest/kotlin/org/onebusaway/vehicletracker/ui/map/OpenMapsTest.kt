package org.onebusaway.vehicletracker.ui.map

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.OpenMaps
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.time.Instant
import java.time.ZoneOffset

/** A map on screen counts as open, so its file is not deleted under it, until it is gone. */
@RunWith(AndroidJUnit4::class)
class OpenMapsTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val start = Instant.parse("2026-07-01T15:00:00Z")
    private val trip = TripGeometry(
        tripId = "T1",
        serviceDate = "20260701",
        timezone = ZoneOffset.UTC,
        shapePoints = listOf(GeoPoint(47.60, -122.34), GeoPoint(47.60, -122.32)),
        stops = listOf(
            TripStop("ST1", "ST1", 47.60, -122.34, 0.0, start, start),
            TripStop("ST2", "ST2", 47.60, -122.32, 1500.0, start.plusSeconds(300), start.plusSeconds(300)),
        ),
        thresholds = AdherenceThresholds(maxShapeDistanceM = 200.0, scheduleEarlyS = 1200, scheduleLateS = 5400),
    )

    @Test
    fun aMapIsOpenFromWhenItIsMadeUntilItGoes() {
        assumeTrue("the map needs OpenGL ES 3", supportsMap(compose.activity.glEsVersion()))
        assertTrue("no map before", OpenMaps.whileNoneOpen {})
        var shown by mutableStateOf(true)
        compose.setContent {
            AppTheme {
                if (shown) RouteMap(trip, null, MapFileState.None, 0, MapUp.HEADING, PaddingValues(0.dp), PaddingValues(0.dp), Modifier.fillMaxSize())
            }
        }

        compose.waitForIdle()
        assertFalse("open while it shows", OpenMaps.whileNoneOpen {})

        shown = false
        compose.waitForIdle()
        assertTrue("and no longer once it has gone", OpenMaps.whileNoneOpen {})
    }
}
