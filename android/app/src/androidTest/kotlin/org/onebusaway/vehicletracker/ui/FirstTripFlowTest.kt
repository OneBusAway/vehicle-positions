package org.onebusaway.vehicletracker.ui

import android.content.Intent
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.datastore.preferences.core.edit
import androidx.lifecycle.createSavedStateHandle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.MainActivity
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
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.WaitingReports
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.driverFlagsDataStore
import org.onebusaway.vehicletracker.data.map.MapFileSource
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.data.vehiclePrefsDataStore
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.service.LocationTrackingService
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.runs.RunsScreen
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tips.DriverTips
import org.onebusaway.vehicletracker.ui.tips.FirstTripDoneHost
import org.onebusaway.vehicletracker.ui.tips.FirstTripDoneViewModel
import org.onebusaway.vehicletracker.ui.tips.RunBoardTipViewModel
import org.onebusaway.vehicletracker.ui.tips.SentCountTipViewModel
import org.onebusaway.vehicletracker.ui.tips.TIP_DELAY_MILLIS
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreen
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import java.io.File
import java.time.ZoneId
import java.util.concurrent.atomic.AtomicInteger

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val MARIA = "maria@metro.example"
private const val NEXT_STEP = "The step after a trip, as this test stands it in"

private const val RUNS_JSON =
    """{"route_id":"R1","service_date":"20260922","timezone":"America/Los_Angeles","trips":[""" +
        """{"id":"T1","headsign":"North","direction_id":0,"starts_at":"2026-09-22T08:00:00-07:00",""" +
        """"ends_at":"2026-09-22T08:10:00-07:00","first_stop":"Stop ST1","last_stop":"Stop ST3"}]}"""

private const val T1_JSON =
    """{"id":"T1","route_id":"R1","headsign":"North","direction_id":0,"service_date":"20260922",""" +
        """"timezone":"America/Los_Angeles","route":{"short_name":"1","long_name":"Straight","color":"0077C0",""" +
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

/** Android's side of a trip, which this test stands in for: no service, and so no fixes but the test's own. */
private object NoReportsService : ServiceController {
    override fun startTracking() = Unit
    override fun stopTracking() = Unit
    override fun askToResume() = Unit
}

private object NoBacklog : BacklogUploadScheduler {
    override fun schedule() = Unit
    override fun scheduleAfresh() = Unit
}

private object NoStreetMap : MapFileSource {
    override val state: StateFlow<MapFileState> = MutableStateFlow(MapFileState.None)
}

/**
 * A driver's first trip, from the run board's tip to "First trip done", with the app's own screens, view models and
 * stores and a server on this phone; then their second, which shows none of it. The test hands Tracking the fixes the
 * tracking service would, since CI's emulator has no Play services to give it any, and stands past the permission
 * checklist, which it has no part in.
 */
@RunWith(AndroidJUnit4::class)
class FirstTripFlowTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val starts = AtomicInteger()
    private val ends = AtomicInteger()
    private val server = HoldingServer(held = "/nothing-is-held") { path ->
        when {
            path.endsWith("/api/v1/gtfs/routes/R1/trips") -> "200 OK" to RUNS_JSON
            path.endsWith("/api/v1/gtfs/trips/T1") -> "200 OK" to T1_JSON
            path.endsWith("/api/v1/trips/start") -> "201 Created".also { starts.incrementAndGet() } to STARTED_JSON
            path.endsWith("/api/v1/trips/end") -> "200 OK".also { ends.incrementAndGet() } to "{}"
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }

    private val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
    private val flags = DataStoreDriverFlagsStore(context.driverFlagsDataStore)
    private val trips = DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault())
    private val geometryFile = File(context.cacheDir, "first-trip-flow-geometry.json")
    private val queueFile = File(context.cacheDir, "first-trip-flow-queue.jsonl")
    private val geometry = FileTripGeometryStore(geometryFile)
    private val tracking = TrackingRepository()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val tips = DriverTips(sessions, flags, scope)
    private val api = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url) }
    private val clock = { System.currentTimeMillis() / 1000 }
    private val tripRepository = TripRepository(api, trips, DataStoreVehiclePrefsStore(context.vehiclePrefsDataStore), sessions, clock)
    private val maria get() = DriverKey.of(server.url, MARIA)

    @Volatile private var runs: RunsViewModel? = null
    private var drives = 0

    private val runsFactory = viewModelFactory {
        initializer {
            RunsViewModel(
                createSavedStateHandle(),
                CatalogRepository(api),
                tripRepository,
                geometry,
                NoReportsService,
                AuthRepository(sessions, trips, ApiFactory { null }, api, FileOfflineFixQueue(queueFile), NoBacklog, clock),
                clock,
            ).also { runs = it }
        }
    }

    @Before fun signIn() = runBlocking {
        forgetAll()
        sessions.saveLogin(server.url, MARIA, IssuedTokens("access", "refresh", accessExpiresAtEpochSec = Long.MAX_VALUE / 2, issuedAtEpochSec = 0L))
    }

    @After fun tearDown() = runBlocking {
        server.close()
        scope.cancel()
        context.stopService(Intent(context, LocationTrackingService::class.java))
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

    /** The run step, Tracking and the step after, wired as AppNav wires them, with "First trip done" over them. */
    @Composable
    private fun FirstTrip() {
        val navController = rememberNavController()
        AppTheme {
            Surface(Modifier.fillMaxSize()) {
                NavHost(navController, startDestination = "runs") {
                    composable(
                        "runs",
                        arguments = listOf(
                            navArgument(ARG_VEHICLE_ID) { defaultValue = "4317" },
                            navArgument(ARG_ROUTE_ID) { defaultValue = "R1" },
                        ),
                    ) {
                        RunsScreen(
                            onTripStarted = { navController.navigate("tracking") { popUpTo(0) { inclusive = true } } },
                            onBack = {},
                            onBusClick = null,
                            onRouteClick = {},
                            onChangeBus = {},
                            onChooseAnotherRoute = {},
                            onSessionEnded = {},
                            onSignedOut = {},
                            viewModel = viewModel(factory = runsFactory),
                            tipViewModel = viewModel { RunBoardTipViewModel(tips) },
                        )
                    }
                    composable("tracking") {
                        TrackingScreen(
                            onTripEnded = { navController.navigate("next") { popUpTo(0) { inclusive = true } } },
                            onReauthRequired = {},
                            viewModel = viewModel {
                                val ender = TripEnder(tripRepository, trips, geometry, NoReportsService, sessions)
                                TrackingViewModel(tracking, trips, ender, NoStreetMap, WaitingReports(FileOfflineFixQueue(queueFile), sessions))
                            },
                            sentCountTip = viewModel { SentCountTipViewModel(tips, tracking) },
                        )
                    }
                    composable("next") { Text(NEXT_STEP) }
                }
                val entry by navController.currentBackStackEntryAsState()
                FirstTripDoneHost(onASetupStep = entry?.destination?.route in setOf("runs", "next"), viewModel = viewModel { FirstTripDoneViewModel(tips) })
            }
        }
    }

    private fun string(id: Int) = context.getString(id)

    private fun shows(text: String) = compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty()

    private fun waitFor(text: String) = compose.waitUntil(TIMEOUT_MS) { shows(text) }

    private fun waitForNo(text: String) = compose.waitUntil(TIMEOUT_MS) { !shows(text) }

    private fun seen(flag: DriverFlag) = runBlocking { flags.isSet(maria, flag) }

    private fun waitUntilSeen(flag: DriverFlag) = compose.waitUntil(TIMEOUT_MS) { seen(flag) }

    private fun tap(text: String) {
        val button = hasText(text) and hasClickAction()
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(button).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(button).performClick()
    }

    /** As the tracking service hands Tracking each fix: here one that has no route judged, at [speed] m/s. */
    private fun aFix(speed: Double, sent: Int) {
        val trip = checkNotNull(runBlocking { geometry.load() }) { "the run's shape was saved as it started" }
        val adherence = AdherenceEvaluator.of(trip)?.evaluate(LocationFix(47.6, -122.33, null, speed, 5.0, clock()), previous = null)
        tracking.update { TrackingState(active = true, fixesSent = sent, adherence = adherence) }
    }

    /** From the run board to the step after the trip. [onTheRunBoard] runs before the run is tapped, [during] on Tracking. */
    private fun drive(scenario: ActivityScenario<MainActivity>, onTheRunBoard: () -> Unit, during: () -> Unit) {
        // A new key for each trip: setContent on the same activity keeps what the last one remembered, its screen too.
        val drive = ++drives
        scenario.onActivity { it.setContent { key(drive) { FirstTrip() } } }
        compose.waitUntil(TIMEOUT_MS) { runs?.uiState?.value is RunsUiState.Loaded }
        onTheRunBoard()
        tap("North")
        // Past the checklist, when it shows: the test has no part in it.
        compose.waitForIdle()
        scenario.onActivity { checkNotNull(runs).onStartRun("T1") }
        waitFor(string(R.string.tracking_end_trip_button))
        during()
        tap(string(R.string.tracking_end_trip_button))
        // The dialog's button has the bar's words.
        val confirm = hasText(string(R.string.tracking_end_trip_dialog_confirm)) and hasClickAction() and hasAnyAncestor(isDialog())
        compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(confirm).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(confirm).performClick()
        waitFor(NEXT_STEP)
    }

    @Test
    fun aFirstTrip_showsEachTipOnce_andEndsOnFirstTripDone_whichTheSecondTripDoesNot() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            drive(scenario, onTheRunBoard = { waitFor(string(R.string.tip_run_board_title)) }) {
                assertTrue("a run tap closed the run board's tip", seen(DriverFlag.RunBoardTipSeen))
                assertFalse(shows(string(R.string.tip_run_board_title)))
                waitFor(string(R.string.tip_sent_count_title))

                aFix(speed = 0.0, sent = 1)
                aFix(speed = 0.4, sent = 2)
                compose.waitForIdle()
                assertTrue("standing at the first stop, it stays", shows(string(R.string.tip_sent_count_title)))
                aFix(speed = 6.0, sent = 3)
                waitForNo(string(R.string.tip_sent_count_title))
                waitUntilSeen(DriverFlag.SentCountTipSeen)
            }

            waitFor(string(R.string.first_trip_done_title))
            compose.onNode(hasText(string(R.string.first_trip_done_title)) and isHeading()).assertExists()
            compose.onNode(hasText("3") and hasText(context.resources.getQuantityString(R.plurals.first_trip_done_reports, 3))).assertExists()
            waitUntilSeen(DriverFlag.FirstTripDoneSeen)
            tap(string(R.string.first_trip_done_ok))
            waitForNo(string(R.string.first_trip_done_title))
            assertEquals(1, ends.get())

            val pastTheTipsDelay = TIP_DELAY_MILLIS + 600
            drive(
                scenario,
                onTheRunBoard = {
                    compose.mainClock.advanceTimeBy(pastTheTipsDelay)
                    assertFalse("no tip on the run board the second time", shows(string(R.string.tip_run_board_title)))
                },
            ) {
                aFix(speed = 0.0, sent = 5)
                compose.mainClock.advanceTimeBy(pastTheTipsDelay)
                assertFalse("nor on Tracking", shows(string(R.string.tip_sent_count_title)))
            }
            compose.mainClock.advanceTimeBy(pastTheTipsDelay)
            assertFalse("nor First trip done", shows(string(R.string.first_trip_done_title)))
            assertEquals(2, starts.get())
        }
    }
}
