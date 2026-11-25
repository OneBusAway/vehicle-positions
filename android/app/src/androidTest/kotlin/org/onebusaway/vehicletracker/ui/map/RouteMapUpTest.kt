package org.onebusaway.vehicletracker.ui.map

import android.view.View
import android.view.ViewGroup
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
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapView
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.time.Instant
import java.time.ZoneOffset
import kotlin.math.abs

private const val TIMEOUT_MS = 30_000L

/** What is up on the Tracking map as it follows a vehicle that goes east: east, or north when the driver chose that. */
@RunWith(AndroidJUnit4::class)
class RouteMapUpTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val start = Instant.parse("2026-07-01T15:00:00Z")
    private fun stop(id: String, lon: Double, along: Double, minutes: Long) =
        TripStop(id, id, 47.60, lon, along, start.plusSeconds(60 * minutes), start.plusSeconds(60 * minutes))

    /** A straight run east. */
    private val trip = TripGeometry(
        tripId = "T1",
        serviceDate = "20260701",
        timezone = ZoneOffset.UTC,
        shapePoints = listOf(GeoPoint(47.60, -122.34), GeoPoint(47.60, -122.33), GeoPoint(47.60, -122.32)),
        stops = listOf(stop("ST1", -122.34, 0.0, 0), stop("ST2", -122.32, 1500.0, 5)),
        thresholds = AdherenceThresholds(maxShapeDistanceM = 200.0, scheduleEarlyS = 1200, scheduleLateS = 5400),
    )

    private val goingEast = requireNotNull(AdherenceEvaluator.of(trip)).evaluate(
        LocationFix(47.60, -122.335, bearing = 90.0, speed = 10.0, accuracy = 5.0, timeEpochSec = start.epochSecond + 60),
        previous = null,
    )

    private var up by mutableStateOf(MapUp.HEADING)

    private fun mapsIn(view: View): List<MapView> = when (view) {
        is MapView -> listOf(view)
        is ViewGroup -> (0 until view.childCount).flatMap { mapsIn(view.getChildAt(it)) }
        else -> emptyList()
    }

    private fun show(first: MapUp): MapLibreMap {
        assumeTrue("the map needs OpenGL ES 3", supportsMap(compose.activity.glEsVersion()))
        up = first
        compose.setContent {
            AppTheme {
                RouteMap(trip, goingEast, MapFileState.None, 0, up, PaddingValues(0.dp), PaddingValues(0.dp), Modifier.fillMaxSize())
            }
        }
        var map: MapLibreMap? = null
        compose.runOnIdle { mapsIn(compose.activity.window.decorView).single().getMapAsync { map = it } }
        compose.waitUntil(TIMEOUT_MS) { map != null }
        return requireNotNull(map)
    }

    private fun MapLibreMap.awaitUp(bearing: Double) {
        var last = Double.NaN
        try {
            compose.waitUntil(TIMEOUT_MS) {
                compose.runOnUiThread { last = cameraPosition.bearing }
                locationComponentFollows && abs(last - bearing) < 2
            }
        } catch (e: Throwable) {
            throw AssertionError("$bearing should be up, but $last is", e)
        }
    }

    private val MapLibreMap.locationComponentFollows: Boolean
        get() = compose.runOnUiThread<Boolean> { locationComponent.isLocationComponentActivated }

    @Test
    fun theWayItGoesIsUp_untilNorthIsChosen() {
        val map = show(MapUp.HEADING)
        map.awaitUp(90.0)

        up = MapUp.NORTH
        map.awaitUp(0.0)
    }

    @Test
    fun withNorthChosen_northIsUpFromTheFirstFix() {
        val map = show(MapUp.NORTH)
        map.awaitUp(0.0)

        compose.waitForIdle()
        assertTrue(abs(compose.runOnUiThread<Double> { map.cameraPosition.bearing }) < 2)
    }
}
