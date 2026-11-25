package org.onebusaway.vehicletracker.ui

import android.content.Context
import android.content.res.Configuration
import android.graphics.Bitmap
import android.os.Build
import android.os.LocaleList
import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.DeviceConfigurationOverride
import androidx.compose.ui.test.Locales
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.Parameterized
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.api.VehicleDto
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.help.HelpScreen
import org.onebusaway.vehicletracker.ui.login.LoginScreenContent
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.permissions.ChecklistRow
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.PreflightContent
import org.onebusaway.vehicletracker.ui.permissions.RowState
import org.onebusaway.vehicletracker.ui.resume.ResumeCard
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftScreenContent
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftUiState
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import org.onebusaway.vehicletracker.ui.routes.RouteItem
import org.onebusaway.vehicletracker.ui.routes.RoutesScreenContent
import org.onebusaway.vehicletracker.ui.routes.RoutesUiState
import org.onebusaway.vehicletracker.ui.runs.Board
import org.onebusaway.vehicletracker.ui.runs.BoardRun
import org.onebusaway.vehicletracker.ui.runs.RunDirection
import org.onebusaway.vehicletracker.ui.runs.RunHighlight
import org.onebusaway.vehicletracker.ui.runs.RunLabel
import org.onebusaway.vehicletracker.ui.runs.RunPage
import org.onebusaway.vehicletracker.ui.runs.RunsScreenContent
import org.onebusaway.vehicletracker.ui.runs.RunsUiState
import org.onebusaway.vehicletracker.ui.runs.TripRun
import org.onebusaway.vehicletracker.ui.runs.runBoardTipText
import org.onebusaway.vehicletracker.ui.settings.PhoneAccess
import org.onebusaway.vehicletracker.ui.settings.SettingsContent
import org.onebusaway.vehicletracker.ui.settings.readiness
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState
import org.onebusaway.vehicletracker.ui.vehicles.BusGroup
import org.onebusaway.vehicletracker.ui.vehicles.BusSection
import org.onebusaway.vehicletracker.ui.vehicles.BusTile
import org.onebusaway.vehicletracker.ui.vehicles.VehicleScreenContent
import org.onebusaway.vehicletracker.ui.vehicles.VehiclesUiState
import org.onebusaway.vehicletracker.ui.welcome.WelcomeContent
import java.io.File
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import androidx.compose.ui.graphics.Color as ComposeColor
import androidx.compose.ui.text.intl.LocaleList as ComposeLocaleList

/**
 * The main screens in a debug build's two pseudolocales: en-XA, every string accented and made longer, and ar-XB,
 * right to left. Each test checks that the screen reads its words from strings.xml in that language, then saves the
 * screen as a PNG for a person to look over: English words that never changed are text strings.xml lacks, and a
 * clipped or overlapping line is one a longer language would break. `./gradlew :app:connectedDebugAndroidTest` pulls
 * the PNGs into `build/outputs/connected_android_test_additional_output`.
 */
@RunWith(Parameterized::class)
class PseudolocaleScreensTest(private val tag: String) {
    companion object {
        @JvmStatic
        @Parameterized.Parameters(name = "{0}")
        fun pseudolocales() = listOf("en-XA", "ar-XB")
    }

    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val pseudo: Context =
        context.createConfigurationContext(Configuration(context.resources.configuration).apply { setLocales(LocaleList.forLanguageTags(tag)) })

    /**
     * Shows [content] in the pseudolocale, checks it shows [title] in that language rather than in English, and saves
     * it as `<name>-<pseudolocale>.png`.
     */
    private fun check(name: String, title: Int, content: @Composable () -> Unit) {
        compose.setContent {
            DeviceConfigurationOverride(DeviceConfigurationOverride.Locales(ComposeLocaleList(tag))) {
                AppTheme(darkTheme = false) { Surface { content() } }
            }
        }

        assertNotEquals("this debug build has $tag strings", context.getString(title), pseudo.getString(title))
        assertTrue(compose.onAllNodesWithText(pseudo.getString(title)).fetchSemanticsNodes().isNotEmpty())
        save(name)
    }

    /**
     * Saves the screen where Gradle's connected test task collects it, or, run some other way, in the app's own
     * files on the phone.
     */
    private fun save(name: String) {
        val image = compose.onRoot().captureToImage().asAndroidBitmap()
        val dirs = listOfNotNull(
            InstrumentationRegistry.getArguments().getString("additionalTestOutputDir")?.let(::File),
            context.getExternalFilesDir("pseudolocales"),
        )
        val saved = dirs.any { dir ->
            runCatching {
                dir.mkdirs()
                File(dir, "$name-$tag.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }.isSuccess
        }
        assertTrue("saved $name-$tag.png in one of $dirs", saved)
    }

    // --- Fixtures, as ScreenFlowTest has them ---

    private val pacific = ZoneId.of("America/Los_Angeles")

    private fun at(hour: Int, minute: Int): Instant = ZonedDateTime.of(2026, 9, 2, hour, minute, 0, 0, pacific).toInstant()

    private val t1 = TripGeometry(
        tripId = "T1",
        serviceDate = "20260902",
        timezone = pacific,
        shapePoints = listOf(GeoPoint(47.6000, -122.3300), GeoPoint(47.6045, -122.3300), GeoPoint(47.6090, -122.3300)),
        stops = listOf(
            TripStop("ST1", "Stop ST1", 47.6000, -122.3300, 0.0, at(8, 0), at(8, 0)),
            TripStop("ST2", "Stop ST2", 47.6045, -122.3300, 500.0, at(8, 5), at(8, 5)),
            TripStop("ST3", "Stop ST3", 47.6090, -122.3300, 1001.0, at(8, 10), at(8, 10)),
        ),
        thresholds = AdherenceThresholds(maxShapeDistanceM = 60.0, scheduleEarlyS = 900, scheduleLateS = 5400),
    )

    private val trip = ActiveTrip(7L, "T1", "4317", "100263", "20260902", 100L)

    private fun route(id: String, short: String, longName: String) = RouteItem(id, RouteBadgeModel.of(short, "FDB71A", "000000"), longName)

    private val routes = listOf(
        route("R7", "7", "Rainier Beach - Downtown Seattle"),
        route("R8", "8", "Rainier Beach - Seattle Center"),
    )

    private val board = Board(
        RunDirection(0, "Downtown Seattle", "Mount Baker TC", "4th Ave & Pine St"),
        (0 until 6).map { n ->
            BoardRun("in$n", "6:%02d AM".format(n * 10), "Downtown Seattle", RunLabel("", ""), past = n < 2, highlight = if (n == 2) RunHighlight.NOW else null)
        },
        nowAt = 2,
        openAt = 1,
    )

    // --- The screens ---

    @Test
    fun signIn() = check("sign-in", R.string.login_title) {
        LoginScreenContent(LoginUiState(), TextFieldState(), TextFieldState(), TextFieldState(), onChangeServer = {}, onEdited = {}, onSignIn = {})
    }

    @Test
    fun vehicles() = check("vehicles", R.string.vehicles_title) {
        VehicleScreenContent(
            VehiclesUiState.Loaded(
                listOf(VehicleDto("4317", "4317"), VehicleDto("4322", "4322")),
                "",
                listOf(
                    BusGroup(BusSection.FAVORITES, listOf(BusTile("4317", null, favorite = true))),
                    BusGroup(BusSection.ALL, listOf(BusTile("4322", null, favorite = false))),
                ),
            ),
            {}, {}, {}, {}, {}, {}, account = {},
        )
    }

    @Test
    fun routes() = check("routes", R.string.routes_title) {
        RoutesScreenContent(RoutesUiState.Loaded(routes, routes, emptyList(), "", grid = false), "4317", {}, {}, {}, {}, {}, {}, account = {})
    }

    @Test
    fun runBoard() = check("run-board", R.string.runs_title) {
        RunsScreenContent(
            state = RunsUiState.Loaded(
                RunPage("20260902", pacific, listOf(TripRun("in0", "Downtown Seattle", 0, "A", "B", Instant.EPOCH, Instant.EPOCH, RunLabel("", "")))),
                listOf(board),
                startBoard = 0,
                startingRunId = null,
                error = null,
            ),
            route = RouteBadgeModel.of("7", "FDB71A", "000000"),
            vehicleId = "4317",
            onBack = {},
            onBusClick = {},
            onRouteClick = {},
            onRunClick = {},
            onReload = {},
            onTryAgain = {},
            onChangeBus = {},
            onChooseAnotherRoute = {},
            onSessionEnded = {},
            account = {},
        )
    }

    @Test
    fun tracking() = check("tracking", R.string.tracking_end_trip_button) {
        TrackingScreenContent(
            state = TrackingUiState(tracking = TrackingState(active = true, problem = TrackingProblem.NONE, geometry = t1), activeTrip = trip),
            onEndTripClick = {},
            onEndTripLocallyClick = {},
            onDismissError = {},
            onReauthClick = {},
        )
    }

    @Test
    fun resume() = check("resume", R.string.resume_title) {
        val card = ResumeCard(RouteBadgeModel("7", ComposeColor(0xFFFDB71A), ComposeColor.Black), "Downtown Seattle", at(6, 58), "4317", at(6, 58), pacific)
        ResumeShiftScreenContent(ResumeShiftUiState(trip = trip, card = card), { at(6, 58).epochSecond + 23 * 60 }, {}, {}, {}, {})
    }

    @Test
    fun welcome() = check("welcome", R.string.welcome_riders_title) {
        WelcomeContent(onLeave = {})
    }

    @Test
    fun help() = check("help", R.string.help_title) {
        HelpScreen({}, {})
    }

    @Test
    fun settings() = check("settings", R.string.settings_title) {
        val grants = Grants(Build.VERSION.SDK_INT, fine = true, coarse = true, background = true, notifications = true, batteryExempt = false)
        SettingsContent(
            readiness = readiness(PhoneAccess(grants, locationServicesOn = true), waiting = null),
            account = AccountUiState("driver@example.com", "tracker.example.org"),
            version = "0.1.0 (1)",
            theme = ThemeChoice.SYSTEM,
            onBack = {}, onOpenPhoneSettings = {}, onSignOut = {}, onSignedOut = {}, onChooseTheme = {}, onHelp = {}, onShare = {},
        )
    }

    @Test
    fun beforeYouStart() = check("before-you-start", R.string.preflight_title) {
        val states = listOf(RowState.ALLOWED, RowState.NEXT, RowState.NOT_REACHED, RowState.NOT_REACHED, RowState.NOT_REACHED, RowState.NOT_REACHED)
        PreflightContent("4317", PermissionRow.entries.zip(states) { row, state -> ChecklistRow(row, state) }, true, {}, {}, {})
    }

    /** A sentence that takes another string keeps its place for it in every language, and the app fills it. */
    @Test
    fun theRunBoardTipNamesTheStartButtonInTheSameLanguage() {
        compose.setContent {
            DeviceConfigurationOverride(DeviceConfigurationOverride.Locales(ComposeLocaleList(tag))) { Text(runBoardTipText()) }
        }

        val button = pseudo.getString(R.string.runs_start_confirm)
        assertNotEquals("this debug build has $tag strings", context.getString(R.string.runs_start_confirm), button)
        val words = pseudo.getString(R.string.tip_run_board_text, button)
        assertTrue("the tip names the button: $words", words.contains(button))
        assertFalse("no placeholder is left showing: $words", words.contains("%"))
        compose.onNodeWithText(words).assertExists()
    }
}
