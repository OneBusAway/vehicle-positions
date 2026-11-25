package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertFalse
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
private val WELCOME_TITLES = listOf(R.string.welcome_riders_title, R.string.welcome_pieces_title, R.string.welcome_habits_title)

/** Help in the app itself: from Settings and back, and the welcome opened from it, which comes back to Help. */
@RunWith(AndroidJUnit4::class)
class HelpFlowTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /** Has no vehicles to give, so the vehicle step stays on screen with its account button. */
    private val server = HoldingServer(held = "/nothing-is-held") { path ->
        when {
            path.endsWith("/api/v1/vehicles") -> "200 OK" to "[]"
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    private val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
    private val flags = DataStoreDriverFlagsStore(context.driverFlagsDataStore)
    private val maria get() = DriverKey.of(server.url, MARIA)

    @Before fun setUp() = runBlocking {
        forgetAll()
        val tokens = IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L)
        sessions.saveLogin(server.url, MARIA, tokens)
        flags.set(maria, DriverFlag.WelcomeSeen)
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

    private fun heading(id: Int) = compose.onAllNodes(hasText(string(id)) and isHeading()).fetchSemanticsNodes().isNotEmpty()

    private fun waitForHeading(id: Int) = compose.waitUntil(TIMEOUT_MS) { heading(id) }

    private fun waitForTheVehicleStep() = compose.waitUntil(TIMEOUT_MS) {
        compose.onAllNodes(hasContentDescription(string(R.string.settings_title))).fetchSemanticsNodes().isNotEmpty() &&
            !heading(R.string.settings_title)
    }

    /** Settings alone, with Help that went back to it gone. */
    private fun waitForSettings() = compose.waitUntil(TIMEOUT_MS) { heading(R.string.settings_title) && !heading(R.string.help_title) }

    private fun tap(id: Int) {
        val button = hasText(string(id)) and hasClickAction()
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(button).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(button).performClick()
    }

    private fun openHelp() {
        waitForTheVehicleStep()
        compose.onNode(hasContentDescription(string(R.string.settings_title))).performClick()
        waitForSettings()
        // A tap during the slide in does nothing.
        compose.waitForIdle()
        openHelpFromSettings()
    }

    private fun openHelpFromSettings() {
        val help = hasText(string(R.string.help_title)) and hasClickAction()
        compose.onNode(help).performScrollTo().performClick()
        waitForHeading(R.string.help_title)
        compose.waitForIdle()
    }

    /** Help alone, with the welcome that went back to it gone. */
    private fun waitForHelpAgain() {
        compose.waitUntil(TIMEOUT_MS) {
            heading(R.string.help_title) && WELCOME_TITLES.none(::heading)
        }
        compose.waitForIdle()
    }

    private fun openTheWelcomeAgain() {
        tap(R.string.help_welcome_again)
        waitForHeading(R.string.welcome_riders_title)
        compose.waitForIdle()
    }

    private fun pressBack() = InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)

    private fun hasSeenTheWelcome() = runBlocking { flags.isSet(maria, DriverFlag.WelcomeSeen) }

    private fun open(block: () -> Unit) = ActivityScenario.launch(MainActivity::class.java).use { block() }

    @Test
    fun helpOpensFromSettings_andTheBackArrowAndBackLeaveEachInTurn() {
        open {
            openHelp()
            compose.onNode(hasContentDescription(string(R.string.setup_navigate_up))).performClick()
            waitForSettings()
            compose.waitForIdle()

            openHelpFromSettings()
            pressBack()
            waitForSettings()
            compose.waitForIdle()
            compose.onNode(hasContentDescription(string(R.string.setup_navigate_up))).performClick()
            waitForTheVehicleStep()
        }
    }

    @Test
    fun theWelcomeFromHelp_comesBackToHelpBySkipBackOrContinue_andRecordsNothing() {
        open {
            openHelp()
            // A driver who has not seen it, so that a record would show.
            runBlocking { context.driverFlagsDataStore.edit { it.clear() } }

            openTheWelcomeAgain()
            tap(R.string.welcome_skip)
            waitForHelpAgain()

            openTheWelcomeAgain()
            pressBack()
            waitForHelpAgain()

            openTheWelcomeAgain()
            tap(R.string.welcome_next)
            waitForHeading(R.string.welcome_pieces_title)
            tap(R.string.welcome_next)
            waitForHeading(R.string.welcome_habits_title)
            tap(R.string.welcome_continue)
            waitForHelpAgain()

            assertFalse("opened from Help, the welcome records nothing", hasSeenTheWelcome())
        }
    }
}
