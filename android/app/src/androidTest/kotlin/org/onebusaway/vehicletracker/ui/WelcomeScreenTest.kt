package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Recomposer
import androidx.compose.ui.Modifier
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.DeviceConfigurationOverride
import androidx.compose.ui.test.FontScale
import androidx.compose.ui.test.ForcedSize
import androidx.compose.ui.test.SemanticsMatcher.Companion.expectValue
import androidx.compose.ui.test.SemanticsMatcher.Companion.keyIsDefined
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsAtLeast
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assertIsNotFocused
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.then
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.DpSize
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
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.LightStatusColors
import org.onebusaway.vehicletracker.ui.welcome.HabitsPicture
import org.onebusaway.vehicletracker.ui.welcome.HabitsTimeline
import org.onebusaway.vehicletracker.ui.welcome.PictureClock
import org.onebusaway.vehicletracker.ui.welcome.PiecesPicture
import org.onebusaway.vehicletracker.ui.welcome.PiecesTimeline
import org.onebusaway.vehicletracker.ui.welcome.RidersPicture
import org.onebusaway.vehicletracker.ui.welcome.RidersTimeline
import org.onebusaway.vehicletracker.ui.welcome.WELCOME_SCREENS
import org.onebusaway.vehicletracker.ui.welcome.WelcomeContent
import org.onebusaway.vehicletracker.ui.welcome.WelcomeState
import org.onebusaway.vehicletracker.ui.welcome.rememberWelcomeState
import kotlin.math.abs

private const val FRAME_MS = 16L

/** Long enough for any of the three pictures to play out, and for a change of screen. */
private const val PLAYED_OUT_MS = 5000L

/**
 * The welcome's three screens on a test's screen, drawn with their own state and no view model, as [ScreenFlowTest]
 * draws the other screens.
 */
private class WelcomeOnScreen(private val compose: ComposeContentTestRule) {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    lateinit var state: WelcomeState

    /** How many times the driver has left the welcome. */
    var left = 0

    fun string(id: Int, vararg args: Any): String = context.getString(id, *args)

    @Composable
    fun Content() {
        AppTheme(darkTheme = false) {
            Surface {
                state = rememberWelcomeState()
                WelcomeContent(onLeave = { left++ }, state = state)
            }
        }
    }

    fun show(override: DeviceConfigurationOverride? = null) {
        compose.setContent {
            if (override == null) Content() else DeviceConfigurationOverride(override) { Content() }
        }
    }

    fun button(words: Int) = compose.onNode(hasText(string(words)) and hasClickAction())

    fun title(words: Int) = compose.onNode(hasText(string(words)) and isHeading())

    val backArrow get() = compose.onNode(hasContentDescription(string(R.string.setup_navigate_up)) and hasClickAction())

    fun step(number: Int) = compose.onNode(hasContentDescription(string(R.string.welcome_step, number, WELCOME_SCREENS)))

    /** Whether the words are on the screen shown: the other two screens are not there for TalkBack, or for a test. */
    fun shows(words: Int) = compose.onAllNodes(hasText(string(words))).fetchSemanticsNodes().isNotEmpty()

    /** Where [screen]'s picture is on its timeline, in milliseconds. */
    fun clock(screen: Int): Float = compose.runOnUiThread { state.clocks[screen].millis }
}

@RunWith(AndroidJUnit4::class)
class WelcomeScreenTest {
    @get:Rule val compose = createComposeRule()

    private val welcome = WelcomeOnScreen(compose)

    private fun pressBack() {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        compose.waitForIdle()
    }

    private fun tappables() = compose.onAllNodes(hasClickAction())

    private fun assertNothingIsASwitch() {
        compose.onAllNodes(keyIsDefined(SemanticsProperties.ToggleableState)).assertCountEquals(0)
        compose.onAllNodes(expectValue(SemanticsProperties.Role, Role.Switch)).assertCountEquals(0)
        compose.onAllNodes(expectValue(SemanticsProperties.Role, Role.Checkbox)).assertCountEquals(0)
    }

    // --- What each screen says, and what TalkBack reads ---

    @Test
    fun screenOne_saysWhatTheAppDoesForRiders() {
        welcome.show()

        welcome.title(R.string.welcome_riders_title).assertIsDisplayed()
        compose.onNode(hasText(welcome.string(R.string.welcome_riders_body))).assertIsDisplayed()
        welcome.step(1).assertIsDisplayed()
        // The picture is one element, read as one sentence, and holds nothing else to read.
        val picture = compose.onNode(hasContentDescription(welcome.string(R.string.welcome_riders_picture)))
        picture.assertIsDisplayed().assert(expectValue(SemanticsProperties.Role, Role.Image))
        assertTrue(picture.fetchSemanticsNode().children.isEmpty())
        assertFalse("the picture's sent count is not read", reads(" sent"))
        welcome.button(R.string.welcome_next).assertIsDisplayed()
        // Only Skip and Next can be tapped: the first screen has no Back.
        tappables().assertCountEquals(2)
        welcome.backArrow.assertDoesNotExist()
        assertNothingIsASwitch()
    }

    /** Whether TalkBack is read [text], as a piece of its own or within a larger one. */
    private fun reads(text: String) = compose.onAllNodes(hasText(text, substring = true, ignoreCase = true)).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun screenTwo_showsTheThreeChoices_thenRidersSeeYou_thenEndTrip() {
        welcome.show()
        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()

        welcome.title(R.string.welcome_pieces_title).assertIsDisplayed()
        compose.onNode(hasText(welcome.string(R.string.welcome_pieces_body))).assertIsDisplayed()
        welcome.step(2).assertIsDisplayed()
        // Each card is one element, its title then its hint, and none is a button.
        val cards = listOf(
            R.string.vehicles_title to R.string.welcome_pieces_vehicle_hint,
            R.string.routes_title to R.string.welcome_pieces_route_hint,
            R.string.runs_title to R.string.welcome_pieces_run_hint,
        )
        for ((choice, hint) in cards) {
            compose.onNode(hasText(welcome.string(choice)) and hasText(welcome.string(hint)) and !hasClickAction()).performScrollTo().assertIsDisplayed()
        }
        compose.onNode(hasText(welcome.string(R.string.welcome_pieces_live))).performScrollTo().assertIsDisplayed()
        assertFalse("the green card's sent count only illustrates, and is not read", reads(" sent"))
        // "When you finish, End trip": a picture of the button, so not a button.
        val finish = hasText(welcome.string(R.string.welcome_pieces_finish)) and hasText(welcome.string(R.string.tracking_end_trip_button))
        compose.onNode(finish and !hasClickAction()).performScrollTo().assertIsDisplayed()
        // Back, Skip and Next.
        tappables().assertCountEquals(3)
        assertNothingIsASwitch()
    }

    @Test
    fun screenThree_saysWhatKeepsItReporting_andGivesNoSwitch() {
        welcome.show()
        welcome.button(R.string.welcome_next).performClick()
        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()

        welcome.title(R.string.welcome_habits_title).assertIsDisplayed()
        compose.onNode(hasText(welcome.string(R.string.welcome_habits_body))).assertIsDisplayed()
        welcome.step(3).assertIsDisplayed()
        val rows = listOf(
            R.string.welcome_habits_location to R.string.welcome_habits_location_why,
            R.string.welcome_habits_charge to R.string.welcome_habits_charge_why,
            R.string.welcome_habits_open to R.string.welcome_habits_open_why,
        )
        for ((habit, why) in rows) {
            compose.onNode(hasText(welcome.string(habit)) and hasText(welcome.string(why)) and !hasClickAction()).performScrollTo().assertIsDisplayed()
        }
        compose.onNode(hasText(welcome.string(R.string.welcome_habits_if_stops)) and isHeading()).performScrollTo().assertIsDisplayed()
        // The notification's own words, so the driver knows it when it comes.
        val paused = hasText(welcome.string(R.string.tracking_notification_resume_title)) and
            hasText(welcome.string(R.string.tracking_notification_resume_text))
        compose.onNode(paused and !hasClickAction()).performScrollTo().assertIsDisplayed()
        welcome.button(R.string.welcome_continue).assertIsDisplayed()
        tappables().assertCountEquals(3)
        assertNothingIsASwitch()
    }

    @Test
    fun talkBackReadsTheScreensWords_thenTheButton_thenTheTopRow() {
        welcome.show()

        welcome.button(R.string.welcome_next).assert(expectValue(SemanticsProperties.TraversalIndex, 1f))
        welcome.button(R.string.welcome_skip).assert(expectValue(SemanticsProperties.TraversalIndex, 1f))
        // The screen the welcome opens on is read from its top; one arrived at takes focus on its title.
        welcome.title(R.string.welcome_riders_title).assertIsNotFocused()

        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()
        welcome.title(R.string.welcome_pieces_title).assertIsFocused()

        welcome.backArrow.performClick()
        compose.waitForIdle()
        welcome.title(R.string.welcome_riders_title).assertIsFocused()
    }

    // --- Next, Back and Skip ---

    @Test
    fun nextWalksThroughTheThreeScreens_andContinueLeaves() {
        welcome.show()

        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()
        assertEquals(1, welcome.state.screen)
        assertFalse("the first screen is gone", welcome.shows(R.string.welcome_riders_title))
        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()
        assertEquals(2, welcome.state.screen)
        welcome.button(R.string.welcome_next).assertDoesNotExist()
        assertEquals("no screen moves on or leaves by itself", 0, welcome.left)

        welcome.button(R.string.welcome_continue).performClick()
        compose.waitForIdle()

        assertEquals(1, welcome.left)
        assertEquals("Continue goes nowhere within the welcome", 2, welcome.state.screen)
    }

    @Test
    fun skipLeavesFromAnyScreen() {
        welcome.show()
        welcome.button(R.string.welcome_skip).performClick()
        assertEquals(1, welcome.left)

        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()
        welcome.button(R.string.welcome_skip).performClick()
        assertEquals(2, welcome.left)
        assertEquals("Skip leaves the screen where it was", 1, welcome.state.screen)
    }

    @Test
    fun backGoesToTheScreenBefore_andOnTheFirstLeavesLikeSkip() {
        welcome.show()
        welcome.button(R.string.welcome_next).performClick()
        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()

        pressBack()
        assertEquals(1, welcome.state.screen)
        welcome.title(R.string.welcome_pieces_title).assertIsDisplayed()
        welcome.backArrow.performClick()
        compose.waitForIdle()
        assertEquals(0, welcome.state.screen)
        welcome.title(R.string.welcome_riders_title).assertIsDisplayed()
        assertEquals(0, welcome.left)

        pressBack()
        assertEquals(1, welcome.left)
    }

    @Test
    fun nextNextBackTappedFast_endsOnTheMiddleScreenWithItsPicturePlayedOut() {
        compose.mainClock.autoAdvance = false
        welcome.show()
        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)

        // Each tap lands three frames into the move before it.
        welcome.button(R.string.welcome_next).performClick()
        compose.mainClock.advanceTimeBy(3 * FRAME_MS)
        welcome.button(R.string.welcome_next).performClick()
        compose.mainClock.advanceTimeBy(3 * FRAME_MS)
        welcome.backArrow.performClick()
        compose.mainClock.advanceTimeBy(3 * FRAME_MS)
        assertEquals(1, welcome.state.screen)
        assertTrue("the move is retargeted, not skipped to its end", compose.runOnUiThread { welcome.state.steps.currentState != welcome.state.steps.targetState })

        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)

        compose.runOnUiThread {
            assertEquals(1, welcome.state.steps.currentState)
            assertEquals(1, welcome.state.steps.targetState)
        }
        welcome.title(R.string.welcome_pieces_title).assertIsDisplayed()
        assertFalse(welcome.shows(R.string.welcome_riders_title))
        assertFalse(welcome.shows(R.string.welcome_habits_title))
        assertEquals("the picture shown is never welcome.left half played", PictureClock.AT_REST, welcome.clock(1), 0f)
        assertEquals("a picture that was cut off starts clean next time", 0f, welcome.clock(0), 0f)
        assertEquals(0f, welcome.clock(2), 0f)
    }

    // --- Each picture's timeline, stepped on the test clock ---

    @Test
    fun aPicturePlaysOnceWhenItsScreenAppears_andAgainWhenTheDriverComesBack() {
        compose.mainClock.autoAdvance = false
        welcome.show()
        compose.mainClock.advanceTimeByFrame()
        assertTrue("it starts once the first frame is drawn: ${welcome.clock(0)}", welcome.clock(0) < 4 * FRAME_MS)
        assertEquals("a screen not shown yet has not started", 0f, welcome.clock(1), 0f)

        compose.mainClock.advanceTimeBy(1700)
        assertEquals("part-way, on the frame clock", 1700f, welcome.clock(0), 6f * FRAME_MS)
        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)
        assertEquals("at rest, and it does not loop", PictureClock.AT_REST, welcome.clock(0), 0f)

        welcome.button(R.string.welcome_next).performClick()
        compose.mainClock.advanceTimeBy(1000)
        assertTrue("the second screen's picture plays as it comes in: ${welcome.clock(1)}", welcome.clock(1) in 700f..1000f)
        assertEquals("and the first, gone, is rewound", 0f, welcome.clock(0), 0f)
        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)
        assertEquals(PictureClock.AT_REST, welcome.clock(1), 0f)

        welcome.backArrow.performClick()
        compose.mainClock.advanceTimeBy(500)
        assertTrue("back on the first screen its picture plays again from the start: ${welcome.clock(0)}", welcome.clock(0) in 200f..500f)
        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)
        assertEquals(PictureClock.AT_REST, welcome.clock(0), 0f)
        assertEquals(0f, welcome.clock(1), 0f)
    }

    @Test
    fun theThirdScreensPictureRestsInUnderTwoSeconds() {
        compose.mainClock.autoAdvance = false
        welcome.show()
        welcome.button(R.string.welcome_next).performClick()
        welcome.button(R.string.welcome_next).performClick()

        compose.mainClock.advanceTimeBy(800)
        assertTrue("part-way: ${welcome.clock(2)}", welcome.clock(2) in 500f..800f)
        compose.mainClock.advanceTimeBy(1200)
        assertEquals(PictureClock.AT_REST, welcome.clock(2), 0f)
    }

    // --- Nothing is recomposed for a frame of a picture ---

    private fun recompositions(): Long = Recomposer.runningRecomposers.value.sumOf { it.changeCount }

    /** Plays [picture] from its first frame to its last, and returns how many frames that took. */
    private fun playThrough(endMillis: Float, picture: @Composable (PictureClock) -> Unit): Int {
        compose.mainClock.autoAdvance = false
        val clock = PictureClock(endMillis) { false }
        compose.setContent {
            AppTheme(darkTheme = false) {
                Surface {
                    picture(clock)
                    LaunchedEffect(clock) { clock.play() }
                }
            }
        }
        // The picture's own composition, and the frame its clock starts on.
        repeat(3) { compose.mainClock.advanceTimeByFrame() }
        val composed = recompositions()
        var frames = 0
        while (compose.runOnUiThread { clock.millis } < endMillis) {
            compose.mainClock.advanceTimeByFrame()
            frames++
        }
        assertEquals("recompositions while the picture played", composed, recompositions())
        return frames
    }

    @Test
    fun screenOnesPicture_recomposesNothingWhileItPlays() {
        val frames = playThrough(RidersTimeline.END) { RidersPicture(it, compact = false, Modifier.size(364.dp, 510.dp)) }

        assertTrue("it played frame by frame: $frames", frames > 200)
    }

    @Test
    fun screenTwosPicture_recomposesNothingWhileItPlays() {
        val frames = playThrough(PiecesTimeline.END) { PiecesPicture(it, compact = false) }

        assertTrue("it played frame by frame: $frames", frames > 200)
    }

    @Test
    fun screenThreesPicture_recomposesNothingWhileItPlays() {
        val frames = playThrough(HabitsTimeline.END) { HabitsPicture(it, compact = false) }

        assertTrue("it played frame by frame: $frames", frames > 80)
    }

    @Test
    fun theWholeWelcome_recomposesNothingWhileAPicturePlays() {
        compose.mainClock.autoAdvance = false
        welcome.show()
        // The first screen, then the other two in the frames after it.
        repeat(6) { compose.mainClock.advanceTimeByFrame() }
        val composed = recompositions()

        compose.mainClock.advanceTimeBy(PLAYED_OUT_MS)

        assertEquals(PictureClock.AT_REST, welcome.clock(0), 0f)
        assertEquals(composed, recompositions())
    }

    // --- Rotation, sizes and the largest text ---

    @Test
    fun theScreenTheDriverWasOnComesBackAfterTheActivityIsRecreated() {
        val restoration = StateRestorationTester(compose)
        restoration.setContent { welcome.Content() }
        welcome.button(R.string.welcome_next).performClick()
        compose.waitForIdle()
        val before = welcome.state

        restoration.emulateSavedInstanceStateRestore()
        compose.waitForIdle()

        assertTrue("a new screen was built", welcome.state !== before)
        assertEquals(1, welcome.state.screen)
        welcome.title(R.string.welcome_pieces_title).assertIsDisplayed()
        welcome.step(2).assertIsDisplayed()
        welcome.backArrow.assertIsDisplayed()
    }

    /** No text on the screen shown is cut short or pushed off its side, and its last piece and the button can be reached. */
    private fun assertNothingIsCutOff() {
        val root = compose.onRoot().fetchSemanticsNode().boundsInRoot
        val texts = compose.onAllNodes(keyIsDefined(SemanticsActions.GetTextLayoutResult), useUnmergedTree = true).fetchSemanticsNodes()
        assertTrue(texts.size >= 4)
        for (node in texts) {
            val layouts = mutableListOf<TextLayoutResult>()
            node.config[SemanticsActions.GetTextLayoutResult].action?.invoke(layouts)
            for (layout in layouts) {
                val words = layout.layoutInput.text.text
                val lines = 0 until layout.lineCount
                assertFalse("\"$words\" is cut short", layout.didOverflowHeight || lines.any(layout::isLineEllipsized))
                assertTrue("\"$words\" is wider than its place", lines.all { layout.getLineRight(it) <= layout.size.width + 0.5f })
                assertTrue("\"$words\" runs off the side", node.boundsInRoot.left >= 0f && node.boundsInRoot.right <= root.right + 0.5f)
            }
        }
    }

    private fun assertEveryScreenFits() {
        assertNothingIsCutOff()
        compose.onNode(hasContentDescription(welcome.string(R.string.welcome_riders_picture))).performScrollTo().assertIsDisplayed()
        welcome.button(R.string.welcome_next).assertIsDisplayed().performClick()
        compose.waitForIdle()
        assertNothingIsCutOff()
        compose.onNode(hasText(welcome.string(R.string.welcome_pieces_finish))).performScrollTo().assertIsDisplayed()
        welcome.button(R.string.welcome_next).assertIsDisplayed().performClick()
        compose.waitForIdle()
        assertNothingIsCutOff()
        compose.onNode(hasText(welcome.string(R.string.tracking_notification_resume_title))).performScrollTo().assertIsDisplayed()
        welcome.button(R.string.welcome_continue).assertIsDisplayed()
        welcome.button(R.string.welcome_skip).assertIsDisplayed()
        welcome.backArrow.assertIsDisplayed()
    }

    @Test
    fun onASmallPhone_nothingIsCutOff() {
        welcome.show(DeviceConfigurationOverride.ForcedSize(DpSize(320.dp, 640.dp)))

        assertEveryScreenFits()
    }

    @Test
    fun atTabletWidth_nothingIsCutOff() {
        welcome.show(DeviceConfigurationOverride.ForcedSize(DpSize(840.dp, 1200.dp)))

        assertEveryScreenFits()
    }

    @Test
    fun atTheLargestText_nothingIsCutOff_andTheLongScreensScroll() {
        welcome.show(DeviceConfigurationOverride.ForcedSize(DpSize(412.dp, 915.dp)) then DeviceConfigurationOverride.FontScale(2f))

        assertEveryScreenFits()
    }

    @Test
    fun atTheLargestTextOnASmallPhone_nothingIsCutOff_andTheFirstPictureKeepsItsHeight() {
        welcome.show(DeviceConfigurationOverride.ForcedSize(DpSize(320.dp, 640.dp)) then DeviceConfigurationOverride.FontScale(2f))

        // The words leave the picture less room than it needs, so the screen scrolls rather than squeeze it.
        compose.onNode(hasContentDescription(welcome.string(R.string.welcome_riders_picture))).assertHeightIsAtLeast(199.dp)
        assertEveryScreenFits()
    }
}

/** The welcome with animations removed on the phone, which Compose reports as a motion duration scale of 0. */
@RunWith(AndroidJUnit4::class)
class WelcomeStillTest {
    @get:Rule val compose = createComposeRule(
        effectContext = object : MotionDurationScale {
            override val scaleFactor = 0f
        },
    )

    private val welcome = WelcomeOnScreen(compose)

    /** The colour at a point of the first screen's picture, as fractions of its width and height. */
    private fun pictureColorAt(x: Float, y: Float): Color {
        val image = compose.onNode(hasContentDescription(welcome.string(R.string.welcome_riders_picture))).captureToImage().asAndroidBitmap()
        return Color(image.getPixel((image.width * x).toInt(), (image.height * y).toInt()))
    }

    private fun assertClose(expected: Color, actual: Color) {
        val apart = abs(expected.red - actual.red) + abs(expected.green - actual.green) + abs(expected.blue - actual.blue)
        assertTrue("expected $expected, found $actual", apart < 0.08f)
    }

    @Test
    fun everyScreenOpensFinished() {
        compose.mainClock.autoAdvance = false
        welcome.show()

        assertTrue(compose.runOnUiThread { welcome.state.still })
        assertEquals("finished from its first frame", PictureClock.AT_REST, welcome.clock(0), 0f)
        assertEquals(PictureClock.AT_REST, welcome.clock(1), 0f)
        assertEquals(PictureClock.AT_REST, welcome.clock(2), 0f)
    }

    @Test
    fun theFirstPictureOpensAtRest_withTheVehicleLiveAtItsPlace() {
        welcome.show(DeviceConfigurationOverride.ForcedSize(DpSize(412.dp, 915.dp)))

        // The route runs down the picture at 36% of its width, and the vehicle rests at half its height. Just inside
        // the vehicle's edge, its ring is the on-time green; at rest nothing is welcome.left where it started, low on the route.
        val ringFromCentre = 21f / 364f
        assertClose(LightStatusColors.onTime.text, pictureColorAt(0.36f - ringFromCentre, 0.50f))
        assertClose(Color(0xFF3B6939), pictureColorAt(0.36f, 0.82f))
    }

    @Test
    fun nextAndBackChangeTheScreenWithNoSlide() {
        compose.mainClock.autoAdvance = false
        welcome.show()
        compose.mainClock.advanceTimeByFrame()

        welcome.button(R.string.welcome_next).performClick()
        // A few frames for the tap to be read and acted on. A slide would take about twenty.
        repeat(4) { compose.mainClock.advanceTimeByFrame() }

        compose.runOnUiThread {
            assertEquals(1, welcome.state.steps.currentState)
            assertEquals(1, welcome.state.steps.targetState)
        }
        welcome.title(R.string.welcome_pieces_title).assertIsDisplayed()
        assertFalse(welcome.shows(R.string.welcome_riders_title))
        assertEquals(PictureClock.AT_REST, welcome.clock(1), 0f)

        welcome.backArrow.performClick()
        repeat(4) { compose.mainClock.advanceTimeByFrame() }

        compose.runOnUiThread { assertEquals(0, welcome.state.steps.currentState) }
        welcome.title(R.string.welcome_riders_title).assertIsDisplayed()
    }
}
