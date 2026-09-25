package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.ui.login.LoginScreenContent
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime

/**
 * Instrumented UI tests that render the stateless screen `Content` composables directly with
 * fake state and no-op callbacks — no Hilt/ViewModel wiring needed. Run with
 * `./gradlew :app:connectedDebugAndroidTest`.
 */
@RunWith(AndroidJUnit4::class)
class ScreenFlowTest {
    @get:Rule val compose = createComposeRule()

    private fun getString(resId: Int): String =
        InstrumentationRegistry.getInstrumentation().targetContext.getString(resId)

    @Test
    fun loginScreen_disablesButtonWhileLoading() {
        compose.setContent {
            LoginScreenContent(
                state = LoginUiState(loading = true),
                onServerUrlChange = {},
                onEmailChange = {},
                onPasswordChange = {},
                onLoginClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.login_button)).assertIsNotEnabled()
    }

    @Test
    fun trackingScreen_showsConnectedStatus() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(
                    tracking = TrackingState(active = true, problem = TrackingProblem.NONE),
                ),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.tracking_status_connected)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_endTripShowsConfirmation() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(
                    tracking = TrackingState(active = true, problem = TrackingProblem.NONE),
                ),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()

        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_title)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_message)).assertIsDisplayed()
    }

    // --- The adherence panel, against the server fixture's trip T1 ---

    private val resources get() = InstrumentationRegistry.getInstrumentation().targetContext.resources
    private val pacific = ZoneId.of("America/Los_Angeles")

    private fun at(hour: Int, minute: Int): Instant = ZonedDateTime.of(2026, 9, 2, hour, minute, 0, 0, pacific).toInstant()

    /** A straight 1 km run north with stops at 0, 500 and 1001 m, scheduled 08:00, 08:05, 08:10. */
    private val t1 = TripGeometry(
        tripId = "T1",
        serviceDate = "20260902",
        timezone = pacific,
        shapePoints = listOf(GeoPoint(47.6000, -122.3300), GeoPoint(47.6045, -122.3300), GeoPoint(47.6090, -122.3300)),
        stops = listOf(
            TripStop("ST1", "Stop ST1", 47.6000, -122.3300, 0.0, at(8, 0), at(8, 0)),
            TripStop("ST2", "Stop ST2", 47.6045, -122.3300, 500.0, at(8, 5), at(8, 5)),
            TripStop("ST3", "Stop ST3", 47.6090, -122.3300, 1001.0, at(8, 10), at(8, 10)),
        ),
        thresholds = AdherenceThresholds(maxShapeDistanceM = 60.0, scheduleEarlyS = 900, scheduleLateS = 5400),
    )

    private fun judged(lat: Double, lon: Double, time: Instant) = requireNotNull(AdherenceEvaluator.of(t1)).evaluate(
        LocationFix(lat, lon, bearing = 0.0, speed = 8.0, accuracy = 5.0, timeEpochSec = time.epochSecond),
        previous = null,
    )

    private fun showTracking(tracking: TrackingState) {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = tracking),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }
    }

    @Test
    fun trackingScreen_showsAdherenceAndTheNextStop() {
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5))))

        compose.onNodeWithText(getString(R.string.tracking_adherence_on_time)).assertIsDisplayed()
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_next_stop, "Stop ST3")).assertIsDisplayed()
        // ~501 m to ST3, due at 08:10 on the agency's clock whatever the device's zone.
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_distance_m, 501), substring = true).assertIsDisplayed()
        compose.onNodeWithText("8:10", substring = true).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsWholeMinutesLate() {
        // 400 s behind ST2's schedule rounds to 7 minutes.
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5).plusSeconds(400))))

        compose.onNodeWithText(resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, 7, 7)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsOffScheduleWithTheDeviation() {
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6000, -122.3300, at(8, 0).plusSeconds(6000))))

        val deviation = resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, 100, 100)
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_off_schedule, deviation)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsOffRouteInKilometresPastOneKilometre() {
        // 0.017° of longitude east of the line is ~1.28 km.
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3130, at(8, 5))))

        val distance = resources.getString(R.string.tracking_adherence_distance_km, 1.3)
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_off_route, distance)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_waitsForTheFirstFix() {
        showTracking(TrackingState(active = true, geometry = t1))

        compose.onNodeWithText(getString(R.string.tracking_adherence_waiting_for_gps)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_saysWhenThereIsNoScheduleToJudgeAgainst() {
        showTracking(TrackingState(active = true, geometry = null))

        compose.onNodeWithText(getString(R.string.tracking_adherence_no_schedule)).assertIsDisplayed()
    }
}
