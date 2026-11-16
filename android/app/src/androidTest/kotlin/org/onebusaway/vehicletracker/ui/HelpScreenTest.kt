package org.onebusaway.vehicletracker.ui

import android.app.Notification
import android.content.Intent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher.Companion.keyIsDefined
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.core.content.IntentCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.service.TrackingNotification
import org.onebusaway.vehicletracker.ui.account.AccountMenuContent
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.help.HelpAbout
import org.onebusaway.vehicletracker.ui.help.HelpContent
import org.onebusaway.vehicletracker.ui.help.ProblemHelp
import org.onebusaway.vehicletracker.ui.help.cardStatusHelp
import org.onebusaway.vehicletracker.ui.help.helpPage
import org.onebusaway.vehicletracker.ui.help.problemHelp
import org.onebusaway.vehicletracker.ui.help.shareIntent
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.StatusArea
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel
import java.time.Instant
import java.time.ZoneId

/** The Help item in the account menu, and the Help page: its words, what TalkBack reads, the largest text, and sharing. */
@RunWith(AndroidJUnit4::class)
class HelpScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int) = context.getString(id)

    private val about = HelpAbout("0.1.0 (1)", "tracker.example.org")
    private val list get() = compose.onNode(hasScrollToNodeAction())

    private fun showHelp(onBack: () -> Unit = {}, onWelcomeAgain: () -> Unit = {}, onShare: () -> Unit = {}) {
        compose.setContent { AppTheme(darkTheme = false) { HelpContent(about, onBack, onWelcomeAgain, onShare) } }
    }

    /** The first text of each node TalkBack can land on, in the order it reaches them, and which of them are headings. */
    private fun spokenInOrder(): List<Pair<String, Boolean>> =
        compose.onAllNodes(keyIsDefined(SemanticsProperties.Text)).fetchSemanticsNodes().map { node ->
            node.config[SemanticsProperties.Text].first().text to (node.config.getOrNull(SemanticsProperties.Heading) != null)
        }

    // --- The account menu ---

    private val account = AccountUiState(email = "d@example.com", server = "tracker.example.com")

    @Test
    fun accountMenu_hasHelpBelowTheBuildAndAboveSignOut_andHelpOpensIt() {
        var opened = 0
        compose.setContent {
            AccountMenuContent(account, "0.1.0 (1)", enabled = true, onSignOut = {}, onSignedOut = {}, onHelp = { opened++ })
        }

        compose.onNodeWithContentDescription(string(R.string.account_menu)).performClick()
        val version = compose.onNodeWithText(context.getString(R.string.account_version, "0.1.0 (1)")).fetchSemanticsNode().boundsInRoot
        val help = compose.onNodeWithText(string(R.string.help_title)).fetchSemanticsNode().boundsInRoot
        val signOut = compose.onNodeWithText(string(R.string.account_sign_out)).fetchSemanticsNode().boundsInRoot
        assertTrue("Help is under the build, past the divider", help.top >= version.bottom)
        assertTrue("Help is over Sign out", help.bottom <= signOut.top)

        compose.onNodeWithText(string(R.string.help_title)).performClick()

        assertEquals(1, opened)
        compose.onNodeWithText(string(R.string.account_sign_out)).assertDoesNotExist()
    }

    @Test
    fun accountMenu_withNoHelpToOpen_hasNoHelp() {
        compose.setContent { AccountMenuContent(account, "0.1.0 (1)", enabled = true, onSignOut = {}, onSignedOut = {}) }

        compose.onNodeWithContentDescription(string(R.string.account_menu)).performClick()

        compose.onNodeWithText(string(R.string.account_sign_out)).assertExists()
        compose.onNodeWithText(string(R.string.help_title)).assertDoesNotExist()
    }

    // --- The rows' words ---

    private val stop = TripStop("ST2", "Rainier Ave S & S Forest St", 47.6045, -122.33, 500.0, Instant.EPOCH, Instant.EPOCH)
    private val zone = ZoneId.of("America/Los_Angeles")

    /** What Tracking can show, each next to the problem under it: one of every answer, then one of every problem. */
    private val trackingShows: List<Pair<StatusCardModel, TrackingProblem>> = buildList {
        val waiting = StatusCardModel.WaitingForGps(stop, zone, "4317")
        add(StatusCardModel.Adherence(StatusTone.ON_TIME, AdherenceStatus.ON_TIME, 0.0, stop, 300.0, zone) to TrackingProblem.NONE)
        add(StatusCardModel.Adherence(StatusTone.LATE, AdherenceStatus.LATE, 360.0, stop, 300.0, zone) to TrackingProblem.NONE)
        add(StatusCardModel.Adherence(StatusTone.EARLY, AdherenceStatus.EARLY, -180.0, stop, 300.0, zone) to TrackingProblem.NONE)
        add(StatusCardModel.OffSchedule(StatusTone.EARLY, -1200.0, stop, zone) to TrackingProblem.NONE)
        add(StatusCardModel.OffRoute(StatusTone.NEUTRAL, 750.0, stop, 300.0, zone) to TrackingProblem.NONE)
        add(waiting to TrackingProblem.NONE)
        add(StatusCardModel.NoSchedule to TrackingProblem.NONE)
        TrackingProblem.entries.filter { it != TrackingProblem.NONE }.forEach { add(waiting to it) }
    }

    /** The title of Help's row whose meaning line is [meaning], read off the page. */
    private fun helpTitleOver(meaning: Int): String {
        val row = hasText(string(meaning)) and hasAnyAncestor(hasScrollToNodeAction())
        list.performScrollToNode(row)
        val texts = compose.onNode(row).fetchSemanticsNode().config[SemanticsProperties.Text].map { it.text }
        assertEquals("a row is its title, then its meaning", listOf(string(meaning)), texts.drop(1))
        return texts.first()
    }

    @Test
    fun eachRowsTitle_isWhatTrackingShows() {
        var shown by mutableIntStateOf(0)
        compose.setContent {
            AppTheme(darkTheme = false) {
                Column {
                    val (model, problem) = trackingShows[shown]
                    Box(Modifier.height(240.dp)) { StatusArea(model, problem, onSignIn = {}) }
                    Box(Modifier.weight(1f)) { HelpContent(about, {}, {}, {}) }
                }
            }
        }

        for ((i, shows) in trackingShows.withIndex()) {
            compose.runOnIdle { shown = i }
            val (model, problem) = shows
            val meaning = problemHelp(problem)?.meaning ?: cardStatusHelp(model).meaning
            val title = helpTitleOver(meaning)
            val onTracking = hasText(title) and !hasAnyAncestor(hasScrollToNodeAction())
            assertTrue(
                "Tracking shows \"$title\" for $model, $problem",
                compose.onAllNodes(onTracking, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty(),
            )
        }
    }

    @Test
    fun reportsPausedsTitle_isTheNotifications() {
        showHelp()
        val notification = TrackingNotification(context).buildResumeNotification()

        assertEquals(notification.extras.getString(Notification.EXTRA_TITLE), helpTitleOver(ProblemHelp.REPORTS_PAUSED.meaning))
    }

    @Test
    fun theAboutRows_sayTheBuildAndTheServer() {
        showHelp()

        list.performScrollToNode(hasText("tracker.example.org"))
        compose.onNode(hasText(string(R.string.help_version)) and hasText("0.1.0 (1)")).assertExists()
        compose.onNode(hasText(string(R.string.help_server)) and hasText("tracker.example.org")).assertExists()
    }

    // --- Taps ---

    @Test
    fun theWelcomeRowAndShareAreButtons_andBackGoesBack() {
        var back = 0
        var welcome = 0
        var share = 0
        showHelp(onBack = { back++ }, onWelcomeAgain = { welcome++ }, onShare = { share++ })

        compose.onNode(hasText(string(R.string.help_welcome_again)) and hasClickAction()).performClick()
        list.performScrollToNode(hasText(string(R.string.help_share)))
        compose.onNode(hasText(string(R.string.help_share)) and hasClickAction()).performClick()
        compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).performClick()

        assertEquals(listOf(1, 1, 1), listOf(back, welcome, share))
    }

    @Test
    fun shareOpensTheShareSheetWithTheTextInIt() {
        val chooser = shareIntent("Vehicle Tracker diagnostics\nApp: 0.1.0 (1)")

        assertEquals(Intent.ACTION_CHOOSER, chooser.action)
        val send = requireNotNull(IntentCompat.getParcelableExtra(chooser, Intent.EXTRA_INTENT, Intent::class.java))
        assertEquals(Intent.ACTION_SEND, send.action)
        assertEquals("text/plain", send.type)
        assertEquals("Vehicle Tracker diagnostics\nApp: 0.1.0 (1)", send.getStringExtra(Intent.EXTRA_TEXT))
    }

    // --- TalkBack ---

    @Test
    fun talkBackReadsTheHeadingsAndRowsInThePagesOrder() {
        compose.setContent {
            AppTheme(darkTheme = false) {
                // Tall enough for every row to be on the page at once.
                Box(Modifier.requiredHeight(3000.dp)) { HelpContent(about, {}, {}, {}) }
            }
        }

        val spoken = spokenInOrder()

        val headings = spoken.filter { it.second }.map { it.first }
        assertEquals(
            listOf(R.string.help_title, R.string.help_section_status, R.string.help_section_problems, R.string.help_section_about).map(::string),
            headings,
        )
        val firsts = spoken.map { it.first }
        val order = listOf(
            string(R.string.help_title),
            string(R.string.help_welcome_again),
            string(R.string.help_section_status),
            string(R.string.tracking_adherence_on_time),
            string(R.string.tracking_adherence_no_schedule),
            string(R.string.help_section_problems),
            string(R.string.tracking_problem_no_network),
            string(R.string.tracking_notification_resume_title),
            string(R.string.help_section_about),
            string(R.string.help_version),
            string(R.string.help_server),
            string(R.string.help_share),
            string(R.string.help_share_detail),
        )
        val at = order.map { firsts.indexOf(it) }
        assertTrue("all read: $firsts", at.none { it < 0 })
        assertEquals("in the page's order: $firsts", at.sorted(), at)
        // Each row is read as one, title then meaning: the bar's title, one for each item of the page, and the line
        // under Share.
        assertEquals(firsts.toString(), 1 + helpPage.size + 1, firsts.size)
    }

    // --- The largest text ---

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        compose.setContent {
            AppTheme(darkTheme = false) {
                CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                    Box(Modifier.requiredWidth(320.dp)) { HelpContent(about, {}, {}, {}) }
                }
            }
        }

        for (i in helpPage.indices) {
            list.performScrollToIndex(i)
            assertNothingIsCutOff()
        }
    }

    /** No text on screen is cut short or pushed off its side. */
    private fun assertNothingIsCutOff() {
        val root = compose.onRoot().fetchSemanticsNode().boundsInRoot
        val texts = compose.onAllNodes(keyIsDefined(SemanticsActions.GetTextLayoutResult), useUnmergedTree = true).fetchSemanticsNodes()
        assertTrue(texts.isNotEmpty())
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
}
