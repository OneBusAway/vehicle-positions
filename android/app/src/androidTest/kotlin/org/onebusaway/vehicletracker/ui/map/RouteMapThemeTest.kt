package org.onebusaway.vehicletracker.ui.map

import android.content.res.Configuration
import android.view.View
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.maplibre.android.maps.MapView
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.time.ZoneOffset

/** The Tracking map through a change of theme: it draws its colours once, so another takes its place. */
@RunWith(AndroidJUnit4::class)
class RouteMapThemeTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val trip = TripGeometry(
        tripId = "T1",
        serviceDate = "20260701",
        timezone = ZoneOffset.UTC,
        shapePoints = listOf(GeoPoint(47.60, -122.33), GeoPoint(47.61, -122.33)),
        stops = emptyList(),
        thresholds = AdherenceThresholds(maxShapeDistanceM = 200.0, scheduleEarlyS = 1200, scheduleLateS = 5400),
    )

    private var dark by mutableStateOf(false)
    private var ring by mutableIntStateOf(0)

    @Before fun setUp() {
        assumeTrue("the map needs OpenGL ES 3", supportsMap(compose.activity.glEsVersion()))
        compose.setContent {
            val night = if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            val configuration = Configuration(LocalConfiguration.current).apply { uiMode = uiMode and Configuration.UI_MODE_NIGHT_MASK.inv() or night }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                AppTheme {
                    RouteMap(trip, null, MapFileState.None, ring, MapUp.HEADING, PaddingValues(0.dp), PaddingValues(0.dp), Modifier.fillMaxSize())
                }
            }
        }
    }

    private fun mapsIn(view: View): List<MapView> = when (view) {
        is MapView -> listOf(view)
        is ViewGroup -> (0 until view.childCount).flatMap { mapsIn(view.getChildAt(it)) }
        else -> emptyList()
    }

    private fun theMap(): MapView = compose.runOnIdle { mapsIn(compose.activity.window.decorView).single() }

    @Test
    fun aChangeOfThemeIsAnotherMap_andTheOneBeforeIsDestroyed() {
        val day = theMap()

        dark = true
        val night = theMap()

        assertNotSame(day, night)
        assertTrue(day.isDestroyed)
        assertFalse(night.isDestroyed)
    }

    @Test
    fun theMapStaysThroughAnythingElse() {
        val map = theMap()

        ring = 0xFF006600.toInt()

        assertSame(map, theMap())
        assertEquals(false, map.isDestroyed)
    }
}
