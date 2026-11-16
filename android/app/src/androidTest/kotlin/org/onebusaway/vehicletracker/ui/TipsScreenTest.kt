package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Composer
import androidx.compose.runtime.CompositionTracer
import androidx.compose.runtime.InternalComposeTracingApi
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverFlagsStore
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.runs.Board
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.RunPage
import org.onebusaway.vehicletracker.ui.runs.RunsScreenContent
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.TripRun
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tips.DriverTips
import org.onebusaway.vehicletracker.ui.tips.FinishedTrip
import org.onebusaway.vehicletracker.ui.tips.FirstTripDoneHost
import org.onebusaway.vehicletracker.ui.tips.FirstTripDoneSheet
import org.onebusaway.vehicletracker.ui.tips.FirstTripDoneViewModel
import org.onebusaway.vehicletracker.ui.tips.RunBoardTipViewModel
import org.onebusaway.vehicletracker.ui.tips.SentCountTipViewModel
import org.onebusaway.vehicletracker.ui.tips.TIP_DELAY_MILLIS
import org.onebusaway.vehicletracker.ui.tips.TipHost
import org.onebusaway.vehicletracker.ui.tips.rememberScreenTip
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger

private const val SERVER = "https://tracker.example.org"
private const val MARIA = "maria@metro.example"
private const val WAIT_MS = 5_000L

/** What each driver has been shown, in memory. */
private class FlagsInMemory : DriverFlagsStore {
    val set: MutableSet<Pair<DriverKey, DriverFlag>> = ConcurrentHashMap.newKeySet()
    override suspend fun isSet(driver: DriverKey, flag: DriverFlag) = (driver to flag) in set
    override suspend fun set(driver: DriverKey, flag: DriverFlag) {
        set += driver to flag
    }
}

/** A driver signed in, who never signs out. */
private class SignedIn(email: String) : SessionStore {
    override val session = MutableStateFlow(Session(SERVER, "jwt", issuedAtEpochSec = 0L, email = email))
    override suspend fun saveLogin(serverUrl: String, email: String, tokens: IssuedTokens) = Unit
    override suspend fun saveRefreshed(tokens: IssuedTokens, spentRefreshToken: String) = Unit
    override suspend fun dropRefreshToken() = Unit
    override suspend fun clearToken() = Unit
    override suspend fun clearTokenAndEmail() = Unit
}

/**
 * Counts how often each of [names] runs, with the lambdas inside it, by the names the Compose compiler gives Android's
 * composition tracing.
 */
@OptIn(InternalComposeTracingApi::class)
private class Compositions(private vararg val names: String) : CompositionTracer {
    private val counts = ConcurrentHashMap<String, AtomicInteger>()

    override fun traceEventStart(key: Int, dirty1: Int, dirty2: Int, info: String) {
        for (name in names) {
            if (info.startsWith("$name ") || info.startsWith("$name.")) counts.getOrPut(name) { AtomicInteger() }.incrementAndGet()
        }
    }

    override fun traceEventEnd() = Unit
    override fun isTraceInProgress() = true

    fun of(name: String) = counts[name]?.get() ?: 0
    fun reset() = counts.clear()
    override fun toString() = names.joinToString { "${it.substringAfterLast('.')}=${of(it)}" }

    fun install() = Composer.setTracer(this)

    companion object {
        fun uninstall() = Composer.setTracer(null)
    }
}

private val pacific = ZoneId.of("America/Los_Angeles")
private fun at(hour: Int, minute: Int): Instant = ZonedDateTime.of(2026, 11, 16, hour, minute, 0, 0, pacific).toInstant()

/** A straight 1 km run north with stops at 0, 500 and 1001 m, scheduled 08:00, 08:05, 08:10. */
private val t1 = TripGeometry(
    tripId = "T1",
    serviceDate = "20261116",
    timezone = pacific,
    shapePoints = listOf(GeoPoint(47.6000, -122.3300), GeoPoint(47.6045, -122.3300), GeoPoint(47.6090, -122.3300)),
    stops = listOf(
        TripStop("ST1", "Stop ST1", 47.6000, -122.3300, 0.0, at(8, 0), at(8, 0)),
        TripStop("ST2", "Stop ST2", 47.6045, -122.3300, 500.0, at(8, 5), at(8, 5)),
        TripStop("ST3", "Stop ST3", 47.6090, -122.3300, 1001.0, at(8, 10), at(8, 10)),
    ),
    thresholds = AdherenceThresholds(maxShapeDistanceM = 60.0, scheduleEarlyS = 900, scheduleLateS = 5400),
    routeShortName = "7",
    routeColor = "FDB71A",
    routeTextColor = "000000",
    headsign = "Downtown Seattle",
)

/** At the first stop, judged from a fix that says the vehicle goes at [speed] m/s. */
private fun atTheFirstStop(speed: Double?, sent: Int = 1) = TrackingState(
    active = true,
    fixesSent = sent,
    geometry = t1,
    adherence = requireNotNull(AdherenceEvaluator.of(t1)).evaluate(
        LocationFix(47.6000, -122.3300, bearing = 0.0, speed = speed, accuracy = 5.0, timeEpochSec = at(8, 0).epochSecond),
        previous = null,
    ),
)

/** The run board and Tracking with their tips over them, laid out as RunsScreen and TrackingScreen lay them out. */
private class TipsOnScreen(private val compose: ComposeContentTestRule) {
    val context = InstrumentationRegistry.getInstrumentation().targetContext!!
    val flags = FlagsInMemory()
    val maria = DriverKey.of(SERVER, MARIA)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val tips = DriverTips(SignedIn(MARIA), flags, scope)

    fun string(id: Int): String = context.getString(id)
    fun close() = scope.cancel()

    // --- The run board ---

    val runTitle get() = string(R.string.tip_run_board_title)
    var tapped: String? = null

    private val board = Board(
        direction = null,
        runs = listOf(
            BoardRun("T1", "6:43 AM", "Downtown Seattle", RunLabel("", ""), past = true, highlight = null),
            BoardRun("T2", "6:58 AM", "Downtown Seattle", RunLabel("", ""), past = false, highlight = RunHighlight.NOW),
            BoardRun("T3", "7:13 AM", "Downtown Seattle", RunLabel("", ""), past = false, highlight = null),
            BoardRun("T4", "7:28 AM", "Downtown Seattle", RunLabel("", ""), past = false, highlight = null),
        ),
        nowAt = 1,
        openAt = 0,
    )

    // Only its being there matters to the screen, which draws the boards.
    private val page = RunPage("20261116", pacific, listOf(TripRun("T1", "Downtown Seattle", 0, "A", "B", Instant.EPOCH, Instant.EPOCH, RunLabel("", ""))))

    fun showRunBoard(tip: RunBoardTipViewModel) {
        compose.setContent {
            AppTheme {
                Surface {
                    val screenTip = rememberScreenTip()
                    Box {
                        RunsScreenContent(
                            state = RunsUiState.Loaded(page, listOf(board), startBoard = 0, startingRunId = null, error = null),
                            route = RouteBadgeModel.of("7", "FDB71A", "000000"),
                            vehicleId = "4317",
                            onBack = {},
                            onBusClick = null,
                            onRouteClick = {},
                            // As RunsScreen does.
                            onRunClick = { runId ->
                                tip.onSeen()
                                tapped = runId
                            },
                            onReload = {},
                            onTryAgain = {},
                            onChangeBus = {},
                            onChooseAnotherRoute = {},
                            onSessionEnded = {},
                            account = {},
                            tip = screenTip,
                        )
                        TipHost(screenTip, runTitle, string(R.string.tip_run_board_text), tip.show, tip::onShown, tip::onLeft, tip::onSeen)
                    }
                }
            }
        }
    }

    // --- Tracking ---

    val sentTitle get() = string(R.string.tip_sent_count_title)
    val tracking = TrackingRepository()

    // Started a moment ago: the trip's time sits beside the sent count, and a long one would leave it no room.
    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261116", startedAtEpochSec = System.currentTimeMillis() / 1000 - 12)
    var mapCompositions = 0

    fun showTracking(tip: SentCountTipViewModel) {
        compose.setContent {
            AppTheme {
                val state by tracking.state.collectAsState()
                val screenTip = rememberScreenTip()
                Box {
                    TrackingScreenContent(
                        state = TrackingUiState(tracking = state, activeTrip = trip),
                        onEndTripClick = {},
                        onEndTripLocallyClick = {},
                        onDismissError = {},
                        onReauthClick = {},
                        tip = screenTip,
                        map = { modifier, _, _ ->
                            SideEffect { mapCompositions++ }
                            Spacer(modifier)
                        },
                    )
                    TipHost(screenTip, sentTitle, string(R.string.tip_sent_count_text), tip.show, tip::onShown, tip::onLeft, tip::onSeen)
                }
            }
        }
    }

    // --- Finding the tip ---

    fun shows(title: String) = compose.onAllNodes(hasText(title)).fetchSemanticsNodes().isNotEmpty()

    fun waitForTip(title: String) = compose.waitUntil(WAIT_MS) { shows(title) }

    fun waitForNoTip(title: String) = compose.waitUntil(WAIT_MS) { !shows(title) }

    /** The tip's own pane, which TalkBack announces as it comes. */
    fun tipPane(title: String) = compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.PaneTitle) and hasAnyDescendant(hasText(title)))

    /**
     * Where [text] itself is on the phone's screen, as drawn: scaled while the tip grows. The tip is a window of its own,
     * so a place in its root says nothing.
     */
    fun onScreen(text: String): Rect = onScreenOf(compose.onNode(hasText(text), useUnmergedTree = true).fetchSemanticsNode())

    /** The clickable row that [text] is in, as TalkBack merges it. */
    fun rowOnScreen(text: String): Rect = onScreenOf(compose.onNode(hasText(text) and hasClickAction()).fetchSemanticsNode())

    /** The whole tip, which its pane holds as it will be once out. */
    fun tipOnScreen(title: String): Rect = onScreenOf(tipPane(title).fetchSemanticsNode())

    private fun onScreenOf(node: SemanticsNode): Rect {
        val topLeft = node.positionOnScreen
        return Rect(topLeft, node.boundsInRoot.size)
    }

    fun seen(flag: DriverFlag) = (maria to flag) in flags.set

    fun waitUntilSeen(flag: DriverFlag) = compose.waitUntil(WAIT_MS) { seen(flag) }
}

/** The two tips and First trip done on screen: when they come, where they point, what closes them, what TalkBack reads. */
@RunWith(AndroidJUnit4::class)
class TipsScreenTest {
    @get:Rule val compose = createComposeRule()

    private val screen = TipsOnScreen(compose)

    @After fun tearDown() {
        Compositions.uninstall()
        screen.close()
    }

    private fun runBoardTip() = RunBoardTipViewModel(screen.tips).also { tip -> compose.waitUntil(WAIT_MS) { tip.show.value } }

    private fun sentCountTip() = SentCountTipViewModel(screen.tips, screen.tracking).also { tip -> compose.waitUntil(WAIT_MS) { tip.show.value } }

    // --- The run board ---

    @Test
    fun theRunBoardTip_comes400MsAfterTheListIsDrawn_pointingAtTheRunMarkedNow() {
        val tip = runBoardTip()
        compose.mainClock.autoAdvance = false
        screen.showRunBoard(tip)

        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS - 100)
        assertFalse("not yet", screen.shows(screen.runTitle))
        compose.mainClock.advanceTimeBy(200)
        compose.mainClock.advanceTimeBy(1_000)
        assertTrue(screen.shows(screen.runTitle))

        val bubble = screen.tipOnScreen(screen.runTitle)
        val now = screen.onScreen("6:58 AM")
        val row = screen.rowOnScreen("6:58 AM")
        assertTrue("above the row of the run marked Now: $bubble over $row", bubble.bottom <= row.top + 1)
        assertTrue("its caret over the run's time", now.center.x in bubble.left..bubble.right)
        assertFalse("shown is not seen", screen.seen(DriverFlag.RunBoardTipSeen))
    }

    @Test
    fun gotIt_closesTheRunBoardTip_forThisDriver() {
        screen.showRunBoard(runBoardTip())
        screen.waitForTip(screen.runTitle)

        compose.onNode(hasText(screen.string(R.string.tip_got_it)) and hasClickAction()).performClick()

        screen.waitForNoTip(screen.runTitle)
        screen.waitUntilSeen(DriverFlag.RunBoardTipSeen)
        assertEquals("Got it starts nothing", null, screen.tapped)
    }

    @Test
    fun aRunTap_closesTheRunBoardTip_andStartsThatRun() {
        screen.showRunBoard(runBoardTip())
        screen.waitForTip(screen.runTitle)

        compose.onNode(hasText("7:13 AM") and hasAnyAncestor(hasClickAction()), useUnmergedTree = true).performClick()

        screen.waitForNoTip(screen.runTitle)
        assertEquals("T3", screen.tapped)
        screen.waitUntilSeen(DriverFlag.RunBoardTipSeen)
    }

    @Test
    fun aDriverWhoHasSeenTheRunBoardTip_isNotShownItAgain() {
        screen.flags.set += screen.maria to DriverFlag.RunBoardTipSeen
        val tip = RunBoardTipViewModel(screen.tips)
        screen.showRunBoard(tip)

        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS + 1_000)
        compose.waitForIdle()
        assertFalse(screen.shows(screen.runTitle))
    }

    @Test
    fun theTip_isOneAnnouncedPane_readAsItsTitleThenItsWordsThenGotIt() {
        screen.showRunBoard(runBoardTip())
        screen.waitForTip(screen.runTitle)
        compose.waitForIdle()

        screen.tipPane(screen.runTitle)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Assertive))
        val title = compose.onNode(hasText(screen.runTitle) and isHeading())
        val words = compose.onNode(hasText(screen.string(R.string.tip_run_board_text)))
        val gotIt = compose.onNode(
            hasText(screen.string(R.string.tip_got_it)) and hasClickAction() and SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button),
        )
        val order = listOf(title, words, gotIt).map { it.fetchSemanticsNode().positionOnScreen.y }
        assertEquals("title, words, Got it, top to bottom", order.sorted(), order)
    }

    @Test
    fun showingAndHidingTheRunBoardTip_recomposesNoneOfTheBoard() {
        val tip = runBoardTip()
        val board = "org.onebusaway.vehicletracker.ui.runs"
        val compositions = Compositions(
            "$board.RunsScreenContent", "$board.Departures", "$board.BoardList", "$board.RunRow",
            "org.onebusaway.vehicletracker.ui.tips.TipBubble",
        ).apply { install() }
        compose.mainClock.autoAdvance = false
        screen.showRunBoard(tip)
        compose.mainClock.advanceTimeBy(100)
        assertTrue("the tracer sees the board drawn: $compositions", compositions.of("$board.RunRow") > 0)

        compositions.reset()
        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS + 1_000)
        assertTrue(screen.shows(screen.runTitle))
        compose.onNode(hasText(screen.string(R.string.tip_got_it)) and hasClickAction()).performClick()
        compose.mainClock.advanceTimeBy(1_000)
        assertFalse(screen.shows(screen.runTitle))

        assertTrue("the tip itself was drawn: $compositions", compositions.of("org.onebusaway.vehicletracker.ui.tips.TipBubble") > 0)
        for (name in listOf("RunsScreenContent", "Departures", "BoardList", "RunRow")) {
            assertEquals("$name recomposed: $compositions", 0, compositions.of("$board.$name"))
        }
    }

    @Test
    fun theTip_growsFromItsCaret_ratherThanFromItsMiddle() {
        val tip = runBoardTip()
        compose.mainClock.autoAdvance = false
        screen.showRunBoard(tip)
        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS)
        while (!screen.shows(screen.runTitle)) compose.mainClock.advanceTimeByFrame()
        val coming = screen.onScreen(screen.runTitle)
        compose.mainClock.advanceTimeBy(1_000)
        val out = screen.onScreen(screen.runTitle)
        val bubble = screen.tipOnScreen(screen.runTitle)
        val anchor = screen.onScreen("6:58 AM")

        // Scaled by s about a point P, every point Q of the tip is drawn at P + s(Q - P): two frames of its title give P.
        val s = coming.width / out.width
        assertTrue("it was still growing: $coming, then $out", s < 0.95f)
        val grownFrom = Offset((coming.left - s * out.left) / (1 - s), (coming.top - s * out.top) / (1 - s))
        assertEquals("from above the run's time: $grownFrom", anchor.center.x, grownFrom.x, 6f)
        assertEquals("from the tip's bottom edge, where its caret is: $grownFrom", bubble.bottom, grownFrom.y, 6f)
    }

    // --- Tracking ---

    @Test
    fun theSentCountTip_showsBeforeTheVehicleMoves_andGoesByItselfAt1MetrePerSecond() {
        val tip = sentCountTip()
        screen.showTracking(tip)
        screen.waitForTip(screen.sentTitle)
        val bubble = screen.tipOnScreen(screen.sentTitle)
        val count = screen.onScreen(screen.context.resources.getQuantityString(R.plurals.tracking_bar_sent, 0, 0))
        val endTrip = screen.onScreen(screen.string(R.string.tracking_end_trip_button))
        assertTrue("over the sent count: $bubble over $count", bubble.bottom <= count.top && count.center.x in bubble.left..bubble.right)
        assertTrue("clear of End trip: $bubble over $endTrip", bubble.bottom <= endTrip.top)

        screen.tracking.update { atTheFirstStop(speed = 0.4) }
        compose.mainClock.advanceTimeBy(1_000)
        assertTrue("standing still, it stays", screen.shows(screen.sentTitle))

        screen.tracking.update { atTheFirstStop(speed = 1.0) }

        screen.waitForNoTip(screen.sentTitle)
        screen.waitUntilSeen(DriverFlag.SentCountTipSeen)
    }

    @Test
    fun theSentCountTip_neverShowsAt1MetrePerSecondOrMore() {
        screen.tracking.update { atTheFirstStop(speed = 1.0) }
        val tip = SentCountTipViewModel(screen.tips, screen.tracking)
        screen.showTracking(tip)

        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS + 2_000)
        compose.waitForIdle()

        assertFalse(screen.shows(screen.sentTitle))
        screen.waitUntilSeen(DriverFlag.SentCountTipSeen)
    }

    @Test
    fun showingAndHidingTheSentCountTip_recomposesNeitherTheMapNorThePanel() {
        val tip = sentCountTip()
        val tracking = "org.onebusaway.vehicletracker.ui.tracking"
        val compositions = Compositions(
            "$tracking.TrackingScreenContent", "$tracking.TripPanel", "$tracking.TripBar", "$tracking.StatusArea",
            "org.onebusaway.vehicletracker.ui.tips.TipBubble",
        ).apply { install() }
        compose.mainClock.autoAdvance = false
        screen.showTracking(tip)
        compose.mainClock.advanceTimeBy(100)
        assertTrue("the tracer sees the panel drawn: $compositions", compositions.of("$tracking.TripPanel") > 0)

        compositions.reset()
        screen.mapCompositions = 0
        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS + 1_000)
        assertTrue(screen.shows(screen.sentTitle))
        compose.onNode(hasText(screen.string(R.string.tip_got_it)) and hasClickAction()).performClick()
        compose.mainClock.advanceTimeBy(1_000)
        assertFalse(screen.shows(screen.sentTitle))

        assertTrue("the tip itself was drawn: $compositions", compositions.of("org.onebusaway.vehicletracker.ui.tips.TipBubble") > 0)
        assertEquals("the map recomposed", 0, screen.mapCompositions)
        for (name in listOf("TrackingScreenContent", "TripPanel", "TripBar", "StatusArea")) {
            assertEquals("$name recomposed: $compositions", 0, compositions.of("$tracking.$name"))
        }
    }

    // --- First trip done ---

    @Test
    fun firstTripDone_waitsForTheStepAfterTheTrip() {
        val done = FirstTripDoneViewModel(screen.tips)
        screen.tips.firstTripEnded(FinishedTrip(screen.maria, 12))
        compose.waitUntil(WAIT_MS) { done.trip.value != null }
        var onAStep by mutableStateOf(false)
        compose.setContent { AppTheme { FirstTripDoneHost(onASetupStep = onAStep, viewModel = done) } }

        compose.mainClock.advanceTimeBy(1_000)
        assertFalse("not over Tracking", screen.shows(screen.string(R.string.first_trip_done_title)))

        onAStep = true
        screen.waitForTip(screen.string(R.string.first_trip_done_title))
        screen.waitUntilSeen(DriverFlag.FirstTripDoneSeen)
    }

    @Test
    fun firstTripDone_readsItsTitle_thenTheCountAsOne_andOkClosesIt() {
        var closed = 0
        compose.setContent { AppTheme { FirstTripDoneSheet(sent = 142, onClose = { closed++ }) } }

        compose.onNode(hasText(screen.string(R.string.first_trip_done_title)) and isHeading()).assertExists()
        compose.onNode(hasText("142") and hasText(screen.context.resources.getQuantityString(R.plurals.first_trip_done_reports, 142)))
            .assertExists()
        compose.onNode(hasText(screen.string(R.string.first_trip_done_text))).assertExists()
        compose.onNode(hasText(screen.string(R.string.first_trip_done_ok)) and hasClickAction()).performClick()

        compose.waitUntil(WAIT_MS) { closed == 1 }
    }

    @Test
    fun firstTripDone_saysReportInTheSingular_forOne() {
        compose.setContent { AppTheme { FirstTripDoneSheet(sent = 1, onClose = {}) } }

        compose.onNode(hasText("1") and hasText(screen.context.resources.getQuantityString(R.plurals.first_trip_done_reports, 1))).assertExists()
        assertEquals("report sent", screen.context.resources.getQuantityString(R.plurals.first_trip_done_reports, 1))
    }
}

/** The tips with animations removed on the phone, which Compose reports as a motion duration scale of 0. */
@RunWith(AndroidJUnit4::class)
class TipsStillTest {
    @get:Rule val compose = createComposeRule(
        effectContext = object : MotionDurationScale {
            override val scaleFactor = 0f
        },
    )

    private val screen = TipsOnScreen(compose)

    @After fun tearDown() = screen.close()

    @Test
    fun theTip_justAppears_atItsFullSize() {
        val tip = RunBoardTipViewModel(screen.tips).also { tip -> compose.waitUntil(WAIT_MS) { tip.show.value } }
        compose.mainClock.autoAdvance = false
        screen.showRunBoard(tip)
        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS)
        while (!screen.shows(screen.runTitle)) compose.mainClock.advanceTimeByFrame()
        val first = screen.onScreen(screen.runTitle)
        compose.mainClock.advanceTimeBy(1_000)
        val settled = screen.onScreen(screen.runTitle)

        assertEquals("no growing", settled.width, first.width, 0.5f)
        assertEquals(settled.top, first.top, 0.5f)
    }

    @Test
    fun gotIt_takesTheTipAwayAtOnce() {
        val tip = RunBoardTipViewModel(screen.tips).also { tip -> compose.waitUntil(WAIT_MS) { tip.show.value } }
        screen.showRunBoard(tip)
        screen.waitForTip(screen.runTitle)
        compose.mainClock.autoAdvance = false

        compose.onNode(hasText(screen.string(R.string.tip_got_it)) and hasClickAction()).performClick()
        compose.mainClock.advanceTimeByFrame()
        compose.mainClock.advanceTimeByFrame()

        assertFalse(screen.shows(screen.runTitle))
    }
}
