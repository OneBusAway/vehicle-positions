package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
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
import org.onebusaway.vehicletracker.ui.settings.PrivacyContent
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Privacy: what it says, the server it names, and the agency's link only when there is one. */
@RunWith(AndroidJUnit4::class)
class PrivacyScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)

    private var link by mutableStateOf<String?>(null)
    private val opened = mutableListOf<String>()
    private var back = 0

    private fun show(server: String? = "metro.example") = compose.setContent {
        AppTheme(darkTheme = false) {
            Surface { PrivacyContent(server, link, onBack = { back++ }, onOpenPolicy = { opened += it }) }
        }
    }

    @Test
    fun saysWhatIsSent_whoGetsIt_andWhatStays() {
        show()

        compose.onNode(hasText(string(R.string.privacy_title)) and isHeading()).assertIsDisplayed()
        for (id in listOf(
            R.string.privacy_trip_detail, R.string.privacy_position_detail, R.string.privacy_occupancy_detail,
            R.string.privacy_riders_detail, R.string.privacy_account_detail, R.string.privacy_kept_detail,
        )) {
            compose.onNodeWithText(string(id)).performScrollTo().assertIsDisplayed()
        }
        compose.onNodeWithText(string(R.string.privacy_agency_detail, "metro.example"), substring = true).performScrollTo().assertIsDisplayed()
        compose.onAllNodes(hasClickAction()).assertCountEquals(1)
    }

    @Test
    fun withNoServer_itSaysTheServerYouSignedInTo() {
        show(server = null)

        compose.onNodeWithText(string(R.string.privacy_agency_detail_no_server)).performScrollTo().assertIsDisplayed()
    }

    @Test
    fun thePolicyIsThereOnlyWithALink_andATapOpensIt() {
        show()
        compose.onAllNodesWithText(string(R.string.privacy_policy)).assertCountEquals(0)
        compose.onAllNodesWithText(string(R.string.privacy_group_more)).assertCountEquals(0)

        link = "https://metro.example/privacy"
        compose.onNode(hasText(string(R.string.privacy_policy)) and hasClickAction()).performScrollTo().performClick()

        assertEquals(listOf("https://metro.example/privacy"), opened)
    }

    @Test
    fun backLeavesIt() {
        show()

        compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).performClick()

        assertEquals(1, back)
    }

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        link = "https://metro.example/privacy"
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                Box(Modifier.requiredWidth(320.dp).requiredHeight(4000.dp)) {
                    AppTheme(darkTheme = false) { Surface { PrivacyContent("metro.example", link, onBack = {}, onOpenPolicy = {}) } }
                }
            }
        }

        compose.assertNothingIsCutOff()
    }
}
