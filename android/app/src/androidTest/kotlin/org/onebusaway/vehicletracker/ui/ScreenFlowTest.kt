package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isDisplayed
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.Dp
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
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceThresholds
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.TripGeometry
import org.onebusaway.vehicletracker.engine.TripStop
import org.onebusaway.vehicletracker.service.LocationFix
import org.onebusaway.vehicletracker.ui.login.LoginScreenContent
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftScreenContent
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftUiState
import org.onebusaway.vehicletracker.ui.tracking.StatusArea
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState
import org.onebusaway.vehicletracker.ui.tracking.TripPanel
import org.onebusaway.vehicletracker.ui.tracking.statusCardModel
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import kotlin.math.absoluteValue

/**
 * Instrumented UI tests that render the stateless screen `Content` composables directly with
 * fake state and no-op callbacks — no Hilt/ViewModel wiring needed. Run with
 * `./gradlew :app:connectedDebugAndroidTest`.
 */
@RunWith(AndroidJUnit4::class)
class ScreenFlowTest {
    @get:Rule val compose = createComposeRule()

    private fun getString(resId: Int): String =
        InstrumentationRegistry.getInstrumentation().targetContext.getString(resId)

    @Test
    fun loginScreen_disablesButtonWhileLoading() {
        compose.setContent {
            LoginScreenContent(
                state = LoginUiState(loading = true),
                onServerUrlChange = {},
                onEmailChange = {},
                onPasswordChange = {},
                onLoginClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.login_button)).assertIsNotEnabled()
    }

    @Test
    fun trackingScreen_showsNoProblemLineWhileAllIsWell() {
        showTracking(TrackingState(active = true, problem = TrackingProblem.NONE, geometry = t1))

        for (title in listOf(R.string.tracking_problem_no_network, R.string.tracking_problem_no_gps, R.string.tracking_problem_clock_skew)) {
            compose.onNodeWithText(getString(title)).assertDoesNotExist()
        }
        compose.onNodeWithText(getString(R.string.tracking_signed_out_title)).assertDoesNotExist()
    }

    @Test
    fun resumeScreen_namesTheShiftAndOffersBothActions() {
        var resumed = false
        var ended = false
        compose.setContent {
            ResumeShiftScreenContent(
                state = ResumeShiftUiState(
                    trip = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L),
                    startedAgoSec = 2 * 3600L + 5 * 60,
                ),
                onResumeClick = { resumed = true },
                onEndShiftClick = { ended = true },
                onEndShiftLocallyClick = {},
                onDismissError = {},
            )
        }

        compose.onNodeWithText(resources.getString(R.string.resume_message, "bus-1")).assertIsDisplayed()
        compose.onNodeWithText(resources.getString(R.string.resume_route, "R1")).assertIsDisplayed()
        compose.onNodeWithText(resources.getString(R.string.resume_started_hours_ago, 2, 5)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.resume_button)).performClick()
        compose.onNodeWithText(getString(R.string.resume_end_shift_button)).performClick()
        assertTrue(resumed)
        assertTrue(ended)
    }

    @Test
    fun resumeScreen_disablesBothActionsUntilTheTripLoads() {
        compose.setContent {
            ResumeShiftScreenContent(
                state = ResumeShiftUiState(trip = null),
                onResumeClick = {},
                onEndShiftClick = {},
                onEndShiftLocallyClick = {},
                onDismissError = {},
            )
        }

        compose.onNodeWithText(getString(R.string.resume_button)).assertIsNotEnabled()
        compose.onNodeWithText(getString(R.string.resume_end_shift_button)).assertIsNotEnabled()
    }

    @Test
    fun resumeScreen_scrollsToEndShiftAtALargeFontScale() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                // A short screen, so End Shift starts below the fold whatever the test device.
                Box(Modifier.height(360.dp)) {
                    ResumeShiftScreenContent(
                        state = ResumeShiftUiState(trip = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L)),
                        onResumeClick = {},
                        onEndShiftClick = {},
                        onEndShiftLocallyClick = {},
                        onDismissError = {},
                    )
                }
            }
        }

        val endShift = compose.onNodeWithText(getString(R.string.resume_end_shift_button))
        endShift.assertIsNotDisplayed()
        endShift.performScrollTo().assertIsDisplayed()
    }

    // --- The status card, against the server fixture's trip T1 ---

    private val resources get() = InstrumentationRegistry.getInstrumentation().targetContext.resources
    private val pacific = ZoneId.of("America/Los_Angeles")

    private fun at(hour: Int, minute: Int): Instant = ZonedDateTime.of(2026, 9, 2, hour, minute, 0, 0, pacific).toInstant()

    /** A straight 1 km run north with stops at 0, 500 and 1001 m, scheduled 08:00, 08:05, 08:10. */
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

    private fun judged(lat: Double, lon: Double, time: Instant) = requireNotNull(AdherenceEvaluator.of(t1)).evaluate(
        LocationFix(lat, lon, bearing = 0.0, speed = 8.0, accuracy = 5.0, timeEpochSec = time.epochSecond),
        previous = null,
    )

    private fun showTracking(tracking: TrackingState, activeTrip: ActiveTrip? = null, onReauthClick: () -> Unit = {}) {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = tracking, activeTrip = activeTrip),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = onReauthClick,
            )
        }
    }

    private val announcedPolitely = SemanticsMatcher.expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite)

    /** The status card's answer: the one text on the screen that TalkBack announces as it changes. */
    private fun answer(text: String) = compose.onNode(hasText(text) and announcedPolitely)

    /**
     * The one node showing [text] on the screen. The trip panel repeats some of the card's words,
     * and is laid out below the screen while it is down.
     */
    private fun shown(text: String, substring: Boolean = false): SemanticsNodeInteraction {
        val nodes = compose.onAllNodesWithText(text, substring = substring)
        val onScreen = nodes.fetchSemanticsNodes().indices.filter { nodes[it].isDisplayed() }
        assertEquals("nodes showing '$text'", 1, onScreen.size)
        return nodes[onScreen.single()]
    }

    @Test
    fun trackingScreen_showsAdherenceAndTheNextStop() {
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5))))

        answer(getString(R.string.tracking_adherence_on_time)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_next_stop)).assertIsDisplayed()
        shown("Stop ST3")
        // ~501 m to ST3, due at 08:10 on the agency's clock whatever the device's zone.
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_distance_m, 501), substring = true).assertIsDisplayed()
        shown("8:10", substring = true)
    }

    @Test
    fun trackingScreen_countsTheTripTimeFromItsStart() {
        val startedAt = System.currentTimeMillis() / 1000 - 65
        showTracking(TrackingState(active = true, tripStartedAtEpochSec = startedAt, geometry = t1))

        // 01:05, or a second or two more on a slow device.
        compose.onNodeWithText("01:0", substring = true).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsTheNextStopInKilometresPastOneKilometre() {
        // Without ST2, the next stop from ST1 is ST3, 1001 m on.
        val twoStops = t1.copy(stops = listOf(t1.stops[0], t1.stops[2]))
        val adherence = requireNotNull(AdherenceEvaluator.of(twoStops)).evaluate(
            LocationFix(47.6000, -122.3300, bearing = 0.0, speed = 8.0, accuracy = 5.0, timeEpochSec = at(8, 0).epochSecond),
            previous = null,
        )
        showTracking(TrackingState(active = true, geometry = twoStops, adherence = adherence))

        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_distance_km, 1.0), substring = true).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsWholeMinutesLate() {
        // 400 s behind ST2's schedule rounds to 7 minutes.
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5).plusSeconds(400))))

        answer(resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, 7, 7)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_showsOffScheduleWithTheDeviation() {
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6000, -122.3300, at(8, 0).plusSeconds(6000))))

        answer(resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, 100, 100)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_off_schedule)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_right_run)).assertIsDisplayed()
        shown("Next Stop ST2", substring = true)
    }

    @Test
    fun trackingScreen_showsOffRouteButAnnouncesItWithoutTheDistance() {
        // 0.017° of longitude east of the line is ~1.28 km.
        showTracking(TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3130, at(8, 5))))

        answer(getString(R.string.tracking_adherence_off_route_short)).assertIsDisplayed()
        // Drawn on the card, but changing with every fix, so TalkBack is never handed it to read out.
        val distance = resources.getString(R.string.tracking_adherence_distance_km, 1.3)
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_off_route, distance)).assertDoesNotExist()
        shown("Stop ST3")
    }

    @Test
    fun trackingScreen_waitsForTheFirstFixFromTheFirstStop() {
        showTracking(TrackingState(active = true, geometry = t1), activeTrip = ActiveTrip(7L, "T1", "4317", "R1", "20260902", 100L))

        compose.onNodeWithText(getString(R.string.tracking_adherence_waiting_for_gps)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_from)).assertIsDisplayed()
        shown("Stop ST1")
        // "8:00 AM run · 4317", with the time in whatever style the device's locale writes it.
        compose.onNodeWithText(resources.getString(R.string.tracking_adherence_run_vehicle, "", "4317"), substring = true).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_saysWhenThereIsNoScheduleToJudgeAgainst() {
        showTracking(TrackingState(active = true, geometry = null))

        compose.onNodeWithText(getString(R.string.tracking_adherence_no_schedule)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_no_schedule_riders)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_no_schedule_detail)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_tellsTheMapHowMuchTheCardAndTripBarCover() {
        var covered: PaddingValues? = null
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5)))),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
                map = { modifier, contentPadding, _ ->
                    covered = contentPadding
                    Spacer(modifier)
                },
            )
        }
        compose.waitForIdle()

        val padding = requireNotNull(covered)
        val screenBottom = compose.onRoot().getBoundsInRoot().bottom
        // The uncovered map starts within the card's bottom padding, under its last line, and ends
        // where the trip bar's drag handle begins.
        val cardLastLine = shown("8:10", substring = true).getBoundsInRoot().bottom
        assertTrue(padding.calculateTopPadding() - cardLastLine in 0.dp..20.dp)
        val sheetTop = compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Expand)).getBoundsInRoot().top
        assertTrue((sheetTop - (screenBottom - padding.calculateBottomPadding())).value.absoluteValue <= 1f)
    }

    // --- The trip bar, and the panel it pulls up into ---

    private val route7 = t1.copy(routeShortName = "7", routeColor = "FDB71A", routeTextColor = "000000", headsign = "Downtown Seattle")
    private val trip4317 = ActiveTrip(7L, "T1", "4317", "R1", "20260902", 100L)
    private val onTheRoad = TrackingState(active = true, fixesSent = 142, geometry = route7, adherence = judged(47.6030, -122.3300, at(8, 5)))
    private val panelHandle get() = compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Expand))

    @Test
    fun tripBar_showsTheRouteWhereItIsHeadedAndWhatWasSent() {
        showTracking(onTheRoad, activeTrip = trip4317)

        compose.onNodeWithText("7").assertIsDisplayed()
        compose.onNodeWithText("Downtown Seattle", substring = true).assertIsDisplayed()
        compose.onNodeWithText(resources.getQuantityString(R.plurals.tracking_bar_sent, 142, 142), substring = true).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).assertIsDisplayed()
    }

    @Test
    fun tripBar_saysEndingOnTheButtonWhileTheEndIsOnItsWay() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = onTheRoad, activeTrip = trip4317, ending = true),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.tracking_end_trip_ending)).assertIsDisplayed().assertIsNotEnabled()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).assertDoesNotExist()
    }

    private fun showTripBar(width: Dp, geometry: TripGeometry) {
        val twentyMinutesIn = onTheRoad.copy(geometry = geometry, tripStartedAtEpochSec = System.currentTimeMillis() / 1000 - 1188)
        compose.setContent {
            // Required, not preferred: CI's emulator is only 320 dp wide, and would squeeze a preferred width.
            Box(Modifier.requiredWidth(width)) {
                TrackingScreenContent(
                    state = TrackingUiState(tracking = twentyMinutesIn, activeTrip = trip4317),
                    onEndTripClick = {},
                    onEndTripLocallyClick = {},
                    onDismissError = {},
                    onReauthClick = {},
                )
            }
        }
    }

    private fun assertSentCountWhole() {
        val sent = resources.getQuantityString(R.plurals.tracking_bar_sent, 142, 142)
        // The bar is one button to TalkBack, so its texts are found in the unmerged tree.
        val layouts = mutableListOf<TextLayoutResult>()
        compose.onNodeWithText(sent, useUnmergedTree = true).fetchSemanticsNode()
            .config[SemanticsActions.GetTextLayoutResult].action?.invoke(layouts)
        // Squeezed, the text wraps after the number and its one line shows only that.
        val layout = layouts.single()
        assertEquals(sent, sent.substring(0, layout.getLineEnd(0, visibleEnd = true)))
    }

    @Test
    fun tripBar_keepsTheSentCountWholeOnA360dpPhone() {
        showTripBar(360.dp, route7)

        assertSentCountWhole()
    }

    @Test
    fun tripBar_keepsTheSentCountWholeBesideALongRouteName() {
        // A Pixel 5, where "7" leaves room to spare and RapidRide's "E Line" does not.
        showTripBar(393.dp, route7.copy(routeShortName = "E Line"))

        assertSentCountWhole()
    }

    // --- Ending the trip ---

    private fun showEnding(state: TrackingUiState, onEnd: () -> Unit = {}, onEndLocally: () -> Unit = {}) {
        compose.setContent {
            TrackingScreenContent(
                state = state,
                onEndTripClick = onEnd,
                onEndTripLocallyClick = onEndLocally,
                onDismissError = {},
                onReauthClick = {},
            )
        }
    }

    private fun inDialog(text: String) = compose.onNode(hasText(text) and hasAnyAncestor(isDialog()))

    @Test
    fun endTrip_asksFirstAndKeepGoingKeepsTheTrip() {
        var ended = false
        showEnding(TrackingUiState(tracking = onTheRoad, activeTrip = trip4317), onEnd = { ended = true })

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()

        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_title)).assertIsDisplayed()
        // "Reports stop, and route 7's 8:00 AM run is marked complete. 142 reports were sent.",
        // with the time in the device's style.
        val message = resources.getQuantityString(R.plurals.tracking_end_trip_dialog_message, 142, "7", "8:00", 142)
        compose.onNodeWithText(message.substringBefore("8:00"), substring = true).assertIsDisplayed()
        compose.onNodeWithText(message.substringAfter("8:00"), substring = true).assertIsDisplayed()

        inDialog(getString(R.string.tracking_end_trip_dialog_cancel)).performClick()

        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_title)).assertDoesNotExist()
        assertFalse(ended)
    }

    @Test
    fun endTrip_endsTheTripOnceConfirmed() {
        var ended = false
        showEnding(TrackingUiState(tracking = onTheRoad, activeTrip = trip4317), onEnd = { ended = true })

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()
        inDialog(getString(R.string.tracking_end_trip_dialog_confirm)).performClick()

        assertTrue(ended)
        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_title)).assertDoesNotExist()
    }

    @Test
    fun endTrip_saysThisRunWhenThePhoneHasNoRouteForIt() {
        showEnding(TrackingUiState(tracking = TrackingState(active = true, fixesSent = 1, geometry = null)))

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()

        compose.onNodeWithText(resources.getQuantityString(R.plurals.tracking_end_trip_dialog_message_no_run, 1, 1)).assertIsDisplayed()
    }

    @Test
    fun endTrip_saysThisRunForARouteTheFeedGaveNoName() {
        showEnding(TrackingUiState(tracking = onTheRoad.copy(geometry = route7.copy(routeShortName = ""))))

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()

        compose.onNodeWithText(resources.getQuantityString(R.plurals.tracking_end_trip_dialog_message_no_run, 142, 142)).assertIsDisplayed()
    }

    @Test
    fun endTrip_thatFailedOffersToTryAgainOrEndOnThisPhone() {
        var retried = false
        var endedHere = false
        showEnding(
            TrackingUiState(tracking = onTheRoad, activeTrip = trip4317, endTripError = true),
            onEnd = { retried = true },
            onEndLocally = { endedHere = true },
        )

        compose.onNodeWithText(getString(R.string.tracking_end_trip_error_title)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_error_message)).assertIsDisplayed()
        inDialog(getString(R.string.tracking_end_trip_error_retry)).performClick()
        assertTrue(retried)
        inDialog(getString(R.string.tracking_end_trip_error_end_locally)).performClick()
        assertTrue(endedHere)
    }

    @Test
    fun tripPanel_opensFromItsDragHandleWithTheRunReportsAndStops() {
        showTracking(onTheRoad, activeTrip = trip4317)
        compose.onNodeWithText(getString(R.string.tracking_fact_run)).assertIsNotDisplayed()

        panelHandle.performSemanticsAction(SemanticsActions.Expand)

        compose.onNodeWithText(getString(R.string.tracking_fact_run)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_reports_reporting)).assertIsDisplayed()
        // The vehicle between ST1, which it has passed, and ST2, which is next: rows of a stop's
        // name and time, and of the vehicle and how it is keeping to the schedule.
        fun row(first: String, second: String) = compose.onNode(hasText(first) and hasText(second, substring = true))
        row("Stop ST1", "8:00").assertIsDisplayed()
        // 100 s behind ST1 to ST2's schedule, which is on time and reads as 2 min late.
        row("4317", resources.getQuantityString(R.plurals.tracking_adherence_minutes_late, 2, 2)).assertIsDisplayed()
        row("Stop ST2", "8:05").assertIsDisplayed()
        row("Stop ST3", "8:10").assertIsDisplayed()
    }

    @Test
    fun tripPanel_opensWhenTheBarIsTapped() {
        showTracking(onTheRoad, activeTrip = trip4317)
        val bar = compose.onNodeWithText("Downtown Seattle", substring = true)
        val clickLabel = getString(R.string.tracking_bar_click_label)
        bar.assert(SemanticsMatcher("click label is $clickLabel") { it.config.getOrNull(SemanticsActions.OnClick)?.label == clickLabel })

        bar.performClick()

        compose.onNodeWithText(getString(R.string.tracking_fact_run)).assertIsDisplayed()
    }

    @Test
    fun tripPanel_holdsItsRowsBelowTheScreenWhileDown() {
        // Under a navigation bar (Android 15 and later draw the panel beneath it) the rows below the
        // bar would otherwise sit on screen, faded, while the panel is down.
        val navigationBar = 48.dp
        var shown by mutableStateOf(0f)
        compose.setContent {
            TripPanel(
                state = TrackingUiState(tracking = onTheRoad, activeTrip = trip4317),
                model = statusCardModel(onTheRoad, vehicleId = "4317"),
                onEndTripClick = {},
                onBarClick = {},
                onBarHeightChanged = {},
                rowsAlpha = { shown },
                navigationBarPx = with(LocalDensity.current) { navigationBar.roundToPx() },
            )
        }
        val run = compose.onNodeWithText(getString(R.string.tracking_fact_run))
        val heldTop = run.getBoundsInRoot().top

        shown = 1f
        compose.waitForIdle()

        assertEquals(navigationBar.value, (heldTop - run.getBoundsInRoot().top).value, 0.5f)
    }

    @Test
    fun tripPanel_stopsBelowTheProblemLineAndTheSignedOutCard() {
        // A run with the stops of a real one, so the panel reaches its limit when pulled up.
        val longRun = t1.copy(
            stops = List(40) { i -> TripStop("S$i", "Stop $i", 47.6000 + i * 0.0001, -122.3300, i * 25.0, at(8, i), at(8, i)) },
        )
        var problem by mutableStateOf(TrackingProblem.NO_NETWORK)
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = onTheRoad.copy(geometry = longRun, problem = problem), activeTrip = trip4317),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }
        panelHandle.performSemanticsAction(SemanticsActions.Expand)
        compose.waitForIdle()

        // Open, the handle offers Collapse instead of Expand.
        val openHandle = compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Collapse))
        fun assertHandleBelow(text: String) {
            val handleTop = openHandle.getBoundsInRoot().top
            val bottom = compose.onNodeWithText(text).getBoundsInRoot().bottom
            assertTrue("the panel's handle ($handleTop) is under '$text' ($bottom)", handleTop >= bottom)
        }
        assertHandleBelow(getString(R.string.tracking_problem_no_network))

        problem = TrackingProblem.AUTH_EXPIRED
        compose.waitForIdle()

        assertHandleBelow(getString(R.string.tracking_reauth_button))
    }

    @Test
    fun tripPanel_closesOnBack() {
        showTracking(onTheRoad, activeTrip = trip4317)
        panelHandle.performSemanticsAction(SemanticsActions.Expand)
        compose.onNodeWithText(getString(R.string.tracking_fact_run)).assertIsDisplayed()

        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        compose.waitForIdle()

        compose.onNodeWithText(getString(R.string.tracking_fact_run)).assertIsNotDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).assertIsDisplayed()
    }

    @Test
    fun statusCard_keepsOneHeightInEveryState() {
        val states = listOf(
            TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3300, at(8, 5))),
            // The longest answer, which the card shrinks to fit on one line.
            TrackingState(active = true, geometry = t1, adherence = judged(47.6045, -122.3130, at(8, 5))),
            TrackingState(active = true, geometry = t1, adherence = judged(47.6000, -122.3300, at(8, 0).plusSeconds(6000))),
            TrackingState(active = true, geometry = t1),
            TrackingState(active = false, geometry = null),
            TrackingState(active = true, geometry = null),
        )
        var tracking by mutableStateOf(states.first())
        val heights = mutableSetOf<Int>()
        compose.setContent {
            Box(Modifier.onSizeChanged { heights += it.height }) {
                StatusArea(statusCardModel(tracking, vehicleId = "4317"), TrackingProblem.NONE, onSignIn = {})
            }
        }

        for (state in states) {
            tracking = state
            compose.waitForIdle()
        }

        // The map sits under the card, so a card that changed height would move the map with every status.
        assertEquals(1, heights.size)
    }

    // --- The problem line under the status card, and the Signed out card in its place ---

    private fun assertProblemLine(problem: TrackingProblem, title: Int, detail: Int) {
        showTracking(TrackingState(active = true, problem = problem, geometry = t1))

        compose.onNodeWithText(getString(title)).assertIsDisplayed().assert(announcedPolitely)
        compose.onNodeWithText(getString(detail)).assertIsDisplayed()
        // The status card stays above the line.
        compose.onNodeWithText(getString(R.string.tracking_adherence_waiting_for_gps)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_saysWhenThereIsNoConnection() =
        assertProblemLine(TrackingProblem.NO_NETWORK, R.string.tracking_problem_no_network, R.string.tracking_problem_no_network_detail)

    @Test
    fun trackingScreen_saysWhenThereIsNoGps() =
        assertProblemLine(TrackingProblem.NO_GPS, R.string.tracking_problem_no_gps, R.string.tracking_problem_no_gps_detail)

    @Test
    fun trackingScreen_saysWhenThePhoneClockIsWrong() =
        assertProblemLine(TrackingProblem.CLOCK_SKEW, R.string.tracking_problem_clock_skew, R.string.tracking_problem_clock_skew_detail)

    @Test
    fun trackingScreen_signedOutCardTakesTheStatusCardsPlace() {
        var signInClicked = false
        showTracking(TrackingState(active = true, problem = TrackingProblem.AUTH_EXPIRED, geometry = t1), onReauthClick = { signInClicked = true })

        compose.onNodeWithText(getString(R.string.tracking_signed_out_title))
            .assertIsDisplayed()
            .assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Heading))
        compose.onNodeWithText(getString(R.string.tracking_signed_out_message)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_adherence_waiting_for_gps)).assertDoesNotExist()
        compose.onNodeWithText(getString(R.string.tracking_reauth_button)).performClick()
        assertTrue(signInClicked)
    }
}
