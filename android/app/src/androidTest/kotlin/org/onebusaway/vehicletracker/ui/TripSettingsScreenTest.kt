package org.onebusaway.vehicletracker.ui

import android.view.View
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
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.isSelectable
import androidx.compose.ui.test.isToggleable
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.MapUp
import org.onebusaway.vehicletracker.data.ScreenOn
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TripSettings
import org.onebusaway.vehicletracker.ui.settings.TripSettingsContent
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.KeepScreenOn
import org.onebusaway.vehicletracker.ui.tracking.VibrateOnProblem

/** During a trip: its choices, and what Tracking does by them. */
@RunWith(AndroidJUnit4::class)
class TripSettingsScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int) = context.getString(id)

    private var settings by mutableStateOf(TripSettings())
    private var back = 0

    @Composable
    private fun Content() {
        AppTheme(darkTheme = false) {
            Surface {
                TripSettingsContent(
                    settings = settings,
                    onBack = { back++ },
                    onScreenOn = { settings = settings.copy(screenOn = it) },
                    onMapUp = { settings = settings.copy(mapUp = it) },
                    onVibrates = { settings = settings.copy(vibrates = it) },
                )
            }
        }
    }

    private fun choice(name: Int) = compose.onNode(hasText(string(name)) and isSelectable())

    private val vibrate get() = compose.onNode(hasText(string(R.string.trip_vibrate)) and isToggleable())

    @Test
    fun theDefaultsAreWhatTrackingDoesNow() {
        compose.setContent { Content() }

        compose.onNode(hasText(string(R.string.trip_settings_title)) and isHeading()).assertIsDisplayed()
        choice(R.string.trip_screen_charging).assertIsSelected()
        choice(R.string.trip_screen_always).assertIsNotSelected()
        choice(R.string.trip_map_heading).assertIsSelected()
        choice(R.string.trip_map_north).assertIsNotSelected()
        vibrate.assertIsOn()
    }

    @Test
    fun aTapChoosesOne() {
        compose.setContent { Content() }

        choice(R.string.trip_screen_always).performScrollTo().performClick()
        choice(R.string.trip_map_north).performScrollTo().performClick()
        vibrate.performScrollTo().performClick()

        assertEquals(TripSettings(ScreenOn.ALWAYS, MapUp.NORTH, vibrates = false), settings)
        choice(R.string.trip_screen_always).assertIsSelected()
        choice(R.string.trip_screen_charging).assertIsNotSelected()
        choice(R.string.trip_map_north).assertIsSelected()
        vibrate.assertIsOff()
    }

    @Test
    fun backLeavesIt() {
        compose.setContent { Content() }

        compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).performClick()

        assertEquals(1, back)
    }

    @Test
    fun theMapsAreSideBySideWithRoom_andUnderEachOtherAtLargeText() {
        var fontScale by mutableStateOf(1f)
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale)) {
                Box(Modifier.requiredWidth(412.dp).requiredHeight(3000.dp)) { Content() }
            }
        }
        fun heading() = choice(R.string.trip_map_heading).fetchSemanticsNode()
        fun north() = choice(R.string.trip_map_north).fetchSemanticsNode()

        if (context.resources.configuration.screenWidthDp >= 350) {
            assertEquals("beside", heading().positionInRoot.y, north().positionInRoot.y, 1f)
            assertEquals("as tall", heading().size.height, north().size.height)
        }
        fontScale = 2f
        compose.waitForIdle()
        assertTrue("under", north().positionInRoot.y >= heading().positionInRoot.y + heading().size.height)
    }

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                Box(Modifier.requiredWidth(320.dp).requiredHeight(3000.dp)) { Content() }
            }
        }

        compose.assertNothingIsCutOff()
    }

    // --- The display on Tracking ---

    @Test
    fun alwaysOn_theDisplayIsHeldWhileTrackingShows_andLetGoAsItGoes() {
        var tracking by mutableStateOf(true)
        lateinit var view: View
        compose.setContent {
            view = LocalView.current
            if (tracking) KeepScreenOn(ScreenOn.ALWAYS)
        }

        compose.runOnIdle { assertTrue(view.keepScreenOn) }
        tracking = false
        compose.runOnIdle { assertFalse(view.keepScreenOn) }
    }

    // --- The vibration on Tracking ---

    private var problem by mutableStateOf(TrackingProblem.NONE)
    private var on by mutableStateOf(true)
    private var vibrations = 0

    private fun becomes(next: TrackingProblem): Int {
        problem = next
        compose.waitForIdle()
        return vibrations
    }

    @Test
    fun eachProblemThatComesUpVibratesOnce() {
        compose.setContent { VibrateOnProblem(problem, on) { vibrations++ } }

        assertEquals(0, becomes(TrackingProblem.NONE))
        assertEquals(1, becomes(TrackingProblem.NO_NETWORK))
        assertEquals(2, becomes(TrackingProblem.AUTH_EXPIRED))
        assertEquals("clears", 2, becomes(TrackingProblem.NONE))
        assertEquals("again", 3, becomes(TrackingProblem.NO_NETWORK))
    }

    @Test
    fun aProblemThereAlreadyDoesNotVibrate() {
        problem = TrackingProblem.NO_GPS
        compose.setContent { VibrateOnProblem(problem, on) { vibrations++ } }

        compose.waitForIdle()

        assertEquals(0, vibrations)
    }

    @Test
    fun switchedOff_nothingVibrates_norForAProblemThereAsItIsSwitchedOn() {
        on = false
        compose.setContent { VibrateOnProblem(problem, on) { vibrations++ } }

        assertEquals(0, becomes(TrackingProblem.NO_NETWORK))
        on = true
        compose.waitForIdle()
        assertEquals(0, vibrations)
    }
}
