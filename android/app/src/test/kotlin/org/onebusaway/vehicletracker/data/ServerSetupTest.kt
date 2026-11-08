package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.ServerSetupLink.Decision

class ServerSetupTest {
    private val link = "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org"
    private val linked = "https://tracker.example.org/"
    private val sessionStore = FakeSessionStore()
    private val tripState = FakeTripStateStore()
    private val managed = FakeManagedServerSource()

    private fun TestScope.serverSetup() = ServerSetup(sessionStore, tripState, managed, debuggable = false, scope = this)

    @Test fun `agreeing to a link hands its server to Sign in and writes nothing`() = runTest {
        val setup = serverSetup()
        val session = sessionStore.state.value

        setup.open(link)
        advanceUntilIdle()
        assertEquals(Decision.Confirm(linked, "tracker.example.org"), setup.prompt.value)
        setup.confirm(linked)
        advanceUntilIdle()

        assertNull(setup.prompt.value)
        assertEquals(linked, setup.linkedServer.value)
        assertEquals(session, sessionStore.state.value)
        assertNull(tripState.tripState.value)
    }

    @Test fun `a token left on the phone refuses a link, and is never paired with its host`() = runTest {
        // From a build before refresh tokens, and past its 24 hours: Sign in shows, but the token is still stored.
        val session = Session(serverUrl = "https://positions.example.org/", token = jwtFor("7"), issuedAtEpochSec = 0L)
        sessionStore.state.value = session
        require(!session.isSignedIn(nowEpochSec = 24 * 3600L))
        val setup = serverSetup()

        setup.open(link)
        advanceUntilIdle()
        assertEquals(Decision.RefusedSignedIn("positions.example.org"), setup.prompt.value)
        setup.confirm(linked)
        advanceUntilIdle()

        assertNull(setup.linkedServer.value)
        assertEquals(session, sessionStore.state.value)
    }

    @Test fun `a trip from an older build, stored with no server, is refused for the session's`() = runTest {
        sessionStore.state.value = Session(serverUrl = "https://positions.example.org/", token = null, issuedAtEpochSec = null)
        tripState.tripState.value = ActiveTrip(7L, "T1", "bus-1", "R1", "20261107", 100L)
        val setup = serverSetup()

        setup.open(link)
        advanceUntilIdle()

        assertEquals(Decision.RefusedSignedIn("positions.example.org"), setup.prompt.value)
    }

    @Test fun `a trip restored from backup refuses a link for the trip's server, even with no token`() = runTest {
        // The backup keeps trip_state, the trip's server with it, and leaves out the session.
        tripState.tripState.value = ActiveTrip(7L, "T1", "bus-1", "R1", "20261107", 100L, serverUrl = "https://positions.example.org/")
        val setup = serverSetup()

        setup.open(link)
        advanceUntilIdle()

        assertEquals(Decision.RefusedSignedIn("positions.example.org"), setup.prompt.value)
    }

    @Test fun `an older build's trip restored from backup refuses a link without naming a server`() = runTest {
        tripState.tripState.value = ActiveTrip(7L, "T1", "bus-1", "R1", "20261107", 100L)
        val setup = serverSetup()

        setup.open(link)
        advanceUntilIdle()

        assertEquals(Decision.RefusedSignedIn(host = null), setup.prompt.value)
    }

    @Test fun `a link while the agency sets the server is refused`() = runTest {
        managed.state.value = ManagedServer("https://managed.example.org/", invalid = false)
        val setup = serverSetup()

        setup.open(link)
        advanceUntilIdle()

        assertEquals(Decision.RefusedManaged, setup.prompt.value)
    }

    @Test fun `agreeing after the driver has signed in does nothing`() = runTest {
        val setup = serverSetup()
        setup.open(link)
        advanceUntilIdle()

        sessionStore.saveLogin("https://positions.example.org/", "d@example.com", IssuedTokens(jwtFor("7"), "r1", 900L, 0L))
        setup.confirm(linked)
        advanceUntilIdle()

        assertNull(setup.prompt.value)
        assertNull(setup.linkedServer.value)
        assertEquals("https://positions.example.org/", sessionStore.state.value.serverUrl)
    }

    @Test fun `agreeing after the agency has set the server does nothing`() = runTest {
        val setup = serverSetup()
        setup.open(link)
        advanceUntilIdle()

        managed.state.value = ManagedServer("https://managed.example.org/", invalid = false)
        setup.confirm(linked)
        advanceUntilIdle()

        assertNull(setup.linkedServer.value)
    }

    @Test fun `cancelling clears the prompt and hands nothing over`() = runTest {
        val setup = serverSetup()
        setup.open(link)
        advanceUntilIdle()

        setup.dismiss()
        advanceUntilIdle()

        assertNull(setup.prompt.value)
        assertNull(setup.linkedServer.value)
    }

    @Test fun `opening a link again clears the last one first`() = runTest {
        val setup = serverSetup()
        setup.open(link)
        advanceUntilIdle()
        setup.confirm(linked)
        advanceUntilIdle()

        setup.open(link)

        assertNull(setup.linkedServer.value)
    }
}
