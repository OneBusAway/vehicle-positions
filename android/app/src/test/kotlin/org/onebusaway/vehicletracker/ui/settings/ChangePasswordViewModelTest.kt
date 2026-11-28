package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
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
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.FakeOfflineFixQueue
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.FakeTripStateStore
import org.onebusaway.vehicletracker.data.PasswordChangeFailure
import org.onebusaway.vehicletracker.data.PasswordChanger
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.service.FakeBacklogUploadScheduler

/** Change password as its view model runs it: what is sent, and how the page ends. */
class ChangePasswordViewModelTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    private val sessions = FakeSessionStore().apply {
        state.value = Session(serverUrl = "https://metro.example/", token = "jwt", issuedAtEpochSec = 0L, refreshToken = "r1", email = "d@example.com")
    }
    private val auth = AuthRepository(sessions, FakeTripStateStore(), ApiFactory { null }, TrackerApiProvider { error("no call") }, FakeOfflineFixQueue(), FakeBacklogUploadScheduler(), clock = { 0L })

    private class Changer(var answer: PasswordChangeFailure? = null) : PasswordChanger {
        val asked = mutableListOf<Pair<String, String>>()
        var hold: CompletableDeferred<Unit>? = null

        override suspend fun change(current: String, new: String): PasswordChangeFailure? {
            asked += current to new
            hold?.await()
            return answer
        }
    }

    private fun typed(vm: ChangePasswordViewModel, current: String, new: String, again: String = new) {
        vm.current.setTextAndPlaceCursorAtEnd(current)
        vm.new.setTextAndPlaceCursorAtEnd(new)
        vm.again.setTextAndPlaceCursorAtEnd(again)
    }

    @Test fun `a change that goes through ends the page as changed`() = runTest(dispatcher) {
        val changer = Changer()
        val vm = ChangePasswordViewModel(changer, auth, sessions)
        typed(vm, "old-secret", "new-secret")

        vm.onChange()
        advanceUntilIdle()

        assertEquals(listOf("old-secret" to "new-secret"), changer.asked)
        assertEquals(ChangePasswordOutcome.CHANGED, vm.outcome.value)
        assertEquals("metro.example", vm.server.value)
    }

    // As any screen does on a 401: Sign in, with the server and the email kept.
    @Test fun `a sign-in that turns out to be over ends here, and Sign in shows`() = runTest(dispatcher) {
        val vm = ChangePasswordViewModel(Changer(PasswordChangeFailure.SESSION_OVER), auth, sessions)
        typed(vm, "old-secret", "new-secret")

        vm.onChange()
        advanceUntilIdle()

        assertEquals(ChangePasswordOutcome.SESSION_ENDED, vm.outcome.value)
        assertNull(sessions.state.value.token)
        assertEquals("d@example.com", sessions.state.value.email)
    }

    @Test fun `what the server said shows on the page, which stays`() = runTest(dispatcher) {
        val vm = ChangePasswordViewModel(Changer(PasswordChangeFailure.WRONG_CURRENT), auth, sessions)
        typed(vm, "wrong", "new-secret")

        vm.onChange()
        advanceUntilIdle()

        assertTrue(vm.form.value.wrongCurrent)
        assertNull(vm.outcome.value)
    }

    @Test fun `nothing is sent until the checks pass, nor twice while one is out`() = runTest(dispatcher) {
        val changer = Changer().apply { hold = CompletableDeferred() }
        val vm = ChangePasswordViewModel(changer, auth, sessions)
        typed(vm, "old-secret", "short", "shorter")
        vm.onChange()
        advanceUntilIdle()
        assertTrue(changer.asked.isEmpty())

        typed(vm, "old-secret", "new-secret")
        vm.onChange()
        vm.onChange()
        advanceUntilIdle()
        assertEquals(1, changer.asked.size)
        assertTrue(vm.form.value.changing)

        changer.hold!!.complete(Unit)
        advanceUntilIdle()
        assertEquals(ChangePasswordOutcome.CHANGED, vm.outcome.value)
    }

    @Test fun `leaving New password checks it`() = runTest(dispatcher) {
        val vm = ChangePasswordViewModel(Changer(), auth, sessions)
        vm.new.setTextAndPlaceCursorAtEnd("short")

        vm.onLeftNew()

        assertEquals(NewPasswordProblem.TOO_SHORT, vm.form.value.newProblem)
    }
}
