package org.onebusaway.vehicletracker.ui

import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.MyTrip
import org.onebusaway.vehicletracker.data.MyTripRoute
import org.onebusaway.vehicletracker.data.MyTripsPage
import org.onebusaway.vehicletracker.ui.settings.MyTripsContent
import org.onebusaway.vehicletracker.ui.settings.MyTripsList
import org.onebusaway.vehicletracker.ui.settings.TripsPageRequest
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.time.Duration
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZonedDateTime

/** My trips as the driver sees it: the days and rows, the empty and failed pages, and the end of the list. */
@RunWith(AndroidJUnit4::class)
class MyTripsScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)

    private val zone = ZoneId.of("America/Los_Angeles")
    private val route7 = MyTripRoute("7", "Rainier Beach", "FDB71A", "000000")
    private val routeE = MyTripRoute("E", "Aurora Village", "B5121B", "FFFFFF")

    private fun trip(id: Long, day: Int, hour: Int, minute: Int, minutes: Long, route: MyTripRoute, headsign: String, vehicle: String): MyTrip {
        val start = ZonedDateTime.of(2026, 11, day, hour, minute, 0, 0, zone).toInstant()
        return MyTrip(id, vehicle, "R$id", route, headsign, start, start.plus(Duration.ofMinutes(minutes)))
    }

    // The sheet's trips: two today (the 25th), one yesterday, one on Monday the 23rd.
    private val sheet = MyTripsPage(
        hasMore = false,
        trips = listOf(
            trip(7, 25, 7, 13, 49, route7, "Downtown Seattle", "4317"),
            trip(6, 25, 6, 5, 46, route7, "Mount Baker", "4317"),
            trip(5, 24, 16, 30, 71, routeE, "Aurora Village", "4401"),
            trip(3, 23, 9, 20, 46, route7, "Madrona Park", "4523"),
        ),
    )

    private var list by mutableStateOf(MyTripsList())
    private var today by mutableStateOf(LocalDate.of(2026, 11, 25))
    private var refreshing by mutableStateOf(false)
    private var retries = 0
    private var refreshes = 0
    private val loadMore = mutableListOf<TripsPageRequest>()
    private var moreRetries = 0

    private fun show() = compose.setContent {
        AppTheme(darkTheme = false) {
            Surface {
                MyTripsContent(
                    list = list,
                    refreshing = refreshing,
                    today = today,
                    onBack = {},
                    onRetry = { retries++ },
                    onRefresh = { refreshes++ },
                    onLoadMore = { loadMore += it },
                    onRetryMore = { moreRetries++ },
                    zone = zone,
                )
            }
        }
    }

    // From the top of what scrolls to its bottom, whatever the screen's density: on CI's 160 dpi emulator, a swipe
    // a fixed number of pixels above the middle of the screen began on the top bar and pulled nothing.
    private fun pullDown() = compose.onNode(hasScrollAction()).performTouchInput { swipeDown() }

    private fun said(text: String) = SemanticsMatcher("contentDescription contains $text") { node ->
        node.config.getOrElseNullable(SemanticsProperties.ContentDescription) { null }.orEmpty().any { it.replace(' ', ' ').contains(text) }
    }

    @Test
    fun tripsGoUnderTodayYesterdayAndTheirDate_andTalkBackReadsARowAsOne() {
        list = MyTripsList().receivedFirst(sheet)
        show()

        compose.onNodeWithText(string(R.string.my_trips_today)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.my_trips_yesterday)).assertIsDisplayed()
        compose.onNodeWithText("Monday, November 23").assertIsDisplayed()
        compose.onNode(said("Route 7, Downtown Seattle, 7:13 AM to 8:02 AM · Vehicle 4317")).assertIsDisplayed()
        compose.onNode(said("Route E, Aurora Village, 4:30 PM to 5:41 PM · Vehicle 4401")).assertIsDisplayed()
        compose.onAllNodes(hasClickAction() and said("Vehicle")).assertCountEquals(0)
    }

    // Aaron on #687: left open past midnight, the page moves its titles on.
    @Test
    fun atMidnightTodayBecomesYesterday() {
        list = MyTripsList().receivedFirst(sheet)
        show()

        today = LocalDate.of(2026, 11, 26)
        compose.waitForIdle()

        compose.onAllNodesWithText(string(R.string.my_trips_today)).assertCountEquals(0)
        compose.onNodeWithText(string(R.string.my_trips_yesterday)).assertIsDisplayed()
        compose.onNodeWithText("Tuesday, November 24").assertIsDisplayed()
    }

    @Test
    fun noTripsYet_andAPullStillRefreshes() {
        list = MyTripsList().receivedFirst(MyTripsPage(hasMore = false, trips = emptyList()))
        show()

        compose.onNodeWithText(string(R.string.my_trips_empty_title)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.my_trips_empty_detail)).assertIsDisplayed()
        pullDown()
        compose.waitForIdle()
        assertEquals(1, refreshes)
    }

    @Test
    fun aFailedFirstLoadTakesThePage_withTryAgain() {
        list = MyTripsList().firstFailed()
        show()

        compose.onNodeWithText(string(R.string.my_trips_error_title)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.my_trips_error_detail)).assertIsDisplayed()
        compose.onNode(hasText(string(R.string.my_trips_retry)) and hasClickAction()).performClick()
        assertEquals(1, retries)
    }

    @Test
    fun aPullRefreshes_andAFailedRefreshKeepsTheListUnderTheBanner() {
        list = MyTripsList().receivedFirst(sheet)
        show()
        pullDown()
        compose.waitForIdle()
        assertEquals(1, refreshes)

        list = list.firstFailed()
        compose.waitForIdle()
        compose.onNodeWithText(string(R.string.my_trips_error_title)).assertIsDisplayed()
        compose.onNode(said("Mount Baker")).assertIsDisplayed()
        compose.onNode(hasText(string(R.string.my_trips_retry)) and hasClickAction()).performClick()
        assertEquals(2, refreshes)
    }

    @Test
    fun theEndOfTheListAsksForTheNextPage_andSaysWhenItFails() {
        list = MyTripsList().receivedFirst(sheet.copy(hasMore = true))
        show()
        compose.waitForIdle()
        val next = list.nextPage!!
        assertEquals(listOf(next), loadMore)

        list = list.failed(next)
        compose.waitForIdle()
        // At the end of the list, which runs past a small screen.
        val tryAgain = hasText(string(R.string.my_trips_retry)) and hasClickAction()
        compose.onNode(hasScrollAction()).performScrollToNode(tryAgain)
        compose.onNodeWithText(string(R.string.my_trips_error_title)).assertIsDisplayed()
        compose.onNode(tryAgain).performClick()
        assertEquals(1, moreRetries)
        assertTrue("asked once", loadMore.size == 1)
    }
}
