package org.onebusaway.vehicletracker.ui

import android.Manifest
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.core.app.ActivityOptionsCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.ui.permissions.PermissionFlowController
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.PermissionStep
import org.onebusaway.vehicletracker.ui.permissions.RowState
import org.onebusaway.vehicletracker.ui.permissions.rememberPermissionFlow

/**
 * The checklist before a trip, through a rotation that recreates the screen. Android's prompts go through a registry
 * that outlives the screen, as the activity's does, so the answer to a prompt that was out comes back to the screen
 * that replaced it.
 */
@RunWith(AndroidJUnit4::class)
class PermissionFlowRestoreTest {
    @get:Rule val compose = createComposeRule()

    /** Notes what Android is asked, and answers only when the test does. */
    private class Prompts : ActivityResultRegistry() {
        val asked = mutableListOf<Pair<Int, Any?>>()

        override fun <I, O> onLaunch(
            requestCode: Int,
            contract: ActivityResultContract<I, O>,
            input: I,
            options: ActivityOptionsCompat?,
        ) {
            asked += requestCode to input
        }
    }

    private val prompts = Prompts()
    private val restoration = StateRestorationTester(compose)
    private lateinit var flow: PermissionFlowController

    /** Whether Android would show a rationale for location: true once it has been refused, false once refused for good. */
    private var rationale = false

    /** A run tapped on a phone that has not granted everything yet. */
    private fun showChecklist() {
        val owner = object : ActivityResultRegistryOwner {
            override val activityResultRegistry: ActivityResultRegistry = prompts
        }
        restoration.setContent {
            CompositionLocalProvider(LocalActivityResultRegistryOwner provides owner) {
                flow = rememberPermissionFlow(onReady = {}, showsLocationRationale = { rationale })
            }
        }
        compose.runOnIdle { flow.start() }
        compose.runOnIdle { assertTrue("the checklist shows before Android is asked", flow.visible) }
    }

    @Test
    fun theChecklistStaysThroughARotation() {
        showChecklist()
        val rows = compose.runOnIdle { flow.checklist }

        restoration.emulateSavedInstanceStateRestore()

        compose.runOnIdle {
            assertTrue(flow.visible)
            assertEquals(rows, flow.checklist)
        }
    }

    @Test
    fun aPromptOutAtTheRotationIsNotAskedTwice_andItsAnswerMovesTheChecklistOn() {
        showChecklist()
        compose.runOnIdle { flow.onContinue() }
        val (requestCode, input) = compose.runOnIdle { prompts.asked.single() }
        assertTrue(Manifest.permission.ACCESS_FINE_LOCATION in input as Array<*>)

        restoration.emulateSavedInstanceStateRestore()

        compose.runOnIdle {
            assertTrue(flow.visible)
            assertEquals(PermissionStep.REQUEST_LOCATION, flow.flow.step)
            assertEquals("asked once", 1, prompts.asked.size)
        }
        val answer = mapOf(Manifest.permission.ACCESS_FINE_LOCATION to true, Manifest.permission.ACCESS_COARSE_LOCATION to true)
        compose.runOnIdle { assertTrue("the answer has a screen to go to", prompts.dispatchResult(requestCode, answer)) }
        compose.runOnIdle {
            assertNotEquals(PermissionStep.REQUEST_LOCATION, flow.flow.step)
            assertEquals("the next step is asked", 2, prompts.asked.size)
        }
    }

    @Test
    fun aSecondRefusalAnsweredAfterARotationIsForGood() {
        rationale = true
        showChecklist()
        compose.runOnIdle { flow.onContinue() }
        val (requestCode, _) = compose.runOnIdle { prompts.asked.single() }

        restoration.emulateSavedInstanceStateRestore()

        // Refused a second time, so Android stops showing the rationale and will not ask again.
        rationale = false
        val answer = mapOf(Manifest.permission.ACCESS_FINE_LOCATION to false, Manifest.permission.ACCESS_COARSE_LOCATION to false)
        compose.runOnIdle { assertTrue("the answer has a screen to go to", prompts.dispatchResult(requestCode, answer)) }
        compose.runOnIdle {
            assertEquals(PermissionStep.LOCATION_DENIED, flow.flow.step)
            assertEquals(
                "needed in settings, not asked again",
                RowState.NEEDED_IN_SETTINGS,
                flow.checklist.single { it.row == PermissionRow.LOCATION }.state,
            )
        }
    }
}
