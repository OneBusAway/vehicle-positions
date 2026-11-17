package org.onebusaway.vehicletracker.ui

import androidx.activity.BackEventCompat
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.DataStoreDriverFlagsStore
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.driverFlagsDataStore
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val MARIA = "maria@metro.example"

/**
 * Help opened from the vehicle step before the driver's one assignment has landed. The step skips itself for a driver
 * with one vehicle, and that skip must not close Help: it waits until the driver is back on the step.
 */
@RunWith(AndroidJUnit4::class)
class HelpOverOneVehicleTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /** Holds the assignments until Help is open. The driver has one vehicle, and no routes to choose from. */
    private val server = HoldingServer(held = "/api/v1/vehicles") { path ->
        when {
            path.endsWith("/api/v1/vehicles") -> "200 OK" to """[{"id":"4317","label":"4317"}]"""
            path.endsWith("/api/v1/gtfs/routes") -> "200 OK" to """{"routes":[]}"""
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    @Before fun setUp() = runBlocking {
        forgetAll()
        val tokens = IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L)
        EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor()).saveLogin(server.url, MARIA, tokens)
        val maria = DriverKey.of(server.url, MARIA)
        DataStoreDriverFlagsStore(context.driverFlagsDataStore).set(maria, DriverFlag.WelcomeSeen)
    }

    @After fun tearDown() = runBlocking {
        server.close()
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
        context.driverFlagsDataStore.edit { it.clear() }
    }

    private fun string(id: Int) = context.getString(id)

    private fun shows(id: Int, heading: Boolean = false): Boolean {
        val matcher = if (heading) hasText(string(id)) and isHeading() else hasText(string(id))
        return compose.onAllNodes(matcher).fetchSemanticsNodes().isNotEmpty()
    }

    private val onTheRouteStep get() = shows(R.string.routes_title)
    private val theOneVehicleIsIn get() = compose.onAllNodes(hasText("4317")).fetchSemanticsNodes().isNotEmpty()

    /** Opens Help from the vehicle step while it is still asking for the assignments, then lets them land. */
    private fun openHelpBeforeTheAssignmentsLand() {
        // Waited for through the rule, which only draws the app while the test waits on it.
        compose.waitUntil(TIMEOUT_MS) { server.heldArrived.count == 0L }
        val account = hasContentDescription(string(R.string.account_menu))
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(account).fetchSemanticsNodes().isNotEmpty() }
        compose.waitForIdle()
        compose.onNode(account).performClick()
        compose.onNode(hasText(string(R.string.help_title)) and hasClickAction()).performClick()
        compose.waitUntil(TIMEOUT_MS) { shows(R.string.help_title, heading = true) }
        compose.waitForIdle()
        server.release()
    }

    /**
     * Starts a back gesture over Help, which brings the vehicle step back underneath it, and waits until the step has
     * its one vehicle, or has skipped past it.
     */
    private fun startTheBackGesture(scenario: ActivityScenario<MainActivity>) {
        scenario.onActivity {
            val back = it.onBackPressedDispatcher
            back.dispatchOnBackStarted(BackEventCompat(10f, 900f, 0f, BackEventCompat.EDGE_LEFT))
            back.dispatchOnBackProgressed(BackEventCompat(200f, 900f, 0.5f, BackEventCompat.EDGE_LEFT))
        }
        compose.waitUntil(TIMEOUT_MS) { theOneVehicleIsIn || onTheRouteStep }
        compose.waitForIdle()
    }

    @Test
    fun theOneVehicleLandingWhileHelpIsOpen_doesNotCloseHelp() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            openHelpBeforeTheAssignmentsLand()
            startTheBackGesture(scenario)

            assertFalse("the skip went ahead under Help", onTheRouteStep)
            scenario.onActivity { it.onBackPressedDispatcher.dispatchOnBackCancelled() }
            compose.waitForIdle()

            assertTrue("Help is still open", shows(R.string.help_title, heading = true))
            assertFalse(onTheRouteStep)
        }
    }

    @Test
    fun backOnTheVehicleStep_theSkipThatWaitedGoesAhead() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            openHelpBeforeTheAssignmentsLand()
            startTheBackGesture(scenario)

            scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

            compose.waitUntil(TIMEOUT_MS) { onTheRouteStep }
            assertFalse("Help closed", shows(R.string.help_title, heading = true))
        }
    }
}
