package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasParent
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.OccupancyStatus
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState

/** The Tracking screen's occupancy row, at the top of the panel the trip bar pulls up into. */
@RunWith(AndroidJUnit4::class)
class OccupancyRowTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20260902", 100L)

    private val labels = mapOf(
        OccupancyStatus.MANY_SEATS_AVAILABLE to R.string.tracking_occupancy_many_seats,
        OccupancyStatus.FEW_SEATS_AVAILABLE to R.string.tracking_occupancy_few_seats,
        OccupancyStatus.STANDING_ROOM_ONLY to R.string.tracking_occupancy_standing,
        OccupancyStatus.FULL to R.string.tracking_occupancy_full,
    )

    private fun show(occupancy: OccupancyStatus?, onOccupancyClick: (OccupancyStatus) -> Unit = {}) {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(tracking = TrackingState(active = true), activeTrip = trip.copy(occupancy = occupancy)),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
                onOccupancyClick = onOccupancyClick,
            )
        }
        compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Expand)).performSemanticsAction(SemanticsActions.Expand)
    }

    private fun button(occupancy: OccupancyStatus) =
        compose.onNode(hasText(context.getString(labels.getValue(occupancy))) and hasClickAction())

    @Test
    fun nothingIsChosenUntilTheDriverSays() {
        show(occupancy = null)

        for (occupancy in OccupancyStatus.entries) button(occupancy).assertIsDisplayed().assertIsNotSelected()
    }

    @Test
    fun theTripsChoiceShowsChosenAndTheRestDoNot() {
        show(OccupancyStatus.FULL)

        button(OccupancyStatus.FULL).assertIsSelected()
        for (occupancy in OccupancyStatus.entries - OccupancyStatus.FULL) button(occupancy).assertIsNotSelected()
    }

    @Test
    fun eachButtonHandsBackItsOwnValue() {
        val tapped = mutableListOf<OccupancyStatus>()
        show(occupancy = null, onOccupancyClick = { tapped += it })

        for (occupancy in OccupancyStatus.entries) button(occupancy).performClick()

        assertEquals(OccupancyStatus.entries.toList(), tapped)
    }

    @Test
    fun talkBackReadsTheQuestionThenTheFourAsOneChoice() {
        show(OccupancyStatus.STANDING_ROOM_ONLY)

        val group = SemanticsMatcher.keyIsDefined(SemanticsProperties.SelectableGroup)
        compose.onNode(hasParent(group) and hasText(context.getString(R.string.tracking_occupancy_question))).assertIsDisplayed()
        val options = compose.onAllNodes(hasParent(group) and SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton))
        options.assertCountEquals(4)
        // A chosen radio button usually stays chosen; TalkBack says what tapping this one does.
        val clear = context.getString(R.string.tracking_occupancy_clear)
        button(OccupancyStatus.STANDING_ROOM_ONLY).assertIsSelected()
            .assert(SemanticsMatcher("taps to $clear") { it.config[SemanticsActions.OnClick].label == clear })
        button(OccupancyStatus.FULL).assertIsNotSelected()
            .assert(SemanticsMatcher("taps to choose") { it.config[SemanticsActions.OnClick].label == null })
    }
}
