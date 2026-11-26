package org.onebusaway.vehicletracker.ui.settings

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.Session

/** What Privacy names: the server signed in, and the agency's link once the server has given it. */
class PrivacyViewModelTest {
    private val dispatcher = StandardTestDispatcher()
    private val sessions = FakeSessionStore().apply {
        state.value = Session(serverUrl = "https://metro.example/", token = "jwt", issuedAtEpochSec = 0L, email = "d@example.com")
    }

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    @Test fun `the server signed in is named as the sign-in screen names it`() = runTest(dispatcher) {
        val vm = PrivacyViewModel(sessions) { null }
        advanceUntilIdle()

        assertEquals("metro.example", vm.server.value)
    }

    @Test fun `there is no link until the server has given one`() = runTest(dispatcher) {
        val answer = CompletableDeferred<String?>()
        val vm = PrivacyViewModel(sessions) { answer.await() }
        advanceUntilIdle()
        assertNull(vm.policyLink.value)

        answer.complete("https://metro.example/privacy")
        advanceUntilIdle()
        assertEquals("https://metro.example/privacy", vm.policyLink.value)
    }
}
