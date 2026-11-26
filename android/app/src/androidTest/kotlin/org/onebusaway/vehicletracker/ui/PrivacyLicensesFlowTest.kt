package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasScrollToIndexAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performScrollToNode
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

/** Privacy, with the link the server gives, and Licenses and one license, from Settings and back. */
@RunWith(AndroidJUnit4::class)
class PrivacyLicensesFlowTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /** Has no vehicles, so the vehicle step stays on screen with its account button, and gives a privacy policy link. */
    private val server = HoldingServer(held = "/nothing-is-held") { path ->
        when {
            path.endsWith("/api/v1/vehicles") -> "200 OK" to "[]"
            path.endsWith("/api/v1/agency") -> "200 OK" to """{"privacy_policy_url":"https://metro.example/privacy"}"""
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    private val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
    private val flags = DataStoreDriverFlagsStore(context.driverFlagsDataStore)

    @Before fun setUp() = runBlocking {
        forgetAll()
        val tokens = IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L)
        sessions.saveLogin(server.url, MARIA, tokens)
        flags.set(DriverKey.of(server.url, MARIA), DriverFlag.WelcomeSeen)
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

    private fun shows(text: String) = compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty()

    private fun heading(text: String) = compose.onAllNodes(hasText(text) and isHeading()).fetchSemanticsNodes().isNotEmpty()

    private fun waitForHeadingAlone(text: String) {
        compose.waitUntil(TIMEOUT_MS) { heading(text) && listOf(R.string.settings_title, R.string.privacy_title, R.string.licenses_title).map(::string).filter { it != text }.none(::heading) }
        compose.waitForIdle()
    }

    private fun openSettings() {
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(hasContentDescription(string(R.string.settings_title))).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(hasContentDescription(string(R.string.settings_title))).performClick()
        waitForHeadingAlone(string(R.string.settings_title))
    }

    private fun tapRow(id: Int) {
        compose.onNode(hasText(string(id)) and hasClickAction()).performScrollTo().performClick()
    }

    private fun pressBack() = InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)

    @Test
    fun privacyHasTheServersLink_andLicensesOpenOneAndComeBack() {
        ActivityScenario.launch(MainActivity::class.java).use {
            openSettings()

            tapRow(R.string.privacy_title)
            waitForHeadingAlone(string(R.string.privacy_title))
            compose.waitUntil(TIMEOUT_MS) { shows(string(R.string.privacy_policy)) }
            pressBack()
            waitForHeadingAlone(string(R.string.settings_title))

            tapRow(R.string.licenses_title)
            waitForHeadingAlone(string(R.string.licenses_title))
            compose.onNode(hasScrollToIndexAction()).performScrollToNode(hasText("MapLibre Native"))
            compose.onNode(hasText("MapLibre Native") and hasClickAction()).performClick()
            compose.waitUntil(TIMEOUT_MS) { heading("MapLibre Native") && shows("MapLibre Native Android") }
            compose.onNode(hasContentDescription(string(R.string.setup_navigate_up))).performClick()
            waitForHeadingAlone(string(R.string.licenses_title))
            pressBack()
            waitForHeadingAlone(string(R.string.settings_title))
        }
    }
}
