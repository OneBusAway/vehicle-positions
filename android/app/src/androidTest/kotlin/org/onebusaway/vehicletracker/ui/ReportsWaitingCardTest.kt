package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
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
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.ui.help.HelpAbout
import org.onebusaway.vehicletracker.ui.help.HelpContent
import org.onebusaway.vehicletracker.ui.help.waitingHelp
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.StatusArea
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState

/** The status card's waiting line on Tracking: its words, what TalkBack reads and when, and its place on the card. */
@RunWith(AndroidJUnit4::class)
class ReportsWaitingCardTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int) = context.getString(id)

    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20260902", 100L)

    // A trip with no schedule on the phone, so the card's last line is a fixed string the waiting line takes over.
    private var waiting by mutableStateOf<ReportsWaiting?>(null)

    private fun showTracking() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(
                    tracking = TrackingState(active = true, problem = TrackingProblem.NO_NETWORK),
                    activeTrip = trip,
                    waiting = waiting,
                ),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }
    }

    private val lastLine get() = string(R.string.tracking_adherence_no_schedule_detail)

    /** The waiting line as TalkBack lands on it: one node, its words, and whether it reads changes out. */
    private class Line(val texts: List<String>, val speaks: Boolean, val bounds: Rect)

    private fun line(words: String): Line {
        val node = compose.onNode(hasText(words)).fetchSemanticsNode()
        return Line(
            texts = node.config[SemanticsProperties.Text].map { it.text },
            speaks = node.config.getOrNull(SemanticsProperties.LiveRegion) != null,
            bounds = node.boundsInRoot,
        )
    }

    private fun show(next: ReportsWaiting?) {
        compose.runOnIdle { waiting = next }
        compose.waitForIdle()
    }

    @Test
    fun theCountTakesTheLastLinesPlace_goesDownAsItUploads_andGoesAway() {
        showTracking()
        val before = compose.onNode(hasText(lastLine)).fetchSemanticsNode().boundsInRoot

        show(ReportsWaiting(1, WaitingOutlook.WILL_SEND))
        val one = line("1 report to send when online")
        assertEquals("TalkBack reads the line as it shows", listOf("1 report to send when online"), one.texts)
        assertTrue("TalkBack reads it out as it comes", one.speaks)
        assertEquals("in the last line's place", before.top, one.bounds.top)
        assertEquals("at its height", before.height, one.bounds.height)
        compose.onNode(hasText(lastLine)).assertDoesNotExist()

        show(ReportsWaiting(37, WaitingOutlook.WILL_SEND))
        val many = line("37 reports to send when online")
        assertEquals(listOf("37 reports to send when online"), many.texts)
        assertFalse("quiet as reports add to it", many.speaks)

        show(ReportsWaiting(12, WaitingOutlook.WILL_SEND))
        assertTrue("read out as the count goes down", line("12 reports to send when online").speaks)

        show(null)
        compose.onNode(hasText("12 reports to send when online")).assertDoesNotExist()
        assertEquals("the last line is back", before, compose.onNode(hasText(lastLine)).fetchSemanticsNode().boundsInRoot)
    }

    // Offline for days, the count dips as each fix ages out in the queue: about once a report (#463).
    @Test
    fun quietAsAFixAgesOut_readOutAsAnUploadSends() {
        showTracking()
        show(ReportsWaiting(37, WaitingOutlook.WILL_SEND, queued = 40))

        show(ReportsWaiting(36, WaitingOutlook.WILL_SEND, queued = 40))
        assertFalse("quiet as one ages out", line("36 reports to send when online").speaks)

        show(ReportsWaiting(6, WaitingOutlook.WILL_SEND, queued = 6))
        assertTrue("read out as an upload sends", line("6 reports to send when online").speaks)
    }

    @Test
    fun reportsThatWillNeverSend_sayThat() {
        showTracking()

        show(ReportsWaiting(1, WaitingOutlook.NEVER))
        assertEquals(listOf("1 report won't send"), line("1 report won't send").texts)

        show(ReportsWaiting(37, WaitingOutlook.NEVER))
        assertEquals(listOf("37 reports won't send"), line("37 reports won't send").texts)

        show(ReportsWaiting(37, WaitingOutlook.WILL_SEND))
        assertTrue("read out as they turn to will send", line("37 reports to send when online").speaks)
        compose.onNode(hasText("37 reports won't send")).assertDoesNotExist()
    }

    /** Help's row for each outlook is titled as the card counts the same number of reports. */
    @Test
    fun eachWaitingRowsTitle_isWhatTrackingShows() {
        val model = StatusCardModel.NoSchedule
        var outlook by mutableStateOf(WaitingOutlook.WILL_SEND)
        compose.setContent {
            AppTheme(darkTheme = false) {
                Column {
                    Box(Modifier.height(240.dp)) {
                        StatusArea(model, TrackingProblem.NONE, onSignIn = {}, waiting = ReportsWaiting(3, outlook))
                    }
                    // Tall enough for every row to be on the page at once.
                    Box(Modifier.requiredHeight(3000.dp)) { HelpContent(HelpAbout("0.1.0 (1)", "tracker.example.org"), {}, {}, {}) }
                }
            }
        }

        for (each in WaitingOutlook.entries) {
            compose.runOnIdle { outlook = each }
            val meaning = string(waitingHelp(each).meaning)
            val row = compose.onNode(hasText(meaning) and hasAnyAncestor(hasScrollToNodeAction())).fetchSemanticsNode()
            val title = row.config[SemanticsProperties.Text].first().text
            val onTracking = compose.onAllNodes(hasText(title) and !hasAnyAncestor(hasScrollToNodeAction()))
            assertEquals("Tracking shows \"$title\" for $each", 1, onTracking.fetchSemanticsNodes().size)
        }
    }
}
