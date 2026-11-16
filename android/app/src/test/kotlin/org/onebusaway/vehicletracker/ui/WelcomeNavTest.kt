package org.onebusaway.vehicletracker.ui

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.FakeDriverFlagsStore
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.ui.welcome.WelcomeViewModel
import java.io.IOException
import java.time.Duration

/** When the welcome shows, and what leaving it records: once per driver, after sign-in and before the vehicle step. */
@OptIn(ExperimentalCoroutinesApi::class)
class WelcomeNavTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    private val now = 100_000L
    private val server = "https://tracker.example.org"
    private val maria = DriverKey.of(server, "maria@metro.example")
    private val omar = DriverKey.of(server, "omar@metro.example")
    private val signedOut = Session(serverUrl = server, token = null, issuedAtEpochSec = null)
    private val trip = ActiveTrip(7L, "T1", "4317", "R1", "20261114", startedAtEpochSec = now - 600)

    private val flags = FakeDriverFlagsStore()
    private val sessions = FakeSessionStore()

    private fun tokens() = IssuedTokens("jwt", "r1", accessExpiresAtEpochSec = now + 900, issuedAtEpochSec = now)

    private fun signedInAs(email: String?) =
        Session(server, "jwt", now - 3600, refreshToken = "r1", accessExpiresAtEpochSec = now + 900, email = email)

    private fun appNav(
        session: Session,
        trip: ActiveTrip? = null,
        tracking: TrackingRepository = TrackingRepository(),
    ): AppNavViewModel {
        sessions.state.value = session
        return AppNavViewModel(
            sessions,
            FakeTripStateStore().apply { tripState.value = trip },
            tracking,
            FakeOfflineFixQueue(),
            clock = { now },
            serviceAnnounceGrace = Duration.ofSeconds(2),
            driverFlags = flags,
        )
    }

    private fun opensOn(session: Session, trip: ActiveTrip? = null, tracking: TrackingRepository = TrackingRepository()): String? {
        val vm = appNav(session, trip, tracking)
        dispatcher.scheduler.advanceUntilIdle()
        return vm.startDestination.value
    }

    // --- Opening the app with a driver signed in ---

    @Test fun `a driver who has not seen the welcome opens on it`() = runTest(dispatcher) {
        assertEquals("welcome", opensOn(signedInAs("maria@metro.example")))
    }

    @Test fun `a driver who has seen it opens on the vehicle step`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.WelcomeSeen

        assertEquals("vehicles", opensOn(signedInAs("maria@metro.example")))
    }

    @Test fun `the next driver on the same phone sees it`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.WelcomeSeen

        assertEquals("welcome", opensOn(signedInAs("omar@metro.example")))
    }

    @Test fun `with nobody signed in it opens on Sign in`() = runTest(dispatcher) {
        assertEquals("login", opensOn(signedOut.copy(email = "maria@metro.example")))
    }

    @Test fun `a session an older build stored, which names no driver, goes to the vehicle step`() = runTest(dispatcher) {
        assertEquals("vehicles", opensOn(signedInAs(email = null)))
    }

    @Test fun `a record that can't be read goes to the vehicle step`() = runTest(dispatcher) {
        flags.failure = IOException("disk")

        assertEquals("vehicles", opensOn(signedInAs("maria@metro.example")))
    }

    @Test fun `a paused trip wins over the welcome`() = runTest(dispatcher) {
        assertEquals("resume", opensOn(signedInAs("maria@metro.example"), trip))
    }

    @Test fun `a running trip wins over the welcome`() = runTest(dispatcher) {
        val tracking = TrackingRepository().apply { update { it.copy(active = true) } }

        assertEquals("tracking", opensOn(signedInAs("maria@metro.example"), trip, tracking))
    }

    @Test fun `ending a trip from Resume goes to the vehicle step, as from Tracking`() = runTest(dispatcher) {
        val vm = appNav(signedInAs("maria@metro.example"), trip)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("vehicles", vm.routeAfterResumeEnds())
    }

    // --- Signing in, and leaving the welcome ---

    @Test fun `a first sign-in goes on to the welcome, and one after Skip to the vehicle step`() = runTest(dispatcher) {
        val vm = appNav(signedOut)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals("login", vm.startDestination.value)

        sessions.saveLogin(server, "Maria@metro.example", tokens())
        assertEquals("welcome", vm.routeAfterSignIn())

        WelcomeViewModel(sessions, flags).leave(marksSeen = true)
        dispatcher.scheduler.advanceUntilIdle()
        // Signing out forgets the driver's email, and not that they have seen the welcome.
        sessions.clearTokenAndEmail()
        sessions.saveLogin("$server/", "maria@metro.example", tokens())

        assertEquals("vehicles", vm.routeAfterSignIn())
    }

    @Test fun `another driver signing in on the phone goes on to the welcome`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.WelcomeSeen
        val vm = appNav(signedOut)
        dispatcher.scheduler.advanceUntilIdle()

        sessions.saveLogin(server, "omar@metro.example", tokens())

        assertEquals("welcome", vm.routeAfterSignIn())
    }

    @Test fun `leaving records the welcome as seen by the driver signed in, and then says so`() = runTest(dispatcher) {
        sessions.state.value = signedInAs("maria@metro.example")
        val vm = WelcomeViewModel(sessions, flags)
        assertFalse(vm.left.value)

        vm.leave(marksSeen = true)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(setOf(maria to DriverFlag.WelcomeSeen), flags.flags)
        assertTrue(vm.left.value)
    }

    @Test fun `leaving a welcome opened to be read again records nothing`() = runTest(dispatcher) {
        sessions.state.value = signedInAs("omar@metro.example")
        val vm = WelcomeViewModel(sessions, flags)

        vm.leave(marksSeen = false)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(0, flags.sets)
        assertFalse(flags.isSet(omar, DriverFlag.WelcomeSeen))
        assertTrue(vm.left.value)
    }

    @Test fun `a second tap on the way out records it once`() = runTest(dispatcher) {
        sessions.state.value = signedInAs("maria@metro.example")
        val vm = WelcomeViewModel(sessions, flags)

        vm.leave(marksSeen = true)
        vm.leave(marksSeen = true)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, flags.sets)
    }

    @Test fun `a record that can't be saved still lets the driver go on`() = runTest(dispatcher) {
        sessions.state.value = signedInAs("maria@metro.example")
        flags.failure = IOException("disk full")
        val vm = WelcomeViewModel(sessions, flags)

        vm.leave(marksSeen = true)
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(vm.left.value)
    }
}
