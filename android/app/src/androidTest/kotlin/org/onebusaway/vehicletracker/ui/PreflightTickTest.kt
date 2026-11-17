package org.onebusaway.vehicletracker.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher.Companion.expectValue
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.ChecklistRow
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.PreflightContent
import org.onebusaway.vehicletracker.ui.permissions.RowState
import org.onebusaway.vehicletracker.ui.permissions.RowState.ALLOWED
import org.onebusaway.vehicletracker.ui.permissions.RowState.APPROXIMATE
import org.onebusaway.vehicletracker.ui.permissions.RowState.NEEDED
import org.onebusaway.vehicletracker.ui.permissions.RowState.NEXT
import org.onebusaway.vehicletracker.ui.permissions.RowState.NOT_REACHED
import org.onebusaway.vehicletracker.ui.permissions.RowState.SKIPPED
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.LightColors
import kotlin.math.abs

/** Long enough for any change on the checklist to finish. */
private const val SETTLED_MS = 1000L

/** About a fifth of a second of frames: the tick's dip is over, and the check has landed. */
private const val TICK_FRAMES = 12

/**
 * The checklist before a trip on a test's screen, in the app's light theme, with rows a test changes the way Android's
 * answers change them. Positions are in dp.
 */
private class ChecklistOnScreen(private val compose: ComposeContentTestRule) {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val density = context.resources.displayMetrics.density
    private var rows by mutableStateOf(emptyList<ChecklistRow>())

    fun string(id: Int): String = context.getString(id)

    private fun rowsOf(states: Array<out RowState>) = PermissionRow.entries.zip(states.toList()) { row, state -> ChecklistRow(row, state) }

    fun show(vararg states: RowState) {
        rows = rowsOf(states)
        compose.setContent {
            AppTheme(darkTheme = false) { PreflightContent("4317", rows, canContinue = true, onBack = {}, onContinue = {}, onRowAction = {}) }
        }
    }

    /** Android answered, or the driver came back from its settings: the rows as the screen shows them next. */
    fun answer(vararg states: RowState) = compose.runOnUiThread { rows = rowsOf(states) }

    /** A row's marker and words, which TalkBack reads as one, found by its title. */
    private fun row(title: Int) = compose.onNodeWithText(string(title))

    fun topOf(title: Int): Float = row(title).fetchSemanticsNode().boundsInRoot.top / density

    fun widthOf(title: Int): Float = row(title).fetchSemanticsNode().boundsInRoot.width / density

    /** The colour 3 dp in from the left edge of a row's marker, halfway down: the check's colour once it has landed. */
    fun markerEdgeOf(title: Int): Color {
        val image = row(title).captureToImage().asAndroidBitmap()
        return Color(image.getPixel((3 * density).toInt(), image.height / 2))
    }

    /**
     * How far below its resting place each row is at each of the next [frames] frames. The resting place is where the
     * row is once everything has settled: a change of words can wrap a row's reason again and move the rows at once.
     */
    fun dipsOf(vararg titles: Int, frames: Int = TICK_FRAMES): Map<Int, List<Float>> {
        val tops = titles.associateWith { mutableListOf<Float>() }
        repeat(frames) {
            compose.mainClock.advanceTimeByFrame()
            titles.forEach { tops.getValue(it) += topOf(it) }
        }
        compose.mainClock.advanceTimeBy(SETTLED_MS)
        return titles.associateWith { title -> topOf(title).let { rest -> tops.getValue(title).map { it - rest } } }
    }

    /** What TalkBack reads at the end of a row. */
    fun says(title: Int, state: Int) = hasContentDescription(context.getString(R.string.preflight_row_state, string(title), string(state)))
}

private fun apart(a: Color, b: Color): Float = abs(a.red - b.red) + abs(a.green - b.green) + abs(a.blue - b.blue)

private fun near(expected: Color, actual: Color): Boolean = apart(expected, actual) < 0.08f

private fun assertStill(what: String, dips: List<Float>) = assertTrue("$what: $dips", dips.all { abs(it) < 0.1f })

/** The dip as the check lands: about 2 dp at its deepest, and nothing above the row's place. */
private fun assertDips(what: String, dips: List<Float>) =
    assertTrue("$what: $dips", dips.max() in 1.5f..2.5f && dips.min() > -0.1f)

/** "Before you start" ticks a row when the screen sees its permission become allowed, and at no other time. */
@RunWith(AndroidJUnit4::class)
class PreflightTickTest {
    @get:Rule val compose = createComposeRule()

    private val checklist = ChecklistOnScreen(compose)

    @Test
    fun aGrantTicksTheRow_theIconTurnsIntoTheCheckAndTheRowSettles() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)
        assertTrue(near(LightColors.primaryContainer, checklist.markerEdgeOf(R.string.preflight_location)))

        checklist.answer(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        val edges = List(8) {
            compose.mainClock.advanceTimeByFrame()
            checklist.markerEdgeOf(R.string.preflight_location)
        }

        // Grown from half its size while the icon shrinks away, so for a moment the marker's edge is close to the
        // screen behind it. A cross-fade keeps it as strongly coloured as either marker throughout.
        assertTrue("the check grows in: $edges", edges.any { apart(LightColors.surface, it) < 0.2f })
        compose.mainClock.advanceTimeBy(SETTLED_MS)
        assertTrue(near(LightColors.primary, checklist.markerEdgeOf(R.string.preflight_location)))
    }

    @Test
    fun aRowTicksHoweverTheScreenSeesItBecomeAllowed() {
        compose.mainClock.autoAdvance = false
        // Android's prompt, precise location granted with location, and a skipped row allowed later in Android's settings.
        checklist.show(NEXT, NOT_REACHED, SKIPPED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        checklist.answer(ALLOWED, ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED)
        val dips = checklist.dipsOf(R.string.preflight_location, R.string.preflight_precise, R.string.preflight_background)

        assertDips("from Next", dips.getValue(R.string.preflight_location))
        assertDips("from not reached", dips.getValue(R.string.preflight_precise))
        assertDips("from skipped", dips.getValue(R.string.preflight_background))
    }

    @Test
    fun theRowsWordsKeepTheirLinesWhileItTicks() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        checklist.answer(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeByFrame()
        // "Allowed" is wider than "Next": the title and reason make room once, not a little more on every frame.
        val widths = List(TICK_FRAMES) {
            compose.mainClock.advanceTimeByFrame()
            checklist.widthOf(R.string.preflight_location)
        }

        assertTrue("$widths", widths.all { abs(it - widths.first()) < 0.1f })
    }

    @Test
    fun aRowAllowedFromTheStartDoesNotTick() {
        compose.mainClock.autoAdvance = false
        checklist.show(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeByFrame()

        val edges = List(4) {
            compose.mainClock.advanceTimeByFrame()
            checklist.markerEdgeOf(R.string.preflight_location)
        }
        // And not when another row changes under it.
        checklist.answer(ALLOWED, ALLOWED, SKIPPED, NEXT, NOT_REACHED, NOT_REACHED)

        assertTrue("the check is there from the first frame: $edges", edges.all { near(LightColors.primary, it) })
        assertStill("allowed from the start", checklist.dipsOf(R.string.preflight_location).getValue(R.string.preflight_location))
    }

    @Test
    fun aRefusalDoesNotTick() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        checklist.answer(NEEDED, NOT_REACHED, SKIPPED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        val dips = checklist.dipsOf(R.string.preflight_location, R.string.preflight_background)

        assertStill("location refused", dips.getValue(R.string.preflight_location))
        assertStill("all the time refused", dips.getValue(R.string.preflight_background))
    }

    @Test
    fun aRowTicksOnce_notAgainWhenAndroidIsAskedForPreciseLocation() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        // Only approximate allowed: location ticks.
        checklist.answer(ALLOWED, APPROXIMATE, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        assertDips("location allowed", checklist.dipsOf(R.string.preflight_location).getValue(R.string.preflight_location))
        // Try again: Android is asked about location again, so its row is next again until the answer.
        checklist.answer(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        checklist.answer(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        val dips = checklist.dipsOf(R.string.preflight_location, R.string.preflight_precise)

        assertStill("location, allowed all along", dips.getValue(R.string.preflight_location))
        assertDips("precise location, allowed now", dips.getValue(R.string.preflight_precise))
    }

    @Test
    fun talkBackReadsTheNewStateOnce_withTheRowsName() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeBy(SETTLED_MS)

        checklist.answer(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        // Halfway through the tick, while "Next" fades out under "Allowed".
        repeat(3) { compose.mainClock.advanceTimeByFrame() }

        val announced = expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite)
        compose.onNode(checklist.says(R.string.preflight_location, R.string.preflight_allowed) and announced).assertExists()
        compose.onNode(checklist.says(R.string.preflight_precise, R.string.preflight_allowed) and announced).assertExists()
        compose.onNode(checklist.says(R.string.preflight_location, R.string.preflight_next)).assertDoesNotExist()
        // The words drawn at the end of the row are not read a second time, even while both are on screen.
        for (state in listOf(R.string.preflight_allowed, R.string.preflight_next)) {
            compose.onNode(hasText(checklist.string(state))).assertDoesNotExist()
        }
        // The row's own words do not change.
        compose.onNode(hasText(checklist.string(R.string.preflight_location)) and hasText(checklist.string(R.string.preflight_location_why)))
            .assertExists()
    }
}

/** The checklist with animations removed on the phone, which Compose reports as a motion duration scale of 0. */
@RunWith(AndroidJUnit4::class)
class PreflightTickStillTest {
    @get:Rule val compose = createComposeRule(
        effectContext = object : MotionDurationScale {
            override val scaleFactor = 0f
        },
    )

    private val checklist = ChecklistOnScreen(compose)

    @Test
    fun aGrantShowsTheCheckAtOnce_withNoDip() {
        compose.mainClock.autoAdvance = false
        checklist.show(NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        compose.mainClock.advanceTimeByFrame()

        checklist.answer(ALLOWED, ALLOWED, NEXT, NOT_REACHED, NOT_REACHED, NOT_REACHED)
        assertStill("no dip", checklist.dipsOf(R.string.preflight_location, frames = 4).getValue(R.string.preflight_location))

        assertTrue(near(LightColors.primary, checklist.markerEdgeOf(R.string.preflight_location)))
        compose.onNode(checklist.says(R.string.preflight_location, R.string.preflight_allowed)).assertExists()
    }
}
