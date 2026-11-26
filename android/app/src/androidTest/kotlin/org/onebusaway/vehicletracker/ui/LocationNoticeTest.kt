package org.onebusaway.vehicletracker.ui

import android.Manifest
import android.os.Build
import android.view.KeyEvent
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.core.app.ActivityOptionsCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeContent
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeHost
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeState
import org.onebusaway.vehicletracker.ui.permissions.PermissionFixes
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFixes
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** The location notice Google Play wants before Android asks for location, and Permission check's asks behind it. */
@RunWith(AndroidJUnit4::class)
class LocationNoticeTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)

    private fun button(id: Int) = compose.onNode(hasText(string(id)) and hasClickAction())

    @Test
    fun itSaysWhatGooglePlayAsks_andNamesTheServer() {
        compose.setContent { AppTheme(darkTheme = false) { Surface { LocationNoticeContent("metro.example", onAgree = {}, onNotNow = {}) } } }

        compose.onNodeWithText(string(R.string.location_notice_title)).assertIsDisplayed()
        val text = string(R.string.location_notice_text, string(R.string.app_name))
        assertTrue("location, and in the background", "location data" in text && "even when the app is closed or not in use" in text)
        compose.onNodeWithText(text).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.location_notice_when)).performScrollTo().assertIsDisplayed()
        compose.onNodeWithText(string(R.string.location_notice_to, "metro.example")).performScrollTo().assertIsDisplayed()
        button(R.string.location_notice_agree).assertIsDisplayed()
        button(R.string.location_notice_not_now).assertIsDisplayed()
    }

    @Test
    fun withNoServerItNamesTheAgency() {
        compose.setContent { AppTheme(darkTheme = false) { Surface { LocationNoticeContent(null, onAgree = {}, onNotNow = {}) } } }

        compose.onNodeWithText(string(R.string.location_notice_to_agency)).performScrollTo().assertIsDisplayed()
    }

    // Google Play: going back or away is not agreeing.
    @Test
    fun onlyAgreeAgrees_andNotNowAndBackLeaveIt() {
        var agreed = 0
        var left = 0
        var visible by mutableStateOf(true)
        compose.setContent {
            AppTheme(darkTheme = false) {
                Surface { LocationNoticeHost(visible, "metro.example", onAgree = { agreed++ }, onNotNow = { left++; visible = false }) {} }
            }
        }

        button(R.string.location_notice_not_now).performClick()
        compose.runOnIdle { assertEquals(listOf(0, 1), listOf(agreed, left)) }

        visible = true
        compose.waitForIdle()
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        compose.runOnIdle { assertEquals(listOf(0, 2), listOf(agreed, left)) }

        visible = true
        compose.waitForIdle()
        button(R.string.location_notice_agree).performClick()
        compose.runOnIdle { assertEquals(listOf(1, 2), listOf(agreed, left)) }
    }

    /** Notes what Android is asked, and never answers. */
    private class Prompts : ActivityResultRegistry() {
        val asked = mutableListOf<Any?>()

        override fun <I, O> onLaunch(requestCode: Int, contract: ActivityResultContract<I, O>, input: I, options: ActivityOptionsCompat?) {
            asked += input
        }
    }

    private val prompts = Prompts()
    private var noticeAgreed by mutableStateOf<Boolean?>(false)
    private lateinit var fixes: PermissionFixes

    private fun permissionCheck() {
        val owner = object : ActivityResultRegistryOwner {
            override val activityResultRegistry: ActivityResultRegistry = prompts
        }
        compose.setContent {
            CompositionLocalProvider(LocalActivityResultRegistryOwner provides owner) {
                fixes = rememberPermissionFixes(onAnswer = {}, notice = LocationNoticeState(noticeAgreed, "metro.example") { noticeAgreed = true })
            }
        }
    }

    private fun asksForLocation(): Boolean = (prompts.asked.lastOrNull() as? Array<*>)?.contains(Manifest.permission.ACCESS_FINE_LOCATION) == true

    @Test
    fun aLocationRowWaitsForTheNotice_andAgreeAsksForIt() {
        permissionCheck()
        compose.runOnIdle { fixes.fix(PermissionRow.LOCATION) }
        compose.runOnIdle {
            assertEquals(PermissionRow.LOCATION, fixes.noticeFor)
            assertTrue("nothing asked yet", prompts.asked.isEmpty())
        }

        compose.runOnIdle { fixes.agreeToNotice() }

        compose.runOnIdle {
            assertNull(fixes.noticeFor)
            assertEquals(true, noticeAgreed)
            assertEquals(1, prompts.asked.size)
            assertTrue(asksForLocation())
        }
    }

    @Test
    fun notNowAsksNothing() {
        permissionCheck()
        compose.runOnIdle { fixes.fix(PermissionRow.BACKGROUND) }
        compose.runOnIdle { assertEquals(PermissionRow.BACKGROUND, fixes.noticeFor) }

        compose.runOnIdle { fixes.declineNotice() }

        compose.runOnIdle {
            assertNull(fixes.noticeFor)
            assertEquals(false, noticeAgreed)
            assertTrue(prompts.asked.isEmpty())
        }
    }

    @Test
    fun onceAgreed_aLocationRowIsAskedAtOnce() {
        noticeAgreed = true
        permissionCheck()
        compose.runOnIdle { fixes.fix(PermissionRow.PRECISE) }

        compose.runOnIdle {
            assertNull(fixes.noticeFor)
            assertTrue(asksForLocation())
        }
    }

    // Before Android 13 notifications are no permission, and there is nothing to ask.
    @Test
    fun notificationsNeedNoNotice() {
        assumeTrue(Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        permissionCheck()
        compose.runOnIdle { fixes.fix(PermissionRow.NOTIFICATIONS) }

        compose.runOnIdle {
            assertNull(fixes.noticeFor)
            assertEquals(1, prompts.asked.size)
            assertTrue(Manifest.permission.POST_NOTIFICATIONS in prompts.asked.single() as Array<*>)
        }
    }
}
