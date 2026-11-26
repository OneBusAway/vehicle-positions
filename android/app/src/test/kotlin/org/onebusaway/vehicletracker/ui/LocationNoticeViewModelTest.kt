package org.onebusaway.vehicletracker.ui

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
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
import org.onebusaway.vehicletracker.data.LocationNoticeStore
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.ui.permissions.LocationNoticeViewModel
import java.io.IOException

/** Whether the notice was agreed to on this phone, and the server it names. */
class LocationNoticeViewModelTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private class FakeStore(agreed: Boolean = false, val saveFails: Boolean = false) : LocationNoticeStore {
        val state = MutableStateFlow(agreed)
        var saves = 0
        override val agreed: Flow<Boolean> = state

        override suspend fun agree() {
            saves++
            if (saveFails) throw IOException("full")
            state.value = true
        }
    }

    private val sessions = FakeSessionStore().apply {
        state.value = Session(serverUrl = "https://metro.example/", token = "jwt", issuedAtEpochSec = 0L, email = "d@example.com")
    }

    @Test fun `unread until the phone's answer is read`() = runTest(dispatcher) {
        val vm = LocationNoticeViewModel(sessions, FakeStore(agreed = true))
        assertNull(vm.agreed.value)

        advanceUntilIdle()

        assertEquals(true, vm.agreed.value)
        assertEquals("metro.example", vm.server.value)
    }

    @Test fun `Agree counts at once and is saved`() = runTest(dispatcher) {
        val store = FakeStore()
        val vm = LocationNoticeViewModel(sessions, store)
        advanceUntilIdle()
        assertEquals(false, vm.agreed.value)

        vm.agree()
        advanceUntilIdle()

        assertEquals(true, vm.agreed.value)
        assertEquals(1, store.saves)
        assertEquals(true, store.state.value)
    }

    // The driver tapped Agree: Android is asked this time, and the notice shows again next time.
    @Test fun `an agreement the phone can't save still counts this time`() = runTest(dispatcher) {
        val vm = LocationNoticeViewModel(sessions, FakeStore(saveFails = true))
        advanceUntilIdle()

        vm.agree()
        advanceUntilIdle()

        assertEquals(true, vm.agreed.value)
    }
}
