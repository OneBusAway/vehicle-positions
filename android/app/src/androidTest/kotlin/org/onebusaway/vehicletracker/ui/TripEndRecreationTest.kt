package org.onebusaway.vehicletracker.ui

import android.content.Intent
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isEnabled
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.service.LocationTrackingService
import java.time.ZoneId
import java.util.concurrent.TimeUnit

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000

/**
 * Ending a trip, in the app itself with its own stores, when the server confirms the end only after a rotation has
 * recreated the screen that asked. Either screen that ends a trip goes on to the vehicle step.
 */
@RunWith(AndroidJUnit4::class)
class TripEndRecreationTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val server = HoldingServer(held = "/api/v1/trips/end") { path ->
        when {
            path.endsWith("/api/v1/trips/end") -> "200 OK" to """{"status":"trip ended"}"""
            path.endsWith("/api/v1/vehicles") -> "200 OK" to "[]"
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    /** A driver signed in to [server] with a trip stored and no tracking service running: the app opens on Resume. */
    @Before fun signInWithATripOpen() = runBlocking {
        forgetAll()
        EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor()).saveLogin(
            server.url,
            "driver@example.com",
            IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L),
        )
        val startedAt = System.currentTimeMillis() / 1000 - 600
        val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261114", startedAtEpochSec = startedAt, serverUrl = server.url)
        DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault()).saveActiveTrip(trip)
    }

    @After fun tearDown() = runBlocking {
        server.close()
        // Resume starts the tracking service, which a run that fails before the end would leave running.
        context.stopService(Intent(context, LocationTrackingService::class.java))
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
    }

    private fun shows(id: Int) = compose.onAllNodes(hasText(context.getString(id))).fetchSemanticsNodes().isNotEmpty()

    private fun waitFor(id: Int) = compose.waitUntil(TIMEOUT_MS) { shows(id) }

    private fun tap(id: Int) {
        val button = hasText(context.getString(id)) and hasClickAction()
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(button and isEnabled()).fetchSemanticsNodes().isNotEmpty() }
        // On a short screen, as CI's, Resume's buttons can be below the fold.
        compose.onNode(button).performScrollTo().performClick()
    }

    /** Recreates the activity while the end is out, as a rotation would, and then lets the server confirm it. */
    private fun rotateBeforeTheServerConfirms(scenario: ActivityScenario<MainActivity>) {
        assertTrue("the end reached the server", server.heldArrived.await(HOLD_TIMEOUT_SEC, TimeUnit.SECONDS))
        scenario.recreate()
        server.release()
    }

    @Test
    fun anEndFromResumeConfirmedAfterARotation_opensTheVehicleStep() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            tap(R.string.resume_end_trip_button)

            rotateBeforeTheServerConfirms(scenario)

            waitFor(R.string.vehicles_title)
        }
    }

    @Test
    fun anEndFromTrackingConfirmedAfterARotation_opensTheVehicleStep() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            tap(R.string.resume_button)
            // Tracking: Resume has gone, and its own End trip is in the trip bar.
            compose.waitUntil(TIMEOUT_MS) { !shows(R.string.resume_title) && shows(R.string.tracking_end_trip_button) }
            compose.onNode(hasText(context.getString(R.string.tracking_end_trip_button)) and hasClickAction()).performClick()
            val confirm = hasText(context.getString(R.string.tracking_end_trip_dialog_confirm)) and hasClickAction()
            compose.onNode(confirm and hasAnyAncestor(isDialog())).performClick()

            rotateBeforeTheServerConfirms(scenario)

            waitFor(R.string.vehicles_title)
        }
    }
}
