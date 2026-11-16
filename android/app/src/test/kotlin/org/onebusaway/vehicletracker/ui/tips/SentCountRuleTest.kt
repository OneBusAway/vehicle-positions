package org.onebusaway.vehicletracker.ui.tips

import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel

/** When the tip on the sent count may show: before the vehicle moves, and never on the road. */
class SentCountRuleTest {
    private val zone = TripFixtures.pacific
    private val nextStop = TripFixtures.t1.stops[1]

    private val waitingBeforeTheTripLoads = StatusCardModel.WaitingForGps(firstStop = null, zone = null, vehicleId = null)
    private val waiting = StatusCardModel.WaitingForGps(TripFixtures.t1.stops[0], zone, vehicleId = "4317")
    private val onTime = StatusCardModel.Adherence(StatusTone.ON_TIME, AdherenceStatus.ON_TIME, 0.0, nextStop, 300.0, zone)
    private val late = StatusCardModel.Adherence(StatusTone.LATE, AdherenceStatus.LATE, 420.0, nextStop, 300.0, zone)
    private val early = StatusCardModel.Adherence(StatusTone.EARLY, AdherenceStatus.EARLY, -180.0, nextStop, 300.0, zone)
    private val offSchedule = StatusCardModel.OffSchedule(StatusTone.EARLY, -3000.0, nextStop, zone)
    private val offRoute = StatusCardModel.OffRoute(StatusTone.NEUTRAL, 200.0, nextStop, 300.0, zone)

    private val waitingCards = listOf(waitingBeforeTheTripLoads, waiting)
    private val judgedCards = listOf(onTime, late, early, offSchedule, offRoute)
    private val everyCard = waitingCards + judgedCards + StatusCardModel.NoSchedule

    private fun assertRule(expected: SentCountRule, cards: List<StatusCardModel>, speeds: List<Double?>) {
        for (card in cards) {
            for (speed in speeds) assertEquals("$card at $speed m/s", expected, sentCountRule(card, speed))
        }
    }

    @Test fun `while the card waits for GPS it may show, with or without a speed`() {
        assertRule(SentCountRule.MAY_SHOW, waitingCards, listOf(null, 0.0, 0.5, 0.999))
    }

    @Test fun `on a judged card it may show while the speed is known and under 1 m per s`() {
        assertRule(SentCountRule.MAY_SHOW, judgedCards + StatusCardModel.NoSchedule, listOf(0.0, 0.3, 0.999))
    }

    @Test fun `from 1 m per s the vehicle is moving, whatever the card says`() {
        assertRule(SentCountRule.MOVING, everyCard, listOf(1.0, 1.001, 8.0, 30.0))
    }

    @Test fun `with the speed unknown it holds on a judged card, and on No schedule`() {
        assertRule(SentCountRule.HOLD, judgedCards + StatusCardModel.NoSchedule, listOf(null))
    }

    @Test fun `a speed below zero or not a number is not known`() {
        val unknown = listOf(-1.0, -0.01, Double.NaN)
        assertRule(SentCountRule.HOLD, judgedCards + StatusCardModel.NoSchedule, unknown)
        assertRule(SentCountRule.MAY_SHOW, waitingCards, unknown)
    }

    @Test fun `the line between standing and moving is 1 m per s`() {
        assertEquals(SentCountRule.MAY_SHOW, sentCountRule(onTime, Math.nextDown(MOVING_SPEED_MPS)))
        assertEquals(SentCountRule.MOVING, sentCountRule(onTime, MOVING_SPEED_MPS))
        assertEquals(1.0, MOVING_SPEED_MPS, 0.0)
    }
}
