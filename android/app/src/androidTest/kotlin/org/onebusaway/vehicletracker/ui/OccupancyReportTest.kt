package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.datastore.preferences.core.edit
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.DataStoreVehiclePrefsStore
import org.onebusaway.vehicletracker.data.EncryptedSessionStore
import org.onebusaway.vehicletracker.data.FileOfflineFixQueue
import org.onebusaway.vehicletracker.data.FileTripGeometryStore
import org.onebusaway.vehicletracker.data.KeystoreCryptor
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripRepository
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.map.MapFileSource
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import org.onebusaway.vehicletracker.data.vehiclePrefsDataStore
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import org.onebusaway.vehicletracker.service.HistoryUploader
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.service.REPORT_INTERVAL_MS
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.service.TripReporter
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreen
import org.onebusaway.vehicletracker.ui.tracking.TrackingViewModel
import org.onebusaway.vehicletracker.ui.tracking.TripEnder
import java.io.Closeable
import java.io.File
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.time.ZoneId
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

private const val TIMEOUT_SEC = 15L

/**
 * Tapping Full on the Tracking screen, with the app's own trip store, reporter and HTTP client, and a server on this
 * phone. The reporter is handed each fix as the tracking service hands it one: CI's emulator has no Play services to
 * give the service fixes of its own.
 */
@RunWith(AndroidJUnit4::class)
class OccupancyReportTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val server = ReportServer()
    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261114", startedAtEpochSec = 1_763_100_000L, serverUrl = server.url)
    private val trips = DataStoreTripStateStore(context.tripStateDataStore, ZoneId.systemDefault())

    @Before fun storeTheTrip() = runBlocking {
        forgetAll()
        trips.saveActiveTrip(trip)
    }

    @After fun tearDown() = runBlocking {
        server.close()
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
    }

    @Test
    fun tappingFull_sendsFullWithTheNextReport() {
        val api = TrackerApiProvider { ApiFactory { "jwt" }.create(server.url) }
        val sessions = EncryptedSessionStore(context.sessionDataStore, KeystoreCryptor())
        val queue = FileOfflineFixQueue(File(context.cacheDir, "occupancy-test-queue").apply { delete() })
        var elapsedMs = 0L
        val reporter = TripReporter(
            api, TrackingRepository(), { elapsedMs += REPORT_INTERVAL_MS; elapsedMs }, queue, HistoryUploader(api, sessions, queue), NoBacklogJob, trips,
        )
        val tripRepository = TripRepository(api, trips, DataStoreVehiclePrefsStore(context.vehiclePrefsDataStore), sessions, clock = { 0L })
        val geometry = FileTripGeometryStore(File(context.cacheDir, "occupancy-test-geometry"))
        val viewModel = TrackingViewModel(TrackingRepository(), trips, TripEnder(tripRepository, trips, geometry, NoService, sessions), NoMapFile)
        compose.setContent { TrackingScreen(onTripEnded = {}, onReauthRequired = {}, viewModel = viewModel) }
        compose.waitUntil(TIMEOUT_SEC * 1000) { viewModel.uiState.value.activeTrip != null }

        assertFalse("nothing chosen yet", send(reporter).containsKey("occupancy_status"))

        compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Expand)).performSemanticsAction(SemanticsActions.Expand)
        val full = compose.onNode(hasText(context.getString(R.string.tracking_occupancy_full)) and hasClickAction())
        full.performClick()
        compose.waitUntil(TIMEOUT_SEC * 1000) { full.fetchSemanticsNode().config.getOrNull(SemanticsProperties.Selected) == true }

        assertEquals("FULL", send(reporter)["occupancy_status"]?.jsonPrimitive?.content)
    }

    /** Hands [reporter] a fix, as the tracking service does with each one, and returns the report the server got. */
    private fun send(reporter: TripReporter): JsonObject {
        val now = System.currentTimeMillis() / 1000
        runBlocking { reporter.report(trip, LocationFix(47.6, -122.33, bearing = null, speed = null, accuracy = 5.0, timeEpochSec = now)) }
        val body = requireNotNull(server.reports.poll(TIMEOUT_SEC, TimeUnit.SECONDS)) { "no report reached the server" }
        return Json.parseToJsonElement(body).jsonObject
    }
}

private object NoBacklogJob : BacklogUploadScheduler {
    override fun schedule() = Unit
    override fun scheduleAfresh() = Unit
}

private object NoService : ServiceController {
    override fun startTracking() = Unit
    override fun stopTracking() = Unit
    override fun askToResume() = Unit
}

private object NoMapFile : MapFileSource {
    override val state: StateFlow<MapFileState> = MutableStateFlow(MapFileState.None)
}

/** A server on this phone that takes every location report, keeping its body, and has nothing else. */
private class ReportServer : Closeable {
    private val socket = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    val url = "http://127.0.0.1:${socket.localPort}/"
    val reports = LinkedBlockingQueue<String>()

    init {
        thread(isDaemon = true) {
            while (true) {
                val client = try {
                    socket.accept()
                } catch (e: IOException) {
                    break
                }
                thread(isDaemon = true) { client.use(::answer) }
            }
        }
    }

    private fun answer(client: Socket) {
        val request = client.getInputStream().bufferedReader()
        val path = request.readLine()?.split(' ')?.getOrNull(1) ?: return
        var length = 0
        while (true) {
            val header = request.readLine() ?: return
            if (header.isEmpty()) break
            if (header.startsWith("Content-Length:", ignoreCase = true)) length = header.substringAfter(':').trim().toInt()
        }
        // The reports are JSON in ASCII, so a character is a byte.
        val body = CharArray(length)
        var read = 0
        while (read < length) read += request.read(body, read, length - read).takeIf { it >= 0 } ?: return
        val status = if (path == "/api/v1/locations") {
            reports += String(body)
            "201 Created"
        } else {
            "404 Not Found"
        }
        client.getOutputStream().run {
            write("HTTP/1.1 $status\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}".toByteArray())
            flush()
        }
    }

    override fun close() = socket.close()
}
