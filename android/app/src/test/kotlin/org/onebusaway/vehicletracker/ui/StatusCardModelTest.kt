package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel
import org.onebusaway.vehicletracker.ui.tracking.statusCardModel

class StatusCardModelTest {
    private val t1 = TripFixtures.t1
    private val evaluator = requireNotNull(AdherenceEvaluator.of(t1))

    /** ST1 is scheduled at exactly 08:00, so the status follows from [secondsLate] alone. */
    private fun atStop1(secondsLate: Long) =
        TrackingState(
            active = true,
            geometry = t1,
            adherence = evaluator.evaluate(fix(47.6000, -122.3300, at(8, 0).plusSeconds(secondsLate)), previous = null),
        )

    @Test fun `on time, late and early are judged against the next stop`() {
        for ((secondsLate, status, tone) in listOf(
            Triple(0L, AdherenceStatus.ON_TIME, StatusTone.ON_TIME),
            Triple(400L, AdherenceStatus.LATE, StatusTone.LATE),
            Triple(-120L, AdherenceStatus.EARLY, StatusTone.EARLY),
        )) {
            val tracking = atStop1(secondsLate)
            val model = statusCardModel(tracking, vehicleId = "4317")

            assertTrue("$status", model is StatusCardModel.Adherence)
            model as StatusCardModel.Adherence
            assertEquals(status, model.status)
            assertEquals(tone, model.tone)
            assertEquals(tracking.adherence?.scheduleDeviationSec, model.deviationSec)
            assertEquals(t1.stops[1], model.nextStop)
            assertEquals(t1.timezone, model.zone)
        }
    }

    @Test fun `off schedule takes its tone from the direction it is off in`() {
        val farAhead = statusCardModel(atStop1(-1000), vehicleId = "4317")
        val farBehind = statusCardModel(atStop1(6000), vehicleId = "4317")

        assertTrue(farAhead is StatusCardModel.OffSchedule)
        assertTrue(farBehind is StatusCardModel.OffSchedule)
        assertEquals(StatusTone.EARLY, farAhead.tone)
        assertEquals(StatusTone.LATE, farBehind.tone)
        assertEquals(6000.0, (farBehind as StatusCardModel.OffSchedule).deviationSec, 0.5)
        assertEquals(t1.stops[1], farBehind.nextStop)
    }

    @Test fun `off route carries the distance from the route and the next stop`() {
        // 0.010° of longitude east of the line is about 750 m.
        val adherence = evaluator.evaluate(fix(47.6045, -122.3200, at(8, 5)), previous = null)
        val model = statusCardModel(TrackingState(active = true, geometry = t1, adherence = adherence), vehicleId = "4317")

        assertTrue(model is StatusCardModel.OffRoute)
        model as StatusCardModel.OffRoute
        assertEquals(StatusTone.NEUTRAL, model.tone)
        assertEquals(adherence.projection.distanceToShape, model.distanceFromRouteM, 0.0)
        assertEquals(adherence.nextStop, model.nextStop)
        assertEquals(adherence.distanceToNextStopM, model.distanceToNextStopM, 0.0)
    }

    @Test fun `with the trip but no fix yet it waits, from the first stop`() {
        val model = statusCardModel(TrackingState(active = true, geometry = t1), vehicleId = "4317")

        assertEquals(StatusCardModel.WaitingForGps(firstStop = t1.stops.first(), zone = t1.timezone, vehicleId = "4317"), model)
        assertEquals(StatusTone.NEUTRAL, model.tone)
    }

    @Test fun `reporting with no trip to judge against has no schedule`() {
        assertEquals(StatusCardModel.NoSchedule, statusCardModel(TrackingState(active = true, geometry = null), vehicleId = "4317"))
    }

    @Test fun `before the service has started there is nothing to name yet`() {
        val model = statusCardModel(TrackingState(active = false, geometry = null), vehicleId = null)

        assertEquals(StatusCardModel.WaitingForGps(firstStop = null, zone = null, vehicleId = null), model)
    }
}
