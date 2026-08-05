package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.ui.login.LoginScreen
import org.onebusaway.vehicletracker.ui.trip.TripSetupScreen
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreen
import org.onebusaway.vehicletracker.ui.vehicles.VehicleScreen
import javax.inject.Inject

private const val ROUTE_LOGIN = "login"
// Distinct route (rather than an optional nav argument on ROUTE_LOGIN) so `popUpTo`/start-destination
// string matching stays unambiguous; reuses the same LoginScreen composable with a different
// post-login destination — returns to the still-active Tracking screen instead of Vehicles.
private const val ROUTE_LOGIN_REAUTH = "login_reauth"
private const val ROUTE_VEHICLES = "vehicles"
private const val ROUTE_TRIP = "trip/{vehicleId}"
private const val ROUTE_TRACKING = "tracking"
private const val ARG_VEHICLE_ID = "vehicleId"

/** Determines which route the app should land on at launch, based on persisted session/trip state. */
@HiltViewModel
class AppNavViewModel @Inject constructor(
    private val sessionStore: SessionStore,
    private val tripStateStore: TripStateStore,
    @param:EpochSecondsClock private val clock: () -> Long,
) : ViewModel() {
    private val _startDestination = MutableStateFlow<String?>(null)
    val startDestination: StateFlow<String?> = _startDestination.asStateFlow()

    init {
        viewModelScope.launch {
            val session = sessionStore.session.first()
            val activeTrip = tripStateStore.activeTrip.first()
            _startDestination.value = when {
                activeTrip != null -> ROUTE_TRACKING
                session.hasFreshToken(clock()) -> ROUTE_VEHICLES
                else -> ROUTE_LOGIN
            }
        }
    }
}

@Composable
fun AppNav(navViewModel: AppNavViewModel = hiltViewModel()) {
    val startDestination by navViewModel.startDestination.collectAsStateWithLifecycle()
    val destination = startDestination

    if (destination == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
        }
        return
    }

    val navController = rememberNavController()
    NavHost(navController = navController, startDestination = destination) {
        composable(ROUTE_LOGIN) {
            LoginScreen(
                onLoginSuccess = {
                    navController.navigate(ROUTE_VEHICLES) {
                        popUpTo(ROUTE_LOGIN) { inclusive = true }
                    }
                },
            )
        }
        composable(ROUTE_LOGIN_REAUTH) {
            LoginScreen(
                onLoginSuccess = {
                    // Trip state was never cleared; just return to the still-active Tracking screen.
                    navController.popBackStack(ROUTE_TRACKING, /* inclusive = */ false)
                },
            )
        }
        composable(ROUTE_VEHICLES) {
            VehicleScreen(
                onVehicleSelected = { vehicleId -> navController.navigate("trip/$vehicleId") },
            )
        }
        composable(
            route = ROUTE_TRIP,
            arguments = listOf(navArgument(ARG_VEHICLE_ID) { type = NavType.StringType }),
        ) { backStackEntry ->
            val vehicleId = backStackEntry.arguments?.getString(ARG_VEHICLE_ID).orEmpty()
            TripSetupScreen(
                vehicleId = vehicleId,
                onTripStarted = {
                    navController.navigate(ROUTE_TRACKING) {
                        popUpTo(0) { inclusive = true }
                    }
                },
            )
        }
        composable(ROUTE_TRACKING) {
            TrackingScreen(
                onTripEnded = {
                    navController.navigate(ROUTE_VEHICLES) {
                        popUpTo(0) { inclusive = true }
                    }
                },
                onReauthRequired = { navController.navigate(ROUTE_LOGIN_REAUTH) },
            )
        }
    }
}
