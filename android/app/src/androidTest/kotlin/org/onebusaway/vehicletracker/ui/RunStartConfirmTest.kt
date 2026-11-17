package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.performClick
import androidx.datastore.preferences.core.edit
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.viewmodel.compose.LocalViewModelStoreOwner
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.DataStoreDriverFlagsStore
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.DataStoreVehiclePrefsStore
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.FileTripGeometryStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.driverFlagsDataStore
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.data.vehiclePrefsDataStore
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.account.AccountViewModel
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.RunsScreen
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import org.onebusaway.vehicletracker.ui.runs.StartRunDialog
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tips.DriverTips
import org.onebusaway.vehicletracker.ui.tips.RunBoardTipViewModel
import org.onebusaway.vehicletracker.ui.tips.TIP_DELAY_MILLIS
import java.io.File
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.temporal.ChronoUnit
import java.util.concurrent.CopyOnWriteArrayList

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val MARIA = "maria@metro.example"
private const val RUNS_PATH = "/api/v1/gtfs/routes/R1/trips"
private const val MAP_PATH = "/api/v1/map"

private const val T1_JSON =
    """{"id":"T1","route_id":"R1","headsign":"North","direction_id":0,"service_date":"20260922",""" +
        """"timezone":"America/Los_Angeles","route":{"short_name":"7","long_name":"Straight","color":"0077C0",""" +
        """"text_color":"FFFFFF"},"shape":{"length_m":1001.2,"points":[[47.6,-122.33],[47.6045,-122.33],""" +
        """[47.609,-122.33]],"from_stops":false},"stops":[{"id":"ST1","name":"Stop ST1","sequence":1,""" +
        """"lat":47.6,"lon":-122.33,"along_shape_m":0,"arrival_at":"2026-09-22T08:00:00-07:00",""" +
        """"departure_at":"2026-09-22T08:00:00-07:00"},{"id":"ST3","name":"Stop ST3","sequence":3,"lat":47.609,""" +
        """"lon":-122.33,"along_shape_m":1001.2,"arrival_at":"2026-09-22T08:10:00-07:00",""" +
        """"departure_at":"2026-09-22T08:10:00-07:00"}],"thresholds":{"max_shape_distance_m":60,""" +
        """"schedule_early_s":900,"schedule_late_s":5400}}"""

private const val STARTED_JSON =
    """{"id":7,"user_id":1,"vehicle_id":"4317","route_id":"R1","gtfs_trip_id":"T1",""" +
        """"start_time":"2026-09-22T15:00:00Z","status":"active"}"""

/**
 * The run board asking before a run starts, as iOS does: the app's run step and view models against a server on this
 * phone that notes every request. The phone has not allowed everything a trip needs, as CI's emulator has not, so a
 * confirmed run goes on to the checklist before anything reaches the server.
 */
@RunWith(AndroidJUnit4::class)
class RunStartConfirmTest {
    @get:Rule val compose = createComposeRule()
    private val restoration = StateRestorationTester(compose)

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    // Two runs ahead of now, so the first is marked Next and the board opens on it, with the tip on it.
    private val zone = ZoneId.of("America/Los_Angeles")
    private val firstStart = ZonedDateTime.now(zone).plusMinutes(10).truncatedTo(ChronoUnit.MINUTES)
    private val secondStart = firstStart.plusMinutes(30)

    // As the board writes a start, in the driver's own clock format.
    private val timeFormat = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)
    private val firstTime = timeFormat.format(firstStart.toLocalTime())
    private val secondTime = timeFormat.format(secondStart.toLocalTime())

    private val requests = CopyOnWriteArrayList<String>()
    private val server = HoldingServer(held = "/nothing-is-held") { path ->
        requests += path
        when {
            path.endsWith(RUNS_PATH) -> "200 OK" to runsJson()
            path.endsWith("/api/v1/gtfs/trips/T1") -> "200 OK" to T1_JSON
            path.endsWith("/api/v1/trips/start") -> "201 Created" to STARTED_JSON
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    /** As Go writes a time: RFC 3339 with the zone's offset. */
    private fun wire(time: ZonedDateTime) = time.format(DateTimeFormatter.ISO_OFFSET_DATE_TIME)

    private fun runsJson(): String {
        fun run(id: String, start: ZonedDateTime) =
            """{"id":"$id","headsign":"North","direction_id":0,"starts_at":"${wire(start)}",""" +
                """"ends_at":"${wire(start.plusMinutes(10))}","first_stop":"Stop ST1","last_stop":"Stop ST3"}"""
        return """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[""" +
            "${run("T1", firstStart)},${run("T2", secondStart)}]}"
    }

    private val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
    private val flags = DataStoreDriverFlagsStore(context.driverFlagsDataStore)
    private val trips = DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault())
    private val geometryFile = File(context.cacheDir, "run-start-confirm-geometry.json")
    private val queueFile = File(context.cacheDir, "run-start-confirm-queue.jsonl")
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val api = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url) }
    private val clock = { System.currentTimeMillis() / 1000 }
    /** Android's side of a trip, which no test here reaches: a start would show in the server's requests first. */
    private val noReports = object : ServiceController {
        override fun startTracking() = Unit
        override fun stopTracking() = Unit
        override fun askToResume() = Unit
    }
    private val noBacklog = object : BacklogUploadScheduler {
        override fun schedule() = Unit
        override fun scheduleAfresh() = Unit
    }
    private val tripRepository = TripRepository(api, trips, DataStoreVehiclePrefsStore(context.vehiclePrefsDataStore), sessions, clock)
    private val auth = AuthRepository(sessions, trips, ApiFactory { null }, api, FileOfflineFixQueue(queueFile), noBacklog, clock)

    /**
     * Not a Hilt screen, so the run step's view models are made here. The account menu asks Hilt for its own; outside
     * a navigation entry Hilt takes it from this owner's store, which already has it.
     */
    private val owner = object : ViewModelStoreOwner {
        override val viewModelStore = ViewModelStore()
    }
    private lateinit var runs: RunsViewModel
    private lateinit var tip: RunBoardTipViewModel

    @Before fun setUp() = runBlocking {
        forgetAll()
        sessions.saveLogin(server.url, MARIA, IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L))
        val factory = viewModelFactory {
            initializer { AccountViewModel(sessions, auth, tripRepository) }
            initializer {
                val arguments = mapOf(ARG_VEHICLE_ID to "4317", ARG_ROUTE_ID to "R1", ARG_ROUTE_NAME to "7")
                RunsViewModel(SavedStateHandle(arguments), CatalogRepository(api), tripRepository, FileTripGeometryStore(geometryFile), noReports, auth, clock)
            }
            initializer { RunBoardTipViewModel(DriverTips(sessions, flags, scope)) }
        }
        val provider = ViewModelProvider.create(owner, factory)
        provider[AccountViewModel::class]
        runs = provider[RunsViewModel::class]
        tip = provider[RunBoardTipViewModel::class]
    }

    @After fun tearDown() = runBlocking {
        owner.viewModelStore.clear()
        server.close()
        scope.cancel()
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
        context.vehiclePrefsDataStore.edit { it.clear() }
        context.driverFlagsDataStore.edit { it.clear() }
        geometryFile.delete()
        queueFile.delete()
    }

    private fun string(id: Int) = context.getString(id)

    private val title get() = context.getString(R.string.runs_start_title, firstTime)
    private val message get() = context.getString(R.string.runs_start_message, "7", "North", "4317")
    private val inDialog = hasAnyAncestor(isDialog())

    /** The run step as AppNav hosts it, on a board the server has answered. */
    private fun showRunBoard() {
        restoration.setContent {
            CompositionLocalProvider(LocalViewModelStoreOwner provides owner) {
                AppTheme {
                    Surface(Modifier.fillMaxSize()) {
                        RunsScreen(
                            onTripStarted = {},
                            onBack = {},
                            onBusClick = null,
                            onRouteClick = {},
                            onChangeBus = {},
                            onChooseAnotherRoute = {},
                            onSessionEnded = {},
                            onSignedOut = {},
                            viewModel = runs,
                            tipViewModel = tip,
                        )
                    }
                }
            }
        }
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(hasText(firstTime) and hasClickAction()).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty()

    private fun tapRun(time: String) = compose.onNode(hasText(time) and hasClickAction()).performClick()

    private fun tapInDialog(button: Int) = compose.onNode(hasText(string(button)) and hasClickAction() and inDialog).performClick()

    /** Every place TalkBack could read [words] from: a node's text or description, or the title a pane is announced by. */
    private fun mentions(words: String) = hasText(words, substring = true) or hasContentDescription(words, substring = true) or
        SemanticsMatcher("a pane titled with \"$words\"") { it.config.getOrNull(SemanticsProperties.PaneTitle)?.contains(words) == true }

    private fun assertStartedNothing() {
        assertNull("no start in flight", (runs.uiState.value as RunsUiState.Loaded).startingRunId)
        assertEquals("no /trips/start", 0, requests.count { it.endsWith("/api/v1/trips/start") })
        // Not the map: ResumeEndTest signs in to a server that refuses, so MapRepository keeps retrying in the app's
        // scope after it ends, and its next try asks whichever server is signed in then, which can be this one.
        assertEquals("only the runs were asked for", listOf(RUNS_PATH), requests.filterNot { it.endsWith(MAP_PATH) })
    }

    @Test
    fun aTapAsks_withTheStartTimeAsTheBoardShowsIt_andTheRouteHeadsignAndVehicle_eachOnce() {
        showRunBoard()
        tapRun(firstTime)

        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()
        compose.onNode(hasText(message) and inDialog).assertIsDisplayed()
        assertEquals("the title is read once", 1, compose.onAllNodes(mentions(title), useUnmergedTree = true).fetchSemanticsNodes().size)
        assertEquals("the details are read once", 1, compose.onAllNodes(mentions(message), useUnmergedTree = true).fetchSemanticsNodes().size)
        assertFalse("no checklist before the driver answers", shows(string(R.string.preflight_title)))
        assertStartedNothing()
    }

    @Test
    fun cancel_closesTheDialog_andStartsNothing() {
        showRunBoard()
        tapRun(firstTime)
        tapInDialog(R.string.runs_start_cancel)

        compose.waitForIdle()
        assertFalse(shows(title))
        assertFalse(shows(string(R.string.preflight_title)))
        assertStartedNothing()

        // Asked again on the next tap.
        tapRun(firstTime)
        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()
    }

    @Test
    fun back_closesTheDialog_andStartsNothing() {
        showRunBoard()
        tapRun(firstTime)
        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()

        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)

        compose.waitForIdle()
        assertFalse(shows(title))
        assertFalse(shows(string(R.string.preflight_title)))
        assertStartedNothing()
    }

    @Test
    fun startTrip_closesTheDialog_andOpensTheChecklist() {
        showRunBoard()
        tapRun(firstTime)
        tapInDialog(R.string.runs_start_confirm)

        compose.waitUntil(TIMEOUT_MS) { shows(string(R.string.preflight_title)) }
        assertFalse(shows(title))
        assertStartedNothing()
    }

    @Test
    fun theDialogStaysThroughARotation_andStartsTheSameRun() {
        showRunBoard()
        tapRun(firstTime)
        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()

        restoration.emulateSavedInstanceStateRestore()

        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()
        compose.onNode(hasText(message) and inDialog).assertIsDisplayed()
        tapInDialog(R.string.runs_start_confirm)
        compose.waitUntil(TIMEOUT_MS) { shows(string(R.string.preflight_title)) }
    }

    @Test
    fun aSecondTapWhileTheDialogIsUp_keepsItOnTheFirstRun() {
        showRunBoard()
        tapRun(firstTime)
        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()

        // A tap that reaches the board as the dialog opens, as the second of a double tap can.
        tapRun(secondTime)

        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()
        assertFalse(shows(context.getString(R.string.runs_start_title, secondTime)))
        assertEquals(1, compose.onAllNodes(isDialog()).fetchSemanticsNodes().size)
        assertStartedNothing()
    }

    @Test
    fun theRunBoardsTip_goesAsTheDialogOpens_andStaysAway() {
        val tipTitle = string(R.string.tip_run_board_title)
        showRunBoard()
        compose.waitUntil(TIMEOUT_MS) { shows(tipTitle) }

        tapRun(firstTime)

        compose.onNode(hasText(title) and inDialog).assertIsDisplayed()
        compose.waitForIdle()
        assertFalse("the tip closed as the dialog opened", shows(tipTitle))
        compose.mainClock.advanceTimeBy(TIP_DELAY_MILLIS * 3)
        assertFalse("and does not come back over it", shows(tipTitle))
        compose.waitUntil(TIMEOUT_MS) { runBlocking { flags.isSet(DriverKey.of(server.url, MARIA), DriverFlag.RunBoardTipSeen) } }
    }

    @Test
    fun theDetails_leaveOutWhatTheFeedDidNotGive() {
        var route by mutableStateOf("7")
        var headsign by mutableStateOf("North")
        compose.setContent {
            AppTheme {
                StartRunDialog(
                    run = BoardRun("T1", "6:43 AM", headsign, RunLabel("", ""), past = false, highlight = null),
                    routeName = route,
                    vehicleId = "4317",
                    onStart = {},
                    onCancel = {},
                )
            }
        }
        fun assertSays(text: String) = compose.onNode(hasText(text) and inDialog).assertIsDisplayed()

        assertSays(context.getString(R.string.runs_start_title, "6:43 AM"))
        assertSays("Route 7 to North on vehicle 4317.")
        headsign = ""
        assertSays("Route 7 on vehicle 4317.")
        route = ""
        assertSays("On vehicle 4317.")
        headsign = "North"
        assertSays("To North on vehicle 4317.")
    }
}
