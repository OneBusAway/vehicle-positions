package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.data.Session

class RefreshWhenSignedInTest {
    private val signedOut = Session(serverUrl = "https://tracker.example.org", token = null, issuedAtEpochSec = null)
    private val signedIn = Session(serverUrl = "https://tracker.example.org", token = "jwt", issuedAtEpochSec = 100L)
    private val session = MutableStateFlow(signedOut)
    private var refreshes = 0

    private fun TestScope.startCollecting() {
        backgroundScope.launch { refreshWhenSignedIn(session) { refreshes++ } }
        runCurrent()
    }

    private fun TestScope.emit(next: Session) {
        session.value = next
        runCurrent()
    }

    // How each return to the app refreshes: MainActivity collects anew every time it resumes.
    @Test fun `signed in when collection starts refreshes once`() = runTest {
        session.value = signedIn

        startCollecting()

        assertEquals(1, refreshes)
    }

    @Test fun `signing in refreshes`() = runTest {
        startCollecting()
        assertEquals(0, refreshes)

        emit(signedIn)

        assertEquals(1, refreshes)
    }

    @Test fun `signed out throughout never refreshes`() = runTest {
        startCollecting()

        assertEquals(0, refreshes)
    }

    @Test fun `a new token while signed in does not refresh again`() = runTest {
        session.value = signedIn
        startCollecting()

        emit(signedIn.copy(token = "jwt-2", issuedAtEpochSec = 200L))

        assertEquals(1, refreshes)
    }

    @Test fun `signing out and in again refreshes again`() = runTest {
        session.value = signedIn
        startCollecting()

        emit(signedOut)
        emit(signedIn)

        assertEquals(2, refreshes)
    }
}
