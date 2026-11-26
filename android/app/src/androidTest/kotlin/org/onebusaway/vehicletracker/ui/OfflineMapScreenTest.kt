package org.onebusaway.vehicletracker.ui

import android.text.format.Formatter
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.map.MapDetails
import org.onebusaway.vehicletracker.ui.settings.OfflineMap
import org.onebusaway.vehicletracker.ui.settings.OfflineMapContent
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Offline map: where the map stands in each state, and what each button does. */
@RunWith(AndroidJUnit4::class)
class OfflineMapScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)
    private fun size(bytes: Long) = Formatter.formatShortFileSize(context, bytes)

    private val ready = OfflineMap.Ready(MapDetails(48_000_000, 1_789_000_000_000L, 1_790_000_000L))
    private var map by mutableStateOf<OfflineMap>(ready)
    private val asked = mutableListOf<String>()
    private var back = 0

    @Composable
    private fun Content() {
        AppTheme(darkTheme = false) {
            Surface {
                OfflineMapContent(
                    map = map,
                    onBack = { back++ },
                    onGetAgain = { asked += "getAgain" },
                    onDelete = { asked += "delete" },
                    onTryAgain = { asked += "tryAgain" },
                    nowEpochSec = 1_790_000_600L,
                )
            }
        }
    }

    private fun button(name: Int) = compose.onNode(hasText(string(name)) and hasClickAction())

    private fun buttons(): List<String> = compose.onAllNodes(hasClickAction()).fetchSemanticsNodes()
        .mapNotNull { node -> node.config.getOrNull(SemanticsProperties.Text)?.joinToString { it.text } }

    @Test
    fun ready_hasItsFacts_andGetItAgainAndDelete() {
        compose.setContent { Content() }

        compose.onNode(hasText(string(R.string.offline_map_title)) and isHeading()).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_ready_detail)).assertIsDisplayed()
        compose.onNodeWithText(size(48_000_000)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_fetched)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_last_checked)).assertIsDisplayed()
        assertEquals(listOf(string(R.string.offline_map_get_again), string(R.string.offline_map_delete)), buttons())

        button(R.string.offline_map_get_again).performScrollTo().performClick()
        assertEquals(listOf("getAgain"), asked)
    }

    @Test
    fun delete_asksFirst_andCancelDeletesNothing() {
        compose.setContent { Content() }

        button(R.string.offline_map_delete).performScrollTo().performClick()
        compose.onNode(isDialog()).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_delete_message)).assertIsDisplayed()
        button(R.string.offline_map_delete_cancel).performClick()
        compose.onAllNodes(isDialog()).assertCountEquals(0)
        assertEquals(emptyList<String>(), asked)

        button(R.string.offline_map_delete).performScrollTo().performClick()
        button(R.string.offline_map_delete_confirm).performClick()
        compose.onAllNodes(isDialog()).assertCountEquals(0)
        assertEquals(listOf("delete"), asked)
    }

    @Test
    fun eachStateSaysWhereTheMapStands_withAtMostItsOwnButton() {
        compose.setContent { Content() }

        map = OfflineMap.Downloading(20_000_000, 48_000_000)
        compose.onNodeWithText(string(R.string.offline_map_downloading, 41)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_downloaded_of, size(20_000_000), size(48_000_000))).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_line_downloading)).assertIsDisplayed()
        assertEquals("no buttons while it downloads", emptyList<String>(), buttons())

        map = OfflineMap.NoSpace(48_000_000)
        compose.onNodeWithText(string(R.string.tracking_street_map_no_space)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_needs, size(48_000_000))).assertIsDisplayed()
        button(R.string.offline_map_try_again).performClick()

        map = OfflineMap.NotDownloaded(byItself = true)
        compose.onNodeWithText(string(R.string.offline_map_tries_by_itself)).assertIsDisplayed()
        button(R.string.offline_map_try_again_now).performClick()

        map = OfflineMap.Unusable(needsHttps = true)
        compose.onNodeWithText(string(R.string.offline_map_needs_https)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_tell_agency)).assertIsDisplayed()
        assertEquals(emptyList<String>(), buttons())

        map = OfflineMap.Deleted
        compose.onNodeWithText(string(R.string.offline_map_deleted)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.offline_map_line_deleted)).assertIsDisplayed()
        button(R.string.offline_map_get).performClick()

        assertEquals(listOf("tryAgain", "tryAgain", "getAgain"), asked)
        compose.onAllNodesWithText(string(R.string.offline_map_last_checked)).assertCountEquals(0)
    }

    @Test
    fun backLeavesIt() {
        compose.setContent { Content() }

        compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).performClick()

        assertEquals(1, back)
    }

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                Box(Modifier.requiredWidth(320.dp).requiredHeight(3000.dp)) { Content() }
            }
        }

        compose.assertNothingIsCutOff()
    }
}
