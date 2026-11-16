package org.onebusaway.vehicletracker.ui

import android.content.Intent
import android.view.KeyEvent
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isEnabled
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
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
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.DataStoreDriverFlagsStore
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.driverFlagsDataStore
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.service.LocationTrackingService
import java.time.ZoneId

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val MARIA = "maria@metro.example"
private const val OMAR = "omar@metro.example"

/**
 * When the welcome shows, in the app itself with its own stores: once for each driver, after sign-in or when the app
 * opens with them signed in, and never over a trip.
 */
@RunWith(AndroidJUnit4::class)
class WelcomeFlowTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /** Signs anyone in, and has no vehicles to give. Nothing is held: no test here waits on the server. */
    private val server = HoldingServer(held = "/nothing-is-held") { path ->
        when {
            path.endsWith("/api/v1/auth/login") -> "200 OK" to """{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""
            path.endsWith("/api/v1/vehicles") -> "200 OK" to "[]"
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    private val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
    private val flags = DataStoreDriverFlagsStore(context.driverFlagsDataStore)

    @Before fun setUp() = runBlocking { forgetAll() }

    @After fun tearDown() = runBlocking {
        server.close()
        // Resume starts the tracking service, which a run that fails part-way would leave running.
        context.stopService(Intent(context, LocationTrackingService::class.java))
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
        context.driverFlagsDataStore.edit { it.clear() }
    }

    private fun string(id: Int) = context.getString(id)

    private fun shows(id: Int) = compose.onAllNodes(hasText(string(id))).fetchSemanticsNodes().isNotEmpty()

    private fun waitFor(id: Int) = compose.waitUntil(TIMEOUT_MS) { shows(id) }

    /** The welcome's own screen, by its heading: a card on its second screen says "Choose your vehicle" too. */
    private fun onTheWelcome(title: Int) =
        compose.onAllNodes(hasText(string(title)) and isHeading()).fetchSemanticsNodes().isNotEmpty()

    private fun waitForTheWelcome(title: Int = R.string.welcome_riders_title) = compose.waitUntil(TIMEOUT_MS) { onTheWelcome(title) }

    /** The vehicle step itself, which has no Skip. */
    private fun waitForTheVehicleStep() =
        compose.waitUntil(TIMEOUT_MS) { shows(R.string.vehicles_title) && !shows(R.string.welcome_skip) }

    /** Taps a button of Sign in or Resume, which on a short screen, as CI's, can be below the fold. */
    private fun tap(id: Int) {
        val button = hasText(string(id)) and hasClickAction()
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(button and isEnabled()).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(button).performScrollTo().performClick()
    }

    /** Taps one of the welcome's own buttons, which stay where they are whatever scrolls. */
    private fun tapOnTheWelcome(id: Int) {
        val button = hasText(string(id)) and hasClickAction()
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(button).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(button).performClick()
    }

    private fun field(label: Int) =
        compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.EditableText) and hasText(string(label)))

    private fun signIn(email: String) {
        compose.waitUntil(TIMEOUT_MS) { shows(R.string.login_button) }
        // A phone that has signed in before shows the server it used, in place of the field.
        if (shows(R.string.login_server_url_label)) field(R.string.login_server_url_label).performTextReplacement(server.url)
        field(R.string.login_email_label).performTextReplacement(email)
        field(R.string.login_password_label).performTextReplacement("driverpass123")
        tap(R.string.login_button)
    }

    private fun signedIn(email: String, serverUrl: String = server.url) = runBlocking {
        val tokens = IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L)
        sessions.saveLogin(serverUrl, email, tokens)
    }

    private fun hasSeenTheWelcome(email: String) = runBlocking { flags.isSet(DriverKey.of(server.url, email), DriverFlag.WelcomeSeen) }

    private fun open(block: (ActivityScenario<MainActivity>) -> Unit) = ActivityScenario.launch(MainActivity::class.java).use(block)

    @Test
    fun aFirstSignInShowsTheWelcome_andSkipGoesOnToTheVehicleStep() {
        open {
            signIn(MARIA)

            waitForTheWelcome()
            assertFalse("only leaving the welcome records it", hasSeenTheWelcome(MARIA))
            tapOnTheWelcome(R.string.welcome_skip)

            waitForTheVehicleStep()
            assertTrue(hasSeenTheWelcome(MARIA))
        }
    }

    @Test
    fun theSecondSignInGoesStraightToTheVehicleStep_andAnotherDriverSeesTheWelcome() {
        runBlocking { flags.set(DriverKey.of(server.url, MARIA), DriverFlag.WelcomeSeen) }
        open {
            signIn(MARIA.uppercase())

            waitForTheVehicleStep()
            assertFalse(onTheWelcome(R.string.welcome_riders_title))
        }
        // Maria signs out and hands the phone on. What she has seen stays; Omar has seen nothing.
        runBlocking { sessions.clearTokenAndEmail() }
        open {
            signIn(OMAR)

            waitForTheWelcome()
            assertTrue(hasSeenTheWelcome(MARIA))
            assertFalse(hasSeenTheWelcome(OMAR))
        }
    }

    @Test
    fun openingTheAppSignedIn_showsTheWelcomeOnce_andContinueOnTheLastScreenLeavesIt() {
        signedIn(MARIA)
        open { scenario ->
            waitForTheWelcome()
            tapOnTheWelcome(R.string.welcome_next)
            waitForTheWelcome(R.string.welcome_pieces_title)
            tapOnTheWelcome(R.string.welcome_next)
            waitForTheWelcome(R.string.welcome_habits_title)

            // A rotation keeps the screen the driver was on.
            scenario.recreate()
            waitForTheWelcome(R.string.welcome_habits_title)
            assertFalse("closed in the middle, it would show again", hasSeenTheWelcome(MARIA))
            tapOnTheWelcome(R.string.welcome_continue)

            waitForTheVehicleStep()
            assertTrue(hasSeenTheWelcome(MARIA))
        }
        open {
            waitForTheVehicleStep()
            assertFalse("the next time the app opens it does not show", onTheWelcome(R.string.welcome_riders_title))
        }
    }

    @Test
    fun backOnTheFirstScreenLeavesTheWelcome_asSkipDoes() {
        signedIn(MARIA)
        open {
            waitForTheWelcome()

            InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)

            waitForTheVehicleStep()
            assertTrue(hasSeenTheWelcome(MARIA))
        }
    }

    @Test
    fun aStoredTripWinsOverTheWelcome_andSigningInAgainForItGoesBackToTheTrip() {
        // A trip is open on this server, and the phone is signed in to another one, so Resume asks Maria to sign in
        // to the trip's. She has not seen the welcome here.
        signedIn(MARIA, serverUrl = "https://other.example.org/")
        runBlocking {
            val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261114", startedAtEpochSec = System.currentTimeMillis() / 1000 - 600, serverUrl = server.url)
            DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault()).saveActiveTrip(trip)
        }
        open {
            waitFor(R.string.resume_title)
            assertFalse(onTheWelcome(R.string.welcome_riders_title))
            tap(R.string.resume_button)

            waitFor(R.string.login_again_title)
            field(R.string.login_password_label).performTextReplacement("driverpass123")
            tap(R.string.login_again_button)

            // Back to the trip she signed in for, and not by way of the welcome.
            compose.waitUntil(TIMEOUT_MS) { shows(R.string.resume_title) && !shows(R.string.login_again_title) }
            assertFalse(onTheWelcome(R.string.welcome_riders_title))
            assertFalse(hasSeenTheWelcome(MARIA))
        }
    }
}
