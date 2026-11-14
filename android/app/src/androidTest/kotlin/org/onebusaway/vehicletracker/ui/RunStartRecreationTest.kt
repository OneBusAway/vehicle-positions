package org.onebusaway.vehicletracker.ui

import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.datastore.preferences.core.edit
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.createSavedStateHandle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.CatalogRepository
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.DataStoreVehiclePrefsStore
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.FileTripGeometryStore
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.data.vehiclePrefsDataStore
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.ui.runs.RunsScreen
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.RunsViewModel
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.io.File
import java.time.ZoneId
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

private const val TIMEOUT_MS = HOLD_TIMEOUT_SEC * 1000
private const val TRACKING = "Tracking, as this test stands it in"

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

/** Android starting the tracking service, or refusing to while [refusing], as from Android 12 with the phone locked. */
private class AndroidsAnswer : ServiceController {
    @Volatile var refusing = true
    val refused = CountDownLatch(1)
    val starts = AtomicInteger()

    override fun startTracking() {
        if (refusing) {
            refused.countDown()
            throw IllegalStateException("startForegroundService() not allowed due to mAllowStartForeground false")
        }
        starts.incrementAndGet()
    }

    override fun stopTracking() = Unit
    override fun askToResume() = Unit
}

/**
 * Starting a run when the server confirms the trip with the phone locked, so Android refuses to start its reports, and
 * the activity is recreated before the driver comes back, as a dark-mode switch at dusk does. The run step is the
 * app's, in the app's activity, on a view model that answers for Android with [AndroidsAnswer]; it goes on to a
 * stand-in for Tracking.
 */
@RunWith(AndroidJUnit4::class)
class RunStartRecreationTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val server = HoldingServer(held = "/api/v1/trips/start") { path ->
        when {
            path.endsWith("/api/v1/gtfs/routes/R1/trips") -> "200 OK" to RUNS_JSON
            path.endsWith("/api/v1/gtfs/trips/T1") -> "200 OK" to T1_JSON
            path.endsWith("/api/v1/trips/start") -> "201 Created" to STARTED_JSON
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
    }
    private val android = AndroidsAnswer()
    private val geometryFile = File(context.cacheDir, "run-start-recreation-geometry.json")
    private val queueFile = File(context.cacheDir, "run-start-recreation-queue.jsonl")

    @Volatile private var runs: RunsViewModel? = null

    private val runsFactory = viewModelFactory {
        initializer {
            val api = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url) }
            val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
            val trips = DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault())
            val clock = { System.currentTimeMillis() / 1000 }
            val noBacklog = object : BacklogUploadScheduler {
                override fun schedule() = Unit
                override fun scheduleAfresh() = Unit
            }
            RunsViewModel(
                createSavedStateHandle(),
                CatalogRepository(api),
                TripRepository(api, trips, DataStoreVehiclePrefsStore(context.vehiclePrefsDataStore), sessions, clock),
                FileTripGeometryStore(geometryFile),
                android,
                AuthRepository(sessions, trips, ApiFactory { null }, api, FileOfflineFixQueue(queueFile), noBacklog, clock),
                clock,
            ).also { runs = it }
        }
    }

    @Before fun setUp() = runBlocking { forgetAll() }

    @After fun tearDown() = runBlocking {
        server.close()
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
        context.vehiclePrefsDataStore.edit { it.clear() }
        geometryFile.delete()
        queueFile.delete()
    }

    /** The run step as AppNav hosts it, going on to Tracking the same way. */
    @Composable
    private fun RunStep() {
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
                        )
                    }
                    composable("tracking") { Text(TRACKING) }
                }
            }
        }
    }

    /** In place of the app's own screens, in this activity and in each one that replaces it. */
    private fun showTheRunStep(scenario: ActivityScenario<MainActivity>) = scenario.onActivity { it.setContent { RunStep() } }

    @Test
    fun aStartConfirmedWhileLocked_goesOnToTrackingOnceTheRecreatedStepIsBack() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            showTheRunStep(scenario)
            compose.waitUntil(TIMEOUT_MS) { runs?.uiState?.value is RunsUiState.Loaded }
            // The run tapped, past the checklist, which the test has no part in.
            scenario.onActivity { checkNotNull(runs).onStartRun("T1") }
            assertTrue("the start reached the server", server.heldArrived.await(HOLD_TIMEOUT_SEC, TimeUnit.SECONDS))

            // The phone locks, and then the server confirms the trip: Android refuses to start its reports.
            scenario.moveToState(Lifecycle.State.CREATED)
            server.release()
            assertTrue("Android was asked to start the reports", android.refused.await(HOLD_TIMEOUT_SEC, TimeUnit.SECONDS))

            scenario.recreate()
            showTheRunStep(scenario)
            android.refusing = false
            // The driver comes back.
            scenario.moveToState(Lifecycle.State.RESUMED)

            compose.waitUntil(TIMEOUT_MS) { compose.onAllNodes(hasText(TRACKING)).fetchSemanticsNodes().isNotEmpty() }
            assertEquals("the reports started once", 1, android.starts.get())
        }
    }
}
