package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.navigation.NavHostController
import androidx.navigation.NavOptionsBuilder
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Change vehicle, from the run step, on AppNav's routes with stand-ins for the screens. */
@RunWith(AndroidJUnit4::class)
class ChangeVehicleNavTest {
    @get:Rule val compose = createComposeRule()

    private lateinit var navController: NavHostController

    private fun startAt(route: String) {
        compose.setContent {
            navController = rememberNavController()
            NavHost(navController, startDestination = route) {
                composable(ROUTE_VEHICLES) {}
                composable(ROUTE_VEHICLES_CHOSEN) {}
                composable("trip/{vehicleId}") {}
                composable("trip/{vehicleId}/routes/{routeId}") {}
            }
        }
        compose.waitForIdle()
    }

    private fun onTo(route: String, builder: NavOptionsBuilder.() -> Unit = {}) {
        compose.runOnIdle { navController.navigate(route, builder) }
    }

    private fun changeVehicle() {
        compose.runOnIdle { navController.toBusStep() }
        compose.waitForIdle()
    }

    @Test
    fun changingVehicleAfterTheSkip_opensTheStepTheDriverAskedFor() {
        startAt(ROUTE_VEHICLES)
        onTo("trip/4317", skipBusStep)
        onTo("trip/4317/routes/10")
        compose.runOnIdle {
            assertEquals("trip/{vehicleId}", navController.previousBackStackEntry?.destination?.route)
            assertNull("the skip left no bus step behind", navController.getBackStackEntryOrNull(ROUTE_VEHICLES))
        }

        changeVehicle()

        compose.runOnIdle {
            assertEquals(ROUTE_VEHICLES_CHOSEN, navController.currentDestination?.route)
            assertNull("it replaces the flow", navController.previousBackStackEntry)
        }
    }

    @Test
    fun changingVehicleWithTheStepBehind_goesBackToIt() {
        startAt(ROUTE_VEHICLES)
        val busStep = compose.runOnIdle { navController.currentBackStackEntry }
        onTo("trip/4317")
        onTo("trip/4317/routes/10")

        changeVehicle()

        compose.runOnIdle {
            assertSame("the same step, not a fresh one", busStep, navController.currentBackStackEntry)
            assertNull(navController.previousBackStackEntry)
        }
    }

    @Test
    fun changingVehicleAfterAChange_goesBackToThatStep() {
        startAt(ROUTE_VEHICLES_CHOSEN)
        val busStep = compose.runOnIdle { navController.currentBackStackEntry }
        onTo("trip/4317")
        onTo("trip/4317/routes/10")

        changeVehicle()

        compose.runOnIdle {
            assertSame("the same step, not a fresh one", busStep, navController.currentBackStackEntry)
            assertNull(navController.previousBackStackEntry)
        }
    }

    private fun NavHostController.getBackStackEntryOrNull(route: String) =
        try {
            getBackStackEntry(route)
        } catch (e: IllegalArgumentException) {
            null
        }
}
