package org.onebusaway.vehicletracker.ui

import android.os.Build
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher.Companion.expectValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionFixes
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.textOf
import org.onebusaway.vehicletracker.ui.settings.PermissionCheckContent
import org.onebusaway.vehicletracker.ui.settings.PhoneAccess
import org.onebusaway.vehicletracker.ui.settings.Readiness
import org.onebusaway.vehicletracker.ui.settings.readiness
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Permission check: what each row says, what fixes a missing one, and the largest text. */
@RunWith(AndroidJUnit4::class)
class PermissionCheckScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)

    private val everything = Grants(Build.VERSION_CODES.BAKLAVA, fine = true, coarse = true, background = true, notifications = true, batteryExempt = true)
    private val refused = everything.copy(fine = false, coarse = false, background = false)

    private fun ready(grants: Grants = everything, locationServicesOn: Boolean = true) = readiness(PhoneAccess(grants, locationServicesOn), waiting = null)

    private val fixed = mutableListOf<PermissionRow>()
    private var back = 0

    @Composable
    private fun Content(readiness: Readiness, onlyInSettings: Set<PermissionRow> = emptySet()) {
        AppTheme(darkTheme = false) {
            Surface { PermissionCheckContent(readiness, PermissionFixes(onlyInSettings) { fixed += it }, onBack = { back++ }) }
        }
    }

    private fun name(row: PermissionRow) = string(textOf(row).title)

    /** The end of a row as TalkBack reads it: the row's name, then its state, or what its button does. */
    private fun end(row: PermissionRow, state: Int) = hasContentDescription(string(R.string.preflight_row_state, name(row), string(state)))

    private fun words(row: PermissionRow, why: Int) = hasText(name(row)) and hasText(string(why))

    private val announcedPolitely = expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite)

    @Test
    fun withEverythingAllowed_everyRowSaysSoAndRidersSeeTheVehicle() {
        compose.setContent { Content(ready()) }

        compose.onNode(hasText(string(R.string.permission_check_title)) and isHeading()).assertIsDisplayed()
        for (row in PermissionRow.entries) {
            compose.onNode(words(row, textOf(row).why)).performScrollTo().assertIsDisplayed()
            compose.onNode(end(row, R.string.preflight_allowed) and announcedPolitely).assertExists()
        }
        compose.onNode(hasText(string(R.string.permission_check_riders)) and hasText(string(R.string.permission_check_riders_ready))).performScrollTo().assertIsDisplayed()
    }

    @Test
    fun aRowATripCanDoWithoutOffersToAllowIt_andRidersStillSeeTheVehicle() {
        compose.setContent { Content(ready(everything.copy(batteryExempt = false))) }

        compose.onNode(words(PermissionRow.BATTERY, R.string.preflight_skipped_why)).assertExists()
        compose.onNode(end(PermissionRow.BATTERY, R.string.permission_check_allow) and hasClickAction()).performScrollTo().performClick()

        assertEquals(listOf(PermissionRow.BATTERY), fixed)
        compose.onNode(hasText(string(R.string.permission_check_riders_ready))).assertExists()
    }

    @Test
    fun withLocationRefused_onlyLocationCanBeFixed_andWhatComesWithItWaits() {
        compose.setContent { Content(ready(refused)) }

        compose.onNode(words(PermissionRow.LOCATION, R.string.preflight_location_needed)).assertExists()
        compose.onNode(end(PermissionRow.LOCATION, R.string.permission_check_allow) and hasClickAction()).performScrollTo().performClick()
        assertEquals(listOf(PermissionRow.LOCATION), fixed)
        for (row in listOf(PermissionRow.PRECISE, PermissionRow.BACKGROUND)) {
            compose.onNode(words(row, textOf(row).why)).assertExists()
            compose.onNode(end(row, R.string.permission_check_allow)).assertDoesNotExist()
            compose.onNode(end(row, R.string.preflight_allowed)).assertDoesNotExist()
        }
        compose.onNode(hasText(string(R.string.permission_check_riders_not_ready))).assertExists()
    }

    @Test
    fun withOnlyApproximateLocation_preciseLocationSaysSoAndCanBeAsked() {
        compose.setContent { Content(ready(everything.copy(fine = false))) }

        compose.onNode(words(PermissionRow.PRECISE, R.string.preflight_approximate)).assertExists()
        compose.onNode(end(PermissionRow.PRECISE, R.string.permission_check_allow) and hasClickAction()).performScrollTo().performClick()
        assertEquals(listOf(PermissionRow.PRECISE), fixed)
    }

    @Test
    fun withLocationOffForThePhone_itsRowTurnsItOn() {
        compose.setContent { Content(ready(locationServicesOn = false)) }

        compose.onNode(end(PermissionRow.LOCATION_SERVICES, R.string.permission_check_turn_on) and hasClickAction()).performScrollTo().performClick()
        assertEquals(listOf(PermissionRow.LOCATION_SERVICES), fixed)
        compose.onNode(hasText(string(R.string.permission_check_riders_not_ready))).assertExists()
    }

    @Test
    fun aRowAndroidWillNotAskAboutAgainOpensTheSettings() {
        compose.setContent { Content(ready(everything.copy(notifications = false)), onlyInSettings = setOf(PermissionRow.NOTIFICATIONS)) }

        compose.onNode(end(PermissionRow.NOTIFICATIONS, R.string.permission_check_allow)).assertDoesNotExist()
        compose.onNode(end(PermissionRow.NOTIFICATIONS, R.string.preflight_open_settings) and hasClickAction()).performScrollTo().performClick()
        assertEquals(listOf(PermissionRow.NOTIFICATIONS), fixed)
    }

    @Test
    fun aRowAllowedWhileOnScreenSaysSoPolitely() {
        var state by mutableStateOf(ready(everything.copy(batteryExempt = false)))
        compose.setContent { Content(state) }

        state = ready()
        compose.waitForIdle()

        compose.onNode(end(PermissionRow.BATTERY, R.string.preflight_allowed) and announcedPolitely).assertExists()
        compose.onNode(end(PermissionRow.BATTERY, R.string.permission_check_allow)).assertDoesNotExist()
    }

    @Test
    fun backLeavesIt() {
        compose.setContent { Content(ready()) }

        compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).performClick()

        assertEquals(1, back)
    }

    @Test
    fun withRoom_theButtonIsBesideTheWords_andWithoutItIsUnderThem() {
        var width by mutableStateOf(412.dp)
        compose.setContent {
            Box(Modifier.requiredWidth(width).requiredHeight(3000.dp)) { Content(ready(everything.copy(batteryExempt = false))) }
        }
        fun words() = compose.onNode(words(PermissionRow.BATTERY, R.string.preflight_skipped_why)).fetchSemanticsNode()
        fun button() = compose.onNode(end(PermissionRow.BATTERY, R.string.permission_check_allow)).fetchSemanticsNode()

        // Where they are placed, on the test's screen or off it.
        assertTrue("beside", button().positionInRoot.x >= words().positionInRoot.x + words().size.width)
        width = 320.dp
        compose.waitForIdle()
        assertTrue("under", button().positionInRoot.y >= words().positionInRoot.y + words().size.height)
    }

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                Box(Modifier.requiredWidth(320.dp).requiredHeight(3000.dp)) { Content(ready(refused)) }
            }
        }

        compose.assertNothingIsCutOff()
    }
}
