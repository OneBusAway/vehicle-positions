package org.onebusaway.vehicletracker.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.core.app.ActivityOptionsCompat
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeState
import org.onebusaway.vehicletracker.ui.permissions.PermissionFixes
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.fixOf
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFixes

private const val SETTINGS = "settings"
private const val PERMISSION_CHECK = "permission_check"

/**
 * Location refused for good, as Settings and Permission check learn it. Each is a page of its own, as in the app, and
 * whichever heard Android's answer, the other knows it, however often either opens again.
 */
@RunWith(AndroidJUnit4::class)
class RefusalForGoodTest {
    @get:Rule val compose = createComposeRule()

    /** Notes what Android is asked, and answers only when the test does. */
    private class Prompts : ActivityResultRegistry() {
        val asked = mutableListOf<Int>()

        override fun <I, O> onLaunch(
            requestCode: Int,
            contract: ActivityResultContract<I, O>,
            input: I,
            options: ActivityOptionsCompat?,
        ) {
            asked += requestCode
        }
    }

    private val prompts = Prompts()
    private lateinit var nav: NavHostController

    /** Each page's fixes, as it last composed. */
    private val fixes = mutableMapOf<String, PermissionFixes>()

    private val nothingGranted =
        Grants(Build.VERSION.SDK_INT, fine = false, coarse = false, background = false, notifications = false, batteryExempt = false)
    private val noticeAgreed = LocationNoticeState(agreed = true, server = null, agree = {})

    private fun showPages() {
        val owner = object : ActivityResultRegistryOwner {
            override val activityResultRegistry: ActivityResultRegistry = prompts
        }
        compose.setContent {
            CompositionLocalProvider(LocalActivityResultRegistryOwner provides owner) {
                nav = rememberNavController()
                NavHost(nav, startDestination = SETTINGS) {
                    for (page in listOf(SETTINGS, PERMISSION_CHECK)) {
                        composable(page) {
                            // Android won't show its prompt again, and nothing is granted.
                            fixes[page] =
                                rememberPermissionFixes({}, noticeAgreed, showsRationale = { false }, readGrants = { nothingGranted })
                        }
                    }
                }
            }
        }
    }

    /** Taps Allow location on [page], and Android answers that it is refused, for good. */
    private fun refuseForGoodOn(page: String) {
        val before = prompts.asked.size
        compose.runOnIdle { fixes.getValue(page).fix(PermissionRow.LOCATION) }
        val requestCode = compose.runOnIdle { prompts.asked.drop(before).single() }
        val refused = mapOf(
            Manifest.permission.ACCESS_FINE_LOCATION to false,
            Manifest.permission.ACCESS_COARSE_LOCATION to false,
        )
        compose.runOnIdle { assertTrue("the answer has a page to go to", prompts.dispatchResult(requestCode, refused)) }
    }

    private fun locationFixOn(page: String): Fix = compose.runOnIdle {
        assertEquals("on top", page, nav.currentDestination?.route)
        fixOf(PermissionRow.LOCATION, fixes.getValue(page).onlyInSettings)
    }

    @Test
    fun refusedOnSettings_PermissionCheckOpensTheSettingsForLocation() {
        showPages()
        assertEquals("Android is asked first", Fix.ALLOW, locationFixOn(SETTINGS))

        refuseForGoodOn(SETTINGS)
        assertEquals(Fix.OPEN_SETTINGS, locationFixOn(SETTINGS))

        compose.runOnIdle { nav.navigate(PERMISSION_CHECK) }

        assertEquals(Fix.OPEN_SETTINGS, locationFixOn(PERMISSION_CHECK))
    }

    @Test
    fun refusedOnPermissionCheck_SettingsAndPermissionCheckOpenedAgainKnowIt() {
        showPages()
        compose.runOnIdle { nav.navigate(PERMISSION_CHECK) }
        refuseForGoodOn(PERMISSION_CHECK)

        compose.runOnIdle { nav.popBackStack() }
        assertEquals(Fix.OPEN_SETTINGS, locationFixOn(SETTINGS))
        compose.runOnIdle { nav.navigate(PERMISSION_CHECK) }

        assertEquals(Fix.OPEN_SETTINGS, locationFixOn(PERMISSION_CHECK))
    }
}
