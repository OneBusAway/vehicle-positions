package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.MyTrip
import org.onebusaway.vehicletracker.data.MyTripsPage
import org.onebusaway.vehicletracker.data.MyTripsSource
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.service.FakeBacklogUploadScheduler
import java.time.Instant

/** My trips as its view model runs it: what it asks for, and how a 401 and a failed refresh end. */
class MyTripsViewModelTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private val sessions = FakeSessionStore().apply {
        state.value = Session(serverUrl = "https://metro.example/", token = "jwt", issuedAtEpochSec = 0L, refreshToken = "r1", email = "d@example.com")
    }
    private val auth = AuthRepository(sessions, FakeTripStateStore(), ApiFactory { null }, TrackerApiProvider { error("no call") }, FakeOfflineFixQueue(), FakeBacklogUploadScheduler(), clock = { 0L })

    private class Trips : MyTripsSource {
        val asked = mutableListOf<Int>()
        val answers = ArrayDeque<Result<MyTripsPage>>()
        var hold: CompletableDeferred<Unit>? = null

        override suspend fun page(limit: Int, offset: Int): Result<MyTripsPage> {
            asked += offset
            hold?.await()
            return answers.removeFirstOrNull() ?: Result.success(MyTripsPage(false, emptyList()))
        }
    }

    private fun page(ids: LongRange, hasMore: Boolean) = MyTripsPage(hasMore, ids.reversed().map {
        MyTrip(it, "4317", "R1", null, "North", Instant.EPOCH, Instant.EPOCH.plusSeconds(600))
    })

    @Test fun `it loads the first page, and the end of the list asks for the next once`() = runTest(dispatcher) {
        val trips = Trips().apply { answers += Result.success(page(51L..100L, hasMore = true)) }
        val vm = MyTripsViewModel(trips, auth)
        runCurrent()
        assertEquals(50, vm.list.value.trips.size)

        trips.hold = CompletableDeferred()
        val next = vm.list.value.nextPage!!
        vm.onLoadMore(next)
        vm.onLoadMore(next)
        runCurrent()
        assertEquals("asked once while it is out", listOf(0, 50), trips.asked)

        trips.answers += Result.success(page(1L..50L, hasMore = false))
        trips.hold!!.complete(Unit)
        runCurrent()
        assertEquals(100, vm.list.value.trips.size)
        vm.viewModelScope.cancel()
    }

    // As the pickers: the tokens go, and Sign in shows, with the server and the email kept.
    @Test fun `a sign-in the server ended ends here`() = runTest(dispatcher) {
        val trips = Trips().apply { answers += Result.failure(ApiError.Unauthorized) }
        val vm = MyTripsViewModel(trips, auth)
        runCurrent()

        assertTrue(vm.sessionEnded.value)
        assertNull(sessions.state.value.token)
        assertEquals("d@example.com", sessions.state.value.email)
        assertEquals("not a failed load", MyTripsLoad.LOADING, vm.list.value.load)
    }

    @Test fun `a failed refresh keeps the list and lets the pull go, and Try again on a failed first load loads again`() = runTest(dispatcher) {
        val trips = Trips().apply {
            answers += Result.success(page(1L..3L, hasMore = false))
            answers += Result.failure(ApiError.Other("network"))
        }
        val vm = MyTripsViewModel(trips, auth)
        runCurrent()

        vm.onRefresh()
        assertTrue(vm.refreshing.value)
        runCurrent()
        assertFalse(vm.refreshing.value)
        assertTrue(vm.list.value.refreshFailed)
        assertEquals(3, vm.list.value.trips.size)

        val failing = Trips().apply { answers += Result.failure(ApiError.Other("network")) }
        val other = MyTripsViewModel(failing, auth)
        runCurrent()
        assertEquals(MyTripsLoad.FAILED, other.list.value.load)
        other.onRetry()
        assertEquals(MyTripsLoad.LOADING, other.list.value.load)
        runCurrent()
        assertEquals(listOf(0, 0), failing.asked)
        assertEquals(MyTripsLoad.LOADED, other.list.value.load)
    }
}
