package org.onebusaway.vehicletracker.ui

import android.net.Uri
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
import androidx.navigation.NavBackStackEntry
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
import kotlinx.coroutines.withTimeoutOrNull
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.di.ServiceAnnounceGrace
import org.onebusaway.vehicletracker.ui.login.LoginScreen
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftScreen
import org.onebusaway.vehicletracker.ui.routes.RoutesScreen
import org.onebusaway.vehicletracker.ui.runs.RunsScreen
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreen
import org.onebusaway.vehicletracker.ui.vehicles.VehicleScreen
import java.time.Duration
import javax.inject.Inject

private const val ROUTE_LOGIN = "login"
// Distinct route (rather than an optional nav argument on ROUTE_LOGIN) so `popUpTo`/start-destination
// string matching stays unambiguous; reuses the same LoginScreen composable with a different
// post-login destination — returns to the still-active Tracking screen instead of Vehicles.
private const val ROUTE_LOGIN_REAUTH = "login_reauth"
private const val ROUTE_VEHICLES = "vehicles"
private const val ROUTE_ROUTES = "trip/{vehicleId}"
private const val ROUTE_RUNS = "trip/{vehicleId}/routes/{routeId}"
private const val ROUTE_TRACKING = "tracking"
private const val ROUTE_RESUME = "resume"
// Not private: RunsViewModel reads both out of its SavedStateHandle, and the names have to
// be the ones the nav graph wrote.
internal const val ARG_VEHICLE_ID = "vehicleId"
internal const val ARG_ROUTE_ID = "routeId"

/**
 * A path argument for a string route. A GTFS `route_id` is arbitrary text — spaces, slashes
 * and colons are all legal — and these routes carry their arguments in the path, so an id that
 * went in raw would split the path and match nothing.
 */
private fun arg(value: String): String = Uri.encode(value)

// Reading one back needs no matching decode: NavDeepLink.getMatchingPathArguments already runs
// every captured path argument through NavUriUtils.decode before it reaches the bundle.
private fun NavBackStackEntry.pathArg(name: String): String = arguments?.getString(name).orEmpty()

/** Determines which route the app should land on at launch, based on persisted session/trip state. */
@HiltViewModel
class AppNavViewModel @Inject constructor(
    private val sessionStore: SessionStore,
    private val tripStateStore: TripStateStore,
    private val trackingRepository: TrackingRepository,
    @param:EpochSecondsClock private val clock: () -> Long,
    @param:ServiceAnnounceGrace private val serviceAnnounceGrace: Duration,
) : ViewModel() {
    private val _startDestination = MutableStateFlow<String?>(null)
    val startDestination: StateFlow<String?> = _startDestination.asStateFlow()

    init {
        viewModelScope.launch {
            val session = sessionStore.session.first()
            val activeTrip = tripStateStore.activeTrip.first()
            _startDestination.value = when {
                activeTrip != null && serviceIsRunning() -> ROUTE_TRACKING
                activeTrip != null -> ROUTE_RESUME
                session.hasFreshToken(clock()) -> ROUTE_VEHICLES
                else -> ROUTE_LOGIN
            }
        }
    }

    /**
     * Read off [TrackingRepository] rather than asked of the system: the service runs in this
     * process and marks the repository active when it starts, so a process with no service in it
     * reads inactive. That tells a trip still reporting in the background — task swiped away, or
     * restarted by `START_STICKY` — from one that was interrupted by a force-stop, crash or reboot.
     *
     * The app can open just as the system restarts the service, before its `onStartCommand` has
     * run, so the service gets [serviceAnnounceGrace] to show up before the trip counts as
     * interrupted.
     */
    private suspend fun serviceIsRunning(): Boolean =
        withTimeoutOrNull(serviceAnnounceGrace.toMillis()) { trackingRepository.state.first { it.active } } != null
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
                onVehicleSelected = { vehicleId -> navController.navigate("trip/${arg(vehicleId)}") },
            )
        }
        composable(
            route = ROUTE_ROUTES,
            arguments = listOf(navArgument(ARG_VEHICLE_ID) { type = NavType.StringType }),
        ) { backStackEntry ->
            val vehicleId = backStackEntry.pathArg(ARG_VEHICLE_ID)
            RoutesScreen(
                onRouteSelected = { routeId ->
                    navController.navigate("trip/${arg(vehicleId)}/routes/${arg(routeId)}")
                },
            )
        }
        composable(
            route = ROUTE_RUNS,
            arguments = listOf(
                navArgument(ARG_VEHICLE_ID) { type = NavType.StringType },
                navArgument(ARG_ROUTE_ID) { type = NavType.StringType },
            ),
        ) {
            // vehicleId and routeId reach RunsViewModel through its SavedStateHandle.
            RunsScreen(
                onTripStarted = {
                    navController.navigate(ROUTE_TRACKING) {
                        popUpTo(0) { inclusive = true }
                    }
                },
            )
        }
        composable(ROUTE_RESUME) {
            ResumeShiftScreen(
                onResumed = {
                    navController.navigate(ROUTE_TRACKING) {
                        popUpTo(ROUTE_RESUME) { inclusive = true }
                    }
                },
                onEnded = {
                    navController.navigate(ROUTE_VEHICLES) {
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
