package org.onebusaway.vehicletracker.ui.tips

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.DriverFlag
import org.onebusaway.vehicletracker.data.DriverKey
import org.onebusaway.vehicletracker.data.FakeDriverFlagsStore
import org.onebusaway.vehicletracker.data.FakeSessionStore
import org.onebusaway.vehicletracker.data.IssuedTokens
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.data.TrackingRepository
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import java.io.IOException

private const val SERVER = "https://tracker.example.org"
private const val MARIA = "maria@metro.example"
private const val OMAR = "omar@metro.example"

/**
 * The two tips and "First trip done", for the driver signed in: each once per driver, the next driver on a shared phone
 * shown them afresh, one at a time, and the sent count's tip only before the vehicle moves.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class DriverTipsTest {
    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    private val maria = DriverKey.of(SERVER, MARIA)
    private val omar = DriverKey.of(SERVER, OMAR)
    private val flags = FakeDriverFlagsStore()
    private val sessions = FakeSessionStore()
    private val tracking = TrackingRepository()
    private val evaluator = requireNotNull(AdherenceEvaluator.of(TripFixtures.t1))

    private suspend fun signIn(email: String) = sessions.saveLogin(SERVER, email, IssuedTokens("jwt", "r1", accessExpiresAtEpochSec = 900, issuedAtEpochSec = 0))

    private fun TestScope.driverTips() = DriverTips(sessions, flags, scope = this)

    private fun seen(driver: DriverKey, flag: DriverFlag) = (driver to flag) in flags.flags

    /** On the route at its first stop, judged from a fix that says the vehicle goes at [speed] m/s, or says nothing. */
    private fun atTheFirstStop(speed: Double?, sent: Int = 0) = TrackingState(
        active = true,
        fixesSent = sent,
        geometry = TripFixtures.t1,
        adherence = evaluator.evaluate(fix(47.6000, -122.3300, at(8, 0), speed = speed), previous = null),
    )

    // --- The run board ---

    @Test fun `the run board's tip shows a driver who has not seen it`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()

        assertTrue(tip.show.value)
        assertTrue(tip.onShown())
        assertFalse("shown is not seen: only closing it is", seen(maria, DriverFlag.RunBoardTipSeen))
    }

    @Test fun `Got it closes it, and it never shows that driver again`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val tip = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        tip.onShown()

        tip.onSeen()
        advanceUntilIdle()

        assertFalse(tip.show.value)
        assertTrue(seen(maria, DriverFlag.RunBoardTipSeen))
        val nextVisit = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        assertFalse(nextVisit.show.value)
    }

    @Test fun `a run tapped before the tip came counts as seen`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()

        tip.onSeen()
        advanceUntilIdle()

        assertFalse(tip.show.value)
        assertTrue(seen(maria, DriverFlag.RunBoardTipSeen))
    }

    @Test fun `a run tapped before the phone had read the record still counts`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = RunBoardTipViewModel(driverTips())

        tip.onSeen()
        advanceUntilIdle()

        assertFalse("it does not come after the tap", tip.show.value)
        assertTrue(seen(maria, DriverFlag.RunBoardTipSeen))
    }

    @Test fun `not for a driver who has seen it`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.RunBoardTipSeen
        signIn(MARIA)
        val tip = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()

        assertFalse(tip.show.value)
        assertFalse(tip.onShown())
    }

    @Test fun `the next driver on the same phone is shown it`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.RunBoardTipSeen
        signIn(OMAR)
        val tip = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()

        assertTrue(tip.show.value)
        tip.onSeen()
        advanceUntilIdle()
        assertTrue(seen(omar, DriverFlag.RunBoardTipSeen))
    }

    @Test fun `left without being closed, as by Back, it shows again on the next visit`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val tip = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        tip.onShown()

        tip.onLeft()
        advanceUntilIdle()

        assertNull("its place is free", tips.onScreen.value)
        assertFalse(seen(maria, DriverFlag.RunBoardTipSeen))
        val nextVisit = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        assertTrue(nextVisit.show.value)
    }

    @Test fun `with nobody signed in, or a session without the email, there is no tip`() = runTest(dispatcher) {
        val nobody = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()
        assertFalse(nobody.show.value)

        sessions.state.value = Session(SERVER, "jwt", issuedAtEpochSec = 0L, email = null)
        val olderBuild = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()
        assertFalse(olderBuild.show.value)
        olderBuild.onSeen()
        advanceUntilIdle()
        assertEquals(0, flags.sets)
    }

    @Test fun `a record the phone can't read leaves the tip out`() = runTest(dispatcher) {
        signIn(MARIA)
        flags.failure = IOException("disk")
        val tip = RunBoardTipViewModel(driverTips())
        advanceUntilIdle()

        assertFalse(tip.show.value)
    }

    @Test fun `a record the phone can't save shows the tip once more, and is not a crash`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val tip = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        flags.failure = IOException("disk full")

        tip.onSeen()
        advanceUntilIdle()

        flags.failure = null
        val nextVisit = RunBoardTipViewModel(tips)
        advanceUntilIdle()
        assertTrue(nextVisit.show.value)
    }

    // --- The sent count, before the vehicle moves ---

    @Test fun `on the first trip it shows while Tracking waits for GPS`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = SentCountTipViewModel(driverTips(), tracking)
        advanceUntilIdle()

        assertTrue(tip.show.value)
    }

    @Test fun `standing at a stop, just under 1 m per s, it shows`() = runTest(dispatcher) {
        signIn(MARIA)
        tracking.update { atTheFirstStop(speed = 0.9) }
        val tip = SentCountTipViewModel(driverTips(), tracking)
        advanceUntilIdle()

        assertTrue(tip.show.value)
    }

    @Test fun `it never shows at 1 m per s or more, and the moving first trip settles it`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        for (speed in listOf(1.0, 8.0)) {
            tracking.update { atTheFirstStop(speed) }
            val tip = SentCountTipViewModel(tips, tracking)
            advanceUntilIdle()

            assertFalse("at $speed m/s", tip.show.value)
            assertFalse(tip.onShown())
        }
        assertTrue("it never comes back on a later trip", seen(maria, DriverFlag.SentCountTipSeen))
    }

    @Test fun `once the vehicle reaches 1 m per s it goes by itself, counts as seen, and never comes back`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        tracking.update { atTheFirstStop(speed = 0.0) }
        val tip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        assertTrue(tip.onShown())

        tracking.update { atTheFirstStop(speed = 1.0) }
        advanceUntilIdle()

        assertFalse(tip.show.value)
        assertNull("its place is free", tips.onScreen.value)
        assertTrue(seen(maria, DriverFlag.SentCountTipSeen))
        tracking.update { atTheFirstStop(speed = 0.0) }
        advanceUntilIdle()
        assertFalse("stopped again, it stays gone", tip.show.value)
    }

    @Test fun `a fix with no speed does not bring it, and keeps one already out`() = runTest(dispatcher) {
        signIn(MARIA)
        tracking.update { atTheFirstStop(speed = null) }
        val tip = SentCountTipViewModel(driverTips(), tracking)
        advanceUntilIdle()
        assertFalse(tip.show.value)

        tracking.update { atTheFirstStop(speed = 0.0) }
        advanceUntilIdle()
        assertTrue(tip.show.value)
        tip.onShown()

        tracking.update { atTheFirstStop(speed = null) }
        advanceUntilIdle()
        assertTrue(tip.show.value)
    }

    @Test fun `Got it settles it`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = SentCountTipViewModel(driverTips(), tracking)
        advanceUntilIdle()
        tip.onShown()

        tip.onSeen()
        advanceUntilIdle()

        assertFalse(tip.show.value)
        assertTrue(seen(maria, DriverFlag.SentCountTipSeen))
    }

    @Test fun `the first trip's end settles it, shown or not`() = runTest(dispatcher) {
        signIn(MARIA)
        val tip = SentCountTipViewModel(driverTips(), tracking)
        advanceUntilIdle()

        tip.onTripEnded()
        advanceUntilIdle()

        assertTrue(seen(maria, DriverFlag.SentCountTipSeen))
    }

    @Test fun `not on a later trip, but on the next driver's first`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.SentCountTipSeen
        signIn(MARIA)
        val tips = driverTips()
        val marias = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        assertFalse(marias.show.value)

        signIn(OMAR)
        val omars = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        assertTrue(omars.show.value)
    }

    // --- First trip done ---

    @Test fun `a first trip that sent reports is followed by First trip done, once`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val done = FirstTripDoneViewModel(tips)
        val firstTrip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        tracking.update { atTheFirstStop(speed = 8.0, sent = 142) }
        advanceUntilIdle()

        firstTrip.onTripEnded()
        advanceUntilIdle()

        assertEquals(FinishedTrip(maria, 142), done.trip.value)
        assertTrue(done.onShown(FinishedTrip(maria, 142)))
        advanceUntilIdle()
        assertTrue("seen as soon as it is up", seen(maria, DriverFlag.FirstTripDoneSeen))
        done.onClosed()
        advanceUntilIdle()
        assertNull(done.trip.value)

        val secondTrip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        secondTrip.onTripEnded()
        advanceUntilIdle()
        assertNull("not after a second trip", done.trip.value)
    }

    @Test fun `an end heard twice, as after a rotation, does not bring it back once closed`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val done = FirstTripDoneViewModel(tips)
        val trip = SentCountTipViewModel(tips, tracking)
        tracking.update { atTheFirstStop(speed = 8.0, sent = 20) }
        advanceUntilIdle()
        trip.onTripEnded()
        advanceUntilIdle()
        done.onShown(FinishedTrip(maria, 20))
        done.onClosed()
        advanceUntilIdle()

        trip.onTripEnded()
        advanceUntilIdle()

        assertNull(done.trip.value)
    }

    @Test fun `not after a trip that sent 0 reports, which leaves it for the next trip`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val done = FirstTripDoneViewModel(tips)
        val emptyTrip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()

        emptyTrip.onTripEnded()
        advanceUntilIdle()
        assertNull(done.trip.value)
        assertFalse(seen(maria, DriverFlag.FirstTripDoneSeen))

        tracking.update { atTheFirstStop(speed = 8.0, sent = 3) }
        val nextTrip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        nextTrip.onTripEnded()
        advanceUntilIdle()
        assertEquals(FinishedTrip(maria, 3), done.trip.value)
    }

    @Test fun `it shows the most the trip sent, though the tracking state clears as the service stops`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val trip = SentCountTipViewModel(tips, tracking)
        advanceUntilIdle()
        tracking.update { atTheFirstStop(speed = 8.0, sent = 57) }
        advanceUntilIdle()

        tracking.update { TrackingState() }
        advanceUntilIdle()
        trip.onTripEnded()

        assertEquals(FinishedTrip(maria, 57), tips.finishedFirstTrip.value)
    }

    @Test fun `not for a driver who has had it`() = runTest(dispatcher) {
        flags.flags += maria to DriverFlag.FirstTripDoneSeen
        signIn(MARIA)
        val tips = driverTips()
        val trip = SentCountTipViewModel(tips, tracking)
        tracking.update { atTheFirstStop(speed = 8.0, sent = 12) }
        advanceUntilIdle()

        trip.onTripEnded()

        assertNull(tips.finishedFirstTrip.value)
    }

    // --- One at a time ---

    @Test fun `only one is out at a time, and the next waits for it to go`() = runTest(dispatcher) {
        signIn(MARIA)
        val tips = driverTips()
        val runBoard = RunBoardTipViewModel(tips)
        val sentCount = SentCountTipViewModel(tips, tracking)
        val done = FirstTripDoneViewModel(tips)
        advanceUntilIdle()
        tips.firstTripEnded(FinishedTrip(maria, 9))
        advanceUntilIdle()

        assertTrue(runBoard.onShown())
        advanceUntilIdle()
        assertEquals(Tip.RUN_BOARD, tips.onScreen.value)
        assertFalse(sentCount.show.value)
        assertFalse(sentCount.onShown())
        assertNull("First trip done waits", done.trip.value)
        assertFalse(done.onShown(FinishedTrip(maria, 9)))

        runBoard.onSeen()
        advanceUntilIdle()
        assertEquals(FinishedTrip(maria, 9), done.trip.value)
        assertTrue(done.onShown(FinishedTrip(maria, 9)))
        advanceUntilIdle()
        assertFalse("and the sheet holds it in turn", sentCount.show.value)

        done.onClosed()
        advanceUntilIdle()
        assertTrue(sentCount.show.value)
        assertTrue(sentCount.onShown())
    }
}
