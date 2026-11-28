package org.onebusaway.vehicletracker.ui.help

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.engine.AdherenceEvaluator
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.engine.TripFixtures.at
import org.onebusaway.vehicletracker.engine.TripFixtures.fix
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel
import org.onebusaway.vehicletracker.ui.tracking.statusCardModel

/**
 * Every status the card can give, every outlook of its waiting line and every problem a trip can have has its own row on
 * Help, in the spec's order.
 */
class HelpRowsTest {
    private val t1 = TripFixtures.t1
    private val evaluator = requireNotNull(AdherenceEvaluator.of(t1))

    private fun judged(lat: Double, lon: Double, secondsLate: Long) = statusCardModel(
        TrackingState(active = true, geometry = t1, adherence = evaluator.evaluate(fix(lat, lon, at(8, 0).plusSeconds(secondsLate)), null)),
        vehicleId = "4317",
    )

    /** One of each answer, made by the card's own model from a trip. */
    private val everyCardAnswer: List<StatusCardModel> = listOf(
        judged(47.6000, -122.3300, 0),
        judged(47.6000, -122.3300, 400),
        judged(47.6000, -122.3300, -120),
        judged(47.6000, -122.3300, -1000),
        // 0.010° of longitude east of the route is about 750 m.
        judged(47.6045, -122.3200, 300),
        statusCardModel(TrackingState(active = true, geometry = t1), vehicleId = "4317"),
        statusCardModel(TrackingState(active = true, geometry = null), vehicleId = "4317"),
    )

    @Test fun `the samples are one of every kind of answer the card has`() {
        val kinds = everyCardAnswer.map { model ->
            if (model is StatusCardModel.Adherence) "Adherence ${model.status}" else model::class.java.simpleName
        }
        assertEquals(
            listOf("Adherence ON_TIME", "Adherence LATE", "Adherence EARLY", "OffSchedule", "OffRoute", "WaitingForGps", "NoSchedule"),
            kinds,
        )
        // A new kind of answer fails here until it has a sample above, and so a row below.
        val permitted = requireNotNull(StatusCardModel::class.java.permittedSubclasses) { "the card's model is not sealed" }
        assertEquals(permitted.map { it.simpleName }.toSet(), everyCardAnswer.map { it::class.java.simpleName }.toSet())
    }

    @Test fun `every answer the card gives has a row of its own, and every other row is the waiting line's`() {
        val rows = everyCardAnswer.map(::cardStatusHelp)

        assertEquals(CardStatusHelp.entries - WaitingOutlook.entries.map(::waitingHelp).toSet(), rows)
    }

    @Test fun `every outlook of the waiting line has a row of its own, worded as the card words it`() {
        val rows = WaitingOutlook.entries.map(::waitingHelp)

        assertEquals("one row each", WaitingOutlook.entries.size, rows.toSet().size)
        for (outlook in WaitingOutlook.entries) {
            assertEquals(outlook, (waitingHelp(outlook).words as CardWords.Waiting).outlook)
        }
    }

    @Test fun `every adherence status has a row of its own`() {
        val rows = AdherenceStatus.entries.map(::cardStatusHelp)

        assertEquals(AdherenceStatus.entries.size, rows.toSet().size)
    }

    @Test fun `a row is the colour the card is for that answer`() {
        for (model in everyCardAnswer) {
            assertEquals("$model", model.tone, cardStatusHelp(model).tone)
        }
        // Off schedule is red when early, as the sample is, and blue when late; Help shows the red.
        assertEquals(StatusTone.LATE, judged(47.6000, -122.3300, 6000).tone)
        assertEquals(StatusTone.EARLY, CardStatusHelp.OFF_SCHEDULE.tone)
    }

    @Test fun `the minutes rows read as the card counts them`() {
        assertEquals(CardWords.Minutes(0), CardStatusHelp.ON_TIME.words)
        assertTrue((CardStatusHelp.LATE.words as CardWords.Minutes).minutes > 0)
        assertTrue((CardStatusHelp.EARLY.words as CardWords.Minutes).minutes < 0)
    }

    @Test fun `every problem has a row of its own, and only the notification's row has no problem`() {
        assertNull(problemHelp(TrackingProblem.NONE))
        val rows = TrackingProblem.entries.filter { it != TrackingProblem.NONE }.map { assertNotNull("$it", problemHelp(it)); problemHelp(it) }

        assertEquals("one row each", rows.size, rows.toSet().size)
        assertEquals(ProblemHelp.entries.toSet() - ProblemHelp.REPORTS_PAUSED, rows.toSet())
    }

    @Test fun `the page runs in the spec's order`() {
        assertEquals(
            listOf(
                "welcome",
                "section-STATUS",
                "status-ON_TIME", "status-LATE", "status-EARLY", "status-OFF_SCHEDULE", "status-OFF_ROUTE",
                "status-WAITING_FOR_GPS", "status-NO_SCHEDULE", "status-REPORTS_WAITING", "status-REPORTS_WONT_SEND",
                "section-PROBLEMS",
                "problem-NO_CONNECTION", "problem-NO_GPS", "problem-CLOCK_WRONG", "problem-REPORTS_REFUSED",
                "problem-SERVER_ERROR", "problem-SIGNED_OUT", "problem-REPORTS_PAUSED",
            ),
            helpPage.map { it.key },
        )
    }

    @Test fun `each group is joined, round at its ends and tight between`() {
        val places = helpPage.filterIsInstance<HelpItem.Status>().map { it.place }

        assertEquals(GroupPlace.FIRST, places.first())
        assertEquals(GroupPlace.LAST, places.last())
        assertTrue(places.drop(1).dropLast(1).all { it == GroupPlace.MIDDLE })
    }
}
