package org.onebusaway.vehicletracker.ui

import android.content.Context
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.os.Build
import android.os.ParcelFileDescriptor
import android.view.ContextThemeWrapper
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.core.content.edit
import androidx.core.view.WindowCompat
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assume.assumeTrue
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
import org.onebusaway.vehicletracker.data.PreferencesThemeStore
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.data.driverFlagsDataStore
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.themePreferences
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.ui.theme.nightConfiguration
import org.onebusaway.vehicletracker.ui.theme.themeController

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val MARIA = "maria@metro.example"

/** The theme chosen in Settings, over the phone's own. */
@RunWith(AndroidJUnit4::class)
class ThemeTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val phoneWasDark = "yes" in shell("cmd uimode night")
    private val server = HoldingServer(held = "/nothing") { path ->
        if (path.endsWith("/api/v1/vehicles")) "200 OK" to "[]" else "404 Not Found" to """{"error":"not found"}"""
    }

    @Before fun setUp() = runBlocking {
        assumeTrue("Android gives the app its theme from Android 12", Build.VERSION.SDK_INT >= Build.VERSION_CODES.S)
        forgetAll()
        val tokens = IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L)
        EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor()).saveLogin(server.url, MARIA, tokens)
        DataStoreDriverFlagsStore(context.driverFlagsDataStore).set(DriverKey.of(server.url, MARIA), DriverFlag.WelcomeSeen)
    }

    @After fun tearDown() = runBlocking {
        server.close()
        forgetAll()
        setPhoneDark(phoneWasDark)
    }

    private suspend fun forgetAll() {
        themeController(context).choose(ThemeChoice.SYSTEM)
        context.themePreferences.edit { clear() }
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
        context.driverFlagsDataStore.edit { it.clear() }
    }

    private fun shell(command: String): String =
        ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(command)).use { it.reader().readText() }

    private fun setPhoneDark(dark: Boolean) {
        shell("cmd uimode night ${if (dark) "yes" else "no"}")
    }

    private fun string(id: Int) = context.getString(id)

    private val Context.isDark get() = resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES

    private fun ActivityScenario<MainActivity>.isDark(): Boolean {
        var dark = false
        onActivity { dark = it.isDark }
        return dark
    }

    private fun ActivityScenario<MainActivity>.awaitDark(dark: Boolean, why: String) {
        try {
            compose.waitUntil(TIMEOUT_MS) { isDark() == dark }
        } catch (e: Throwable) {
            throw AssertionError("$why: dark should be $dark", e)
        }
    }

    @Test
    fun eachChoiceOverEachPhoneSetting_isWhatTheAppAndTheBarsAreShownIn() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            for (phoneDark in listOf(false, true)) {
                setPhoneDark(phoneDark)
                for (choice in listOf(ThemeChoice.DARK, ThemeChoice.SYSTEM, ThemeChoice.LIGHT)) {
                    themeController(context).choose(choice)
                    val dark = when (choice) {
                        ThemeChoice.SYSTEM -> phoneDark
                        ThemeChoice.LIGHT -> false
                        ThemeChoice.DARK -> true
                    }
                    val why = "$choice on a ${if (phoneDark) "dark" else "light"} phone"
                    scenario.awaitDark(dark, why)
                    compose.waitForIdle()
                    scenario.onActivity { activity ->
                        val bars = WindowCompat.getInsetsController(activity.window, activity.window.decorView)
                        assertEquals("$why: dark status icons", !dark, bars.isAppearanceLightStatusBars)
                        assertEquals("$why: its resources", if (dark) 0xFF131314.toInt() else 0xFFFFFFFF.toInt(), activity.getColor(R.color.window_background))
                    }
                }
            }
        }
    }

    @Test
    fun aThemeChosenInSettings_isShownAtOnce_withSettingsStillOpenAndItsListClosed() {
        setPhoneDark(false)
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val account = hasContentDescription(string(R.string.settings_title))
            compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(account).fetchSemanticsNodes().isNotEmpty() }
            compose.waitForIdle()
            compose.onNode(account).performClick()
            val settings = hasText(string(R.string.settings_title)) and isHeading()
            compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(settings).fetchSemanticsNodes().isNotEmpty() }
            compose.waitForIdle()

            var before: MainActivity? = null
            scenario.onActivity { before = it }
            compose.onNode(hasText(string(R.string.settings_theme)) and hasClickAction()).performScrollTo().performClick()
            compose.onNode(hasText(string(R.string.theme_dark)) and hasAnyAncestor(isDialog())).performClick()

            scenario.awaitDark(true, "Dark, chosen in Settings")
            compose.waitForIdle()
            scenario.onActivity { assertSame("changed in place, so nothing goes black", before, it) }
            compose.onNode(settings).assertExists()
            compose.onNode(isDialog()).assertDoesNotExist()
            compose.onNode(hasText(string(R.string.settings_theme)) and hasText(string(R.string.theme_dark)) and hasClickAction()).assertExists()
        }
        assertEquals(ThemeChoice.DARK, PreferencesThemeStore(context.themePreferences).choice)
    }

    @Test
    fun theStoredThemeIsReadBack_andIsThePhonesOwnUntilOneIsChosen() {
        val store = PreferencesThemeStore(context.themePreferences)
        assertEquals(ThemeChoice.SYSTEM, store.choice)
        for (choice in ThemeChoice.entries) {
            store.choice = choice
            assertEquals(choice, PreferencesThemeStore(context.themePreferences).choice)
        }
        // As a later version might store, and this one be put back over.
        context.themePreferences.edit { putString("theme", "sepia") }
        assertEquals(ThemeChoice.SYSTEM, store.choice)
    }

    /** How an activity shows the theme below Android 12. An activity is a ContextThemeWrapper. */
    @Test
    fun anActivitysNightConfiguration_changesItsNightAndNothingElse() {
        for ((night, background) in listOf(Configuration.UI_MODE_NIGHT_NO to 0xFFFFFFFF, Configuration.UI_MODE_NIGHT_YES to 0xFF131314)) {
            val activity = ContextThemeWrapper(context, R.style.Theme_VehicleTracker).apply { applyOverrideConfiguration(nightConfiguration(night)) }
            val (has, phone) = activity.resources.configuration to context.resources.configuration
            assertEquals(night, has.uiMode and Configuration.UI_MODE_NIGHT_MASK)
            assertEquals(background.toInt(), activity.getColor(R.color.window_background))
            assertEquals(phone.uiMode and Configuration.UI_MODE_TYPE_MASK, has.uiMode and Configuration.UI_MODE_TYPE_MASK)
            assertEquals(phone.locales, has.locales)
            assertEquals(phone.fontScale, has.fontScale)
            assertEquals(phone.densityDpi, has.densityDpi)
            assertEquals("only its night differs", 0, phone.diff(has) and ActivityInfo.CONFIG_UI_MODE.inv())
        }
    }
}
