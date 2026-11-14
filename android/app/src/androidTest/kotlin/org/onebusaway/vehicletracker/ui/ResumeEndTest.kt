package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
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
import java.time.ZoneId

private const val TIMEOUT_MS = 15_000L

/**
 * Ending a stored trip from Resume, in the app itself with its own stores. The server can't be reached, so the trip is
 * ended on this phone, and where the app goes then depends on whether anyone is signed in.
 */
@RunWith(AndroidJUnit4::class)
class ResumeEndTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    // Nothing listens on port 1, so a request there is refused at once.
    private val server = "http://127.0.0.1:1/"
    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261114", startedAtEpochSec = 1_763_100_000L, serverUrl = server)

    @Before fun storeTheTrip() = runBlocking {
        forgetAll()
        DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault()).saveActiveTrip(trip)
    }

    @After fun tearDown() = runBlocking { forgetAll() }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
    }

    private fun waitFor(id: Int, clickable: Boolean = false) {
        val text = hasText(context.getString(id))
        val node = if (clickable) text and hasClickAction() and isEnabled() else text
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(node).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun tap(id: Int) {
        waitFor(id, clickable = true)
        // On a short screen, as CI's, the buttons can be below the fold.
        compose.onNode(hasText(context.getString(id)) and hasClickAction()).performScrollTo().performClick()
    }

    /** End trip, which the server doesn't confirm, then End on this phone. */
    private fun endFromResume() {
        tap(R.string.resume_end_trip_button)
        tap(R.string.tracking_end_trip_error_end_locally)
    }

    @Test
    fun withNobodySignedIn_theEndOpensSignIn() {
        // As on a phone restored from backup, which keeps the trip but not the session.
        ActivityScenario.launch(MainActivity::class.java).use {
            endFromResume()

            waitFor(R.string.login_button, clickable = true)
        }
    }

    @Test
    fun withADriverSignedIn_theEndOpensTheVehicleStep() {
        runBlocking {
            EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor()).saveLogin(
                server,
                "driver@example.com",
                IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L),
            )
        }
        ActivityScenario.launch(MainActivity::class.java).use {
            endFromResume()

            waitFor(R.string.vehicles_title)
        }
    }
}
