package org.onebusaway.vehicletracker.ui

import android.net.Uri
import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.dropUnlessResumed
import androidx.lifecycle.viewModelScope
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavOptionsBuilder
import androidx.navigation.NavType
import androidx.navigation.compose.DefaultNavTransitions
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
import org.onebusaway.vehicletracker.data.OfflineFixQueue
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.offlineQueueAccount
import org.onebusaway.vehicletracker.di.EpochSecondsClock
import org.onebusaway.vehicletracker.di.ServiceAnnounceGrace
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.account.AccountViewModel
import org.onebusaway.vehicletracker.ui.login.LoginScreen
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftScreen
import org.onebusaway.vehicletracker.ui.routes.RouteItem
import org.onebusaway.vehicletracker.ui.routes.RoutesScreen
import org.onebusaway.vehicletracker.ui.routes.routeColorHex
import org.onebusaway.vehicletracker.ui.runs.RunsScreen
import org.onebusaway.vehicletracker.ui.theme.SharedAxisSlide
import org.onebusaway.vehicletracker.ui.theme.fadeThroughIn
import org.onebusaway.vehicletracker.ui.theme.fadeThroughOut
import org.onebusaway.vehicletracker.ui.theme.sharedAxisIn
import org.onebusaway.vehicletracker.ui.theme.sharedAxisOut
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreen
import org.onebusaway.vehicletracker.ui.vehicles.VehicleScreen
import java.time.Duration
import javax.inject.Inject

private const val ROUTE_LOGIN = "login"
// Distinct route (rather than an optional nav argument on ROUTE_LOGIN) so `popUpTo`/start-destination
// string matching stays unambiguous; reuses the same LoginScreen composable with a different
// post-login destination — returns to the still-active Tracking screen instead of Vehicles.
private const val ROUTE_LOGIN_REAUTH = "login_reauth"
internal const val ROUTE_VEHICLES = "vehicles"
// The same bus step, opened because the driver asked to change bus. A distinct route, as for ROUTE_LOGIN_REAUTH, so
// skipBusStep's `popUpTo` still matches only the step the app opened by itself.
internal const val ROUTE_VEHICLES_CHOSEN = "vehicles_chosen"
private const val ROUTE_ROUTES = "trip/{vehicleId}"
// The route's badge rides along as optional arguments: the runs reply names only the route's id.
private const val ROUTE_RUNS = "trip/{vehicleId}/routes/{routeId}?name={routeName}&color={routeColor}&text={routeTextColor}"
private const val ROUTE_TRACKING = "tracking"
private const val ROUTE_RESUME = "resume"
// Not private: RunsViewModel reads them out of its SavedStateHandle, and the names have to
// be the ones the nav graph wrote.
internal const val ARG_VEHICLE_ID = "vehicleId"
internal const val ARG_ROUTE_ID = "routeId"
internal const val ARG_ROUTE_NAME = "routeName"
internal const val ARG_ROUTE_COLOR = "routeColor"
internal const val ARG_ROUTE_TEXT_COLOR = "routeTextColor"
// Not private, for LoginViewModel, as above.
internal const val ARG_SIGN_IN_AGAIN = "again"

/**
 * A path argument for a string route. A GTFS `route_id` is arbitrary text — spaces, slashes
 * and colons are all legal — and these routes carry their arguments in the path, so an id that
 * went in raw would split the path and match nothing.
 */
private fun arg(value: String): String = Uri.encode(value)

// Reading one back needs no matching decode: NavDeepLink.getMatchingPathArguments already runs
// every captured path argument through NavUriUtils.decode before it reaches the bundle.
private fun NavBackStackEntry.pathArg(name: String): String = arguments?.getString(name).orEmpty()

/**
 * [dropUnlessResumed] for a tap that carries a value, such as the bus tapped, which the library's version cannot
 * take. The tap does nothing unless this screen is resumed, and it is not while a screen change is under way, so a
 * double tap cannot open two screens.
 */
internal fun <T> NavBackStackEntry.ifResumed(block: (T) -> Unit): (T) -> Unit = { value ->
    if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) block(value)
}

private val SETUP_STEPS = setOf(ROUTE_VEHICLES, ROUTE_VEHICLES_CHOSEN, ROUTE_ROUTES, ROUTE_RUNS)

private fun AnimatedContentTransitionScope<NavBackStackEntry>.betweenSetupSteps(): Boolean =
    initialState.destination.route in SETUP_STEPS && targetState.destination.route in SETUP_STEPS

/** Signing in is the step before choosing the bus, so it goes forward along the same axis. */
private fun AnimatedContentTransitionScope<NavBackStackEntry>.fromSignInToBuses(): Boolean =
    initialState.destination.route == ROUTE_LOGIN && targetState.destination.route == ROUTE_VEHICLES

private fun AnimatedContentTransitionScope<NavBackStackEntry>.toOrFromTracking(): Boolean =
    initialState.destination.route == ROUTE_TRACKING || targetState.destination.route == ROUTE_TRACKING

/** Whether the bus step is behind this screen. It is not once the one-bus skip has taken its place. */
private fun NavController.hasBusStep(): Boolean = listOf(ROUTE_VEHICLES, ROUTE_VEHICLES_CHOSEN).any { route ->
    try {
        getBackStackEntry(route)
        true
    } catch (e: IllegalArgumentException) {
        false
    }
}

/**
 * Back to the bus step, to choose another bus. After the one-bus skip there is no bus step to go back to, so a fresh
 * one replaces the flow: it loads the assignments again, and shows them even if there is still only one.
 */
internal fun NavController.toBusStep() {
    if (!popBackStack(ROUTE_VEHICLES, inclusive = false) && !popBackStack(ROUTE_VEHICLES_CHOSEN, inclusive = false)) {
        navigate(ROUTE_VEHICLES_CHOSEN) { popUpTo(0) { inclusive = true } }
    }
}

/**
 * Only the step the app opened by itself skips. One the driver asked for shows the one vehicle, rather than bouncing
 * back. Nor does it skip once the driver has confirmed Sign out: the assignments can land behind the Sign out dialog,
 * and the skip would take a driver who is signing out on to the route step.
 */
internal fun skipsOnlyVehicle(route: String?, account: AccountUiState): Boolean =
    route == ROUTE_VEHICLES && !account.signingOut

/**
 * How the app leaves the bus step when the driver has only one bus: the route step takes its place. Left on the back
 * stack, the bus step would be where Back lands, and it would skip straight forward again.
 */
internal val skipBusStep: NavOptionsBuilder.() -> Unit = { popUpTo(ROUTE_VEHICLES) { inclusive = true } }

/**
 * Off to Sign in once the session is over, ended by the server or by Sign out: nothing behind it still works, so nothing
 * stays behind it.
 */
internal val toSignIn: NavOptionsBuilder.() -> Unit = { popUpTo(0) { inclusive = true } }

/** Determines which route the app should land on at launch, based on persisted session/trip state. */
@HiltViewModel
class AppNavViewModel @Inject constructor(
    private val sessionStore: SessionStore,
    private val tripStateStore: TripStateStore,
    private val trackingRepository: TrackingRepository,
    private val offlineQueue: OfflineFixQueue,
    @param:EpochSecondsClock private val clock: () -> Long,
    @param:ServiceAnnounceGrace private val serviceAnnounceGrace: Duration,
) : ViewModel() {
    private val _startDestination = MutableStateFlow<String?>(null)
    val startDestination: StateFlow<String?> = _startDestination.asStateFlow()

    init {
        viewModelScope.launch {
            val session = sessionStore.session.first()
            claimQueueForRestoredDriver(session)
            val activeTrip = tripStateStore.activeTrip.first()
            _startDestination.value = when {
                activeTrip != null && serviceIsRunning() -> ROUTE_TRACKING
                activeTrip != null -> ROUTE_RESUME
                else -> withNoTrip(session)
            }
        }
    }

    /**
     * Where Resume goes once it has ended the trip, as the app would open with no trip. Sign in when nobody is signed in
     * to choose the next one, as on a phone restored from backup, which keeps the trip but not the session.
     */
    suspend fun routeAfterResumeEnds(): String = withNoTrip(sessionStore.session.first())

    private fun withNoTrip(session: Session): String = if (session.isSignedIn(clock())) ROUTE_VEHICLES else ROUTE_LOGIN

    /**
     * A session signed in before the queue recorded owners has none on file, so this driver's next sign-in would drop
     * their queued fixes. Done before the first screen shows, so Sign in can't claim the queue first.
     */
    private suspend fun claimQueueForRestoredDriver(session: Session) {
        val serverUrl = session.serverUrl ?: return
        val email = session.email ?: return
        offlineQueue.claimIfUnowned(offlineQueueAccount(serverUrl, email))
    }

    /**
     * Read off [TrackingRepository] rather than asked of the system: the service runs in this
     * process and marks the repository active when it starts, so a process with no service in it
     * reads inactive. That tells a trip still reporting in the background (task swiped away, or
     * restarted by `START_STICKY`) from one that was interrupted by a force-stop, crash or reboot.
     *
     * The app can open just as the system restarts the service, before its `onStartCommand` has
     * run, so the service gets [serviceAnnounceGrace] to show up before the trip counts as
     * interrupted.
     */
    private suspend fun serviceIsRunning(): Boolean =
        withTimeoutOrNull(serviceAnnounceGrace.toMillis()) { trackingRepository.state.first { it.active } } != null
}

/**
 * Keeps a screen clear of the system bars and the keyboard. Every screen but Tracking sits in one:
 * Tracking draws its map under the bars and keeps its own cards clear of them.
 */
@Composable
private fun ClearOfSystemBars(content: @Composable () -> Unit) {
    Box(Modifier.safeDrawingPadding()) { content() }
}

/** Going back a setup step, or out of Tracking; null for the screens that keep the default transitions. */
private fun AnimatedContentTransitionScope<NavBackStackEntry>.popEnter(slidePx: Int): EnterTransition? = when {
    betweenSetupSteps() -> sharedAxisIn(SlideDirection.End, slidePx)
    toOrFromTracking() -> fadeThroughIn()
    else -> null
}

private fun AnimatedContentTransitionScope<NavBackStackEntry>.popExit(slidePx: Int): ExitTransition? = when {
    betweenSetupSteps() -> sharedAxisOut(SlideDirection.End, slidePx)
    toOrFromTracking() -> fadeThroughOut()
    else -> null
}

@Composable
fun AppNav(modifier: Modifier = Modifier, navViewModel: AppNavViewModel = hiltViewModel()) {
    val startDestination by navViewModel.startDestination.collectAsStateWithLifecycle()
    val destination = startDestination
    // MainActivity keeps the launch screen up until this is known.
    if (destination == null) return

    val navController = rememberNavController()
    val slidePx = with(LocalDensity.current) { SharedAxisSlide.roundToPx() }
    // Signing in and the steps of choosing a trip slide along one axis; starting the trip and leaving it fade through
    // instead, because they change the task. The rest keep the defaults. The back gesture previews the same pop it
    // ends in.
    NavHost(
        navController = navController,
        startDestination = destination,
        modifier = modifier,
        enterTransition = {
            when {
                betweenSetupSteps() || fromSignInToBuses() -> sharedAxisIn(SlideDirection.Start, slidePx)
                toOrFromTracking() -> fadeThroughIn()
                else -> DefaultNavTransitions.enterTransition(this)
            }
        },
        exitTransition = {
            when {
                betweenSetupSteps() || fromSignInToBuses() -> sharedAxisOut(SlideDirection.Start, slidePx)
                toOrFromTracking() -> fadeThroughOut()
                else -> DefaultNavTransitions.exitTransition(this)
            }
        },
        popEnterTransition = { popEnter(slidePx) ?: DefaultNavTransitions.enterTransition(this) },
        popExitTransition = { popExit(slidePx) ?: DefaultNavTransitions.exitTransition(this) },
        predictivePopEnterTransition = { swipeEdge ->
            popEnter(slidePx) ?: DefaultNavTransitions.predictivePopEnterTransition(this, swipeEdge)
        },
        predictivePopExitTransition = { swipeEdge ->
            popExit(slidePx) ?: DefaultNavTransitions.predictivePopExitTransition(this, swipeEdge)
        },
    ) {
        composable(ROUTE_LOGIN) {
            ClearOfSystemBars {
                LoginScreen(
                    onLoginSuccess = {
                        navController.navigate(ROUTE_VEHICLES) {
                            popUpTo(ROUTE_LOGIN) { inclusive = true }
                        }
                    },
                )
            }
        }
        composable(
            route = ROUTE_LOGIN_REAUTH,
            // Read by LoginViewModel, which shows the trip that is still open and asks only for the password.
            arguments = listOf(navArgument(ARG_SIGN_IN_AGAIN) { type = NavType.BoolType; defaultValue = true }),
        ) {
            ClearOfSystemBars {
                LoginScreen(
                    onLoginSuccess = {
                        // Trip state was never cleared; back to the still-active Tracking screen, or to Resume when
                        // that is where the sign-in was asked for.
                        if (!navController.popBackStack(ROUTE_TRACKING, /* inclusive = */ false)) {
                            navController.popBackStack(ROUTE_RESUME, /* inclusive = */ false)
                        }
                    },
                )
            }
        }
        vehicleStep(ROUTE_VEHICLES, navController)
        vehicleStep(ROUTE_VEHICLES_CHOSEN, navController)
        composable(
            route = ROUTE_ROUTES,
            arguments = listOf(navArgument(ARG_VEHICLE_ID) { type = NavType.StringType }),
        ) { backStackEntry ->
            val vehicleId = backStackEntry.pathArg(ARG_VEHICLE_ID)
            // A driver whose only bus was chosen for them has no bus step to go back to: no Back, and the bus chip
            // only says which bus it is.
            val hasBusStep = remember(backStackEntry) { navController.hasBusStep() }
            val backToBuses = if (hasBusStep) dropUnlessResumed { navController.popBackStack() } else null
            val openRuns = remember(backStackEntry) {
                backStackEntry.ifResumed<RouteItem> { route ->
                    val badge = route.badge
                    navController.navigate(
                        "trip/${arg(vehicleId)}/routes/${arg(route.id)}?name=${arg(badge.shortName)}" +
                            "&color=${arg(routeColorHex(badge.color))}&text=${arg(routeColorHex(badge.textColor))}",
                    )
                }
            }
            ClearOfSystemBars {
                RoutesScreen(
                    busId = vehicleId,
                    onBack = backToBuses,
                    onBusClick = backToBuses,
                    onRouteSelected = openRuns,
                    // Not a tap: it fires when the reply lands, and a guard would drop it.
                    onSessionEnded = { navController.navigate(ROUTE_LOGIN, toSignIn) },
                    // Not a tap either: it fires once the phone has forgotten the driver.
                    onSignedOut = { navController.navigate(ROUTE_LOGIN, toSignIn) },
                )
            }
        }
        composable(
            route = ROUTE_RUNS,
            arguments = listOf(
                navArgument(ARG_VEHICLE_ID) { type = NavType.StringType },
                navArgument(ARG_ROUTE_ID) { type = NavType.StringType },
                navArgument(ARG_ROUTE_NAME) { defaultValue = "" },
                navArgument(ARG_ROUTE_COLOR) { defaultValue = "" },
                navArgument(ARG_ROUTE_TEXT_COLOR) { defaultValue = "" },
            ),
        ) { backStackEntry ->
            val hasBusStep = remember(backStackEntry) { navController.hasBusStep() }
            ClearOfSystemBars {
                // The arguments reach RunsViewModel through its SavedStateHandle.
                RunsScreen(
                    // Not a tap: the server confirms the start, perhaps after the driver has locked the phone, and
                    // a guard would leave the trip started with the run step still on screen.
                    onTripStarted = {
                        navController.navigate(ROUTE_TRACKING) {
                            popUpTo(0) { inclusive = true }
                        }
                    },
                    onBack = dropUnlessResumed { navController.popBackStack() },
                    onBusClick = if (hasBusStep) dropUnlessResumed { navController.toBusStep() } else null,
                    onRouteClick = dropUnlessResumed { navController.popBackStack(ROUTE_ROUTES, inclusive = false) },
                    onChangeBus = dropUnlessResumed { navController.toBusStep() },
                    onChooseAnotherRoute = dropUnlessResumed { navController.popBackStack() },
                    // Not a tap: it fires when the reply lands, and a guard would drop it.
                    onSessionEnded = { navController.navigate(ROUTE_LOGIN, toSignIn) },
                    // Not a tap either: it fires once the phone has forgotten the driver.
                    onSignedOut = { navController.navigate(ROUTE_LOGIN, toSignIn) },
                )
            }
        }
        composable(ROUTE_RESUME) {
            val scope = rememberCoroutineScope()
            ClearOfSystemBars {
                ResumeShiftScreen(
                    onResumed = dropUnlessResumed {
                        navController.navigate(ROUTE_TRACKING) {
                            popUpTo(ROUTE_RESUME) { inclusive = true }
                        }
                    },
                    onSignInFirst = dropUnlessResumed { navController.navigate(ROUTE_LOGIN_REAUTH) },
                    // Not a tap: the server confirms the end, perhaps after the driver has locked the phone.
                    onEnded = {
                        scope.launch {
                            navController.navigate(navViewModel.routeAfterResumeEnds()) {
                                popUpTo(0) { inclusive = true }
                            }
                        }
                    },
                )
            }
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

/** The bus step, under either of its routes: they differ only in whether one bus skips it. */
private fun NavGraphBuilder.vehicleStep(route: String, navController: NavController) {
    composable(route) { backStackEntry ->
        // The same view model as the account menu's on this step, so it knows when a sign-out has begun.
        val account: AccountViewModel = hiltViewModel()
        val openRoutes = remember(backStackEntry) {
            backStackEntry.ifResumed<String> { vehicleId -> navController.navigate("trip/${arg(vehicleId)}") }
        }
        ClearOfSystemBars {
            VehicleScreen(
                onVehicleSelected = openRoutes,
                // Not a tap: it fires once the assignments load, which can be while this screen is still sliding in,
                // and a guard would drop it.
                onOnlyVehicle = { vehicleId ->
                    if (skipsOnlyVehicle(route, account.uiState.value)) {
                        navController.navigate("trip/${arg(vehicleId)}", skipBusStep)
                    }
                },
                // Not a tap: it fires when the reply lands, and a guard would drop it.
                onSessionEnded = { navController.navigate(ROUTE_LOGIN, toSignIn) },
                // Not a tap either: it fires once the phone has forgotten the driver.
                onSignedOut = { navController.navigate(ROUTE_LOGIN, toSignIn) },
            )
        }
    }
}
