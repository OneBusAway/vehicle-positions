package org.onebusaway.vehicletracker.ui.welcome

import androidx.compose.runtime.BroadcastFrameClock
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

private const val NANOS_PER_MILLI = 1_000_000L

/**
 * The welcome pictures' timelines, read at a time as the pictures read them: where each part is as its screen
 * appears, part-way through, and at rest.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class WelcomeTimelineTest {
    private fun SpringCurve.highest(untilMillis: Int = 3000) = (0..untilMillis).maxOf { settle(it.toFloat()) }

    // --- How things settle ---

    @Test fun `nothing has moved before its start`() {
        for (curve in listOf(WelcomeMotion.Rise, WelcomeMotion.Pop, WelcomeMotion.Fade, WelcomeMotion.SlowFade, WelcomeMotion.Glide)) {
            assertEquals(0f, curve.settle(-200f), 0f)
            assertEquals(0f, curve.settle(0f), 0f)
            assertEquals(0f, curve.hop(0f), 0f)
        }
    }

    @Test fun `colour and opacity never overshoot`() {
        assertTrue(WelcomeMotion.Fade.highest() <= 1f)
        assertTrue(WelcomeMotion.SlowFade.highest() <= 1f)
        assertEquals(1f, WelcomeMotion.Fade.settle(400f), 0.01f)
        assertEquals(1f, WelcomeMotion.SlowFade.settle(400f), 0.01f)
    }

    @Test fun `what moves settles a little past its place and comes back, the small things more`() {
        val rise = WelcomeMotion.Rise.highest()
        val pop = WelcomeMotion.Pop.highest()

        assertTrue("a card rising in barely passes its place: $rise", rise > 1f && rise < 1.03f)
        assertTrue("a check landing bounces: $pop", pop > 1.05f && pop < 1.15f)
        assertEquals(1f, WelcomeMotion.Rise.settle(700f), 0.01f)
        assertEquals(1f, WelcomeMotion.Pop.settle(700f), 0.01f)
    }

    @Test fun `the vehicle's glide lasts about 2400 ms and does not bounce`() {
        assertTrue(WelcomeMotion.Glide.highest(untilMillis = 6000) <= 1f)
        assertTrue("still on its way after a second", WelcomeMotion.Glide.settle(1000f) in 0.5f..0.85f)
        assertEquals(1f, WelcomeMotion.Glide.settle(2400f), 0.02f)
    }

    @Test fun `a hop goes up to one and comes back down`() {
        val heights = (0..600).map { WelcomeMotion.Pop.hop(it.toFloat()) }

        assertEquals(1f, heights.max(), 0.001f)
        assertTrue("it is up within a tenth of a second", heights.indexOf(heights.max()) < 100)
        assertEquals(0f, heights.last(), 0.02f)
    }

    // --- Screen 1: Riders can see you coming ---

    @Test fun `screen 1 starts with the vehicle waiting and nothing else`() {
        for (millis in listOf(0f, 499f)) {
            assertEquals(0f, RidersTimeline.live(millis), 0f)
            assertEquals(0f, RidersTimeline.glide(millis), 0f)
            assertEquals(0f, RidersTimeline.onPhone(millis), 0f)
            assertEquals(0f, RidersTimeline.chipLit(millis), 0f)
            assertEquals(0f, RidersTimeline.rest(millis), 0f)
            assertEquals(0, RidersTimeline.sent(millis))
            for (ring in 0 until RidersTimeline.RINGS) assertEquals(-1f, RidersTimeline.ring(ring, millis), 0f)
            for (rider in 0 until RidersTimeline.RIDERS) assertTrue(RidersTimeline.sinceWave(rider, millis) < 0f)
        }
    }

    @Test fun `at 500 ms the vehicle goes live and starts to glide, and shows on the phone 300 ms later`() {
        assertTrue(RidersTimeline.live(600f) > 0f)
        assertTrue(RidersTimeline.glide(600f) > 0f)
        assertEquals(0f, RidersTimeline.onPhone(800f), 0f)
        assertTrue(RidersTimeline.onPhone(900f) > 0f)
        assertEquals("the ring is green well before the glide is over", 1f, RidersTimeline.live(1000f), 0.01f)
        assertTrue(RidersTimeline.glide(1000f) < 0.5f)
    }

    @Test fun `three rings leave the vehicle half a second apart, each for a second and a half`() {
        val leaves = listOf(500f, 1000f, 1500f)
        for ((ring, left) in leaves.withIndex()) {
            assertEquals("not yet", -1f, RidersTimeline.ring(ring, left), 0f)
            assertTrue(RidersTimeline.ring(ring, left + 10f) in 0f..0.2f)
            assertTrue("it eases out: most of the way in half the time", RidersTimeline.ring(ring, left + 750f) > 0.7f)
            assertTrue(RidersTimeline.ring(ring, left + 1490f) > 0.99f)
            assertEquals("gone", -1f, RidersTimeline.ring(ring, left + 1500f), 0f)
        }
    }

    @Test fun `the riders' chip lights at 1200 ms and they wave one after another`() {
        assertEquals(0f, RidersTimeline.chipLit(1200f), 0f)
        assertTrue(RidersTimeline.chipLit(1300f) > 0f)
        assertEquals(0f, RidersTimeline.sinceWave(0, 1300f), 0f)
        assertEquals(0f, RidersTimeline.sinceWave(1, 1620f), 0f)
        assertEquals(0f, RidersTimeline.sinceWave(2, 1940f), 0f)
        assertTrue("the third still stands while the second waves", RidersTimeline.sinceWave(2, 1700f) < 0f)
    }

    @Test fun `the sent chip counts 1, 2 and 3`() {
        assertEquals(0, RidersTimeline.sent(1499f))
        assertEquals(1, RidersTimeline.sent(1500f))
        assertEquals(0f, RidersTimeline.sinceSentChip(1500f), 0f)
        assertEquals(1, RidersTimeline.sent(2199f))
        assertEquals(2, RidersTimeline.sent(2200f))
        assertEquals(2, RidersTimeline.sent(2899f))
        assertEquals(3, RidersTimeline.sent(2900f))
    }

    @Test fun `screen 1 rests inside 4 seconds, with the vehicle at its place and two still rings`() {
        val end = RidersTimeline.END

        assertTrue(end <= 4000f)
        assertEquals(0f, RidersTimeline.rest(3400f), 0f)
        assertEquals(1f, RidersTimeline.rest(end), 0.01f)
        assertEquals(1f, RidersTimeline.glide(end), 0.005f)
        assertEquals(1f, RidersTimeline.live(end), 0.001f)
        assertEquals(1f, RidersTimeline.onPhone(end), 0.001f)
        assertEquals(1f, RidersTimeline.chipLit(end), 0.001f)
        assertEquals(3, RidersTimeline.sent(end))
        for (ring in 0 until RidersTimeline.RINGS) assertEquals(-1f, RidersTimeline.ring(ring, end), 0f)
        for (rider in 0 until RidersTimeline.RIDERS) assertTrue(RidersTimeline.sinceWave(rider, end) > 1000f)
    }

    // --- Screen 2: Vehicle, route, run ---

    @Test fun `screen 2's cards rise in 120 ms apart and fill half a second apart`() {
        assertEquals(listOf(0f, 120f, 240f), List(3, PiecesTimeline::cardRisesAt))
        assertEquals(listOf(600f, 1100f, 1600f), List(3, PiecesTimeline::cardFillsAt))
    }

    @Test fun `the green card rises in with 1 sent, counts on, and the last row follows`() {
        assertEquals(2100f, PiecesTimeline.LIVE_RISES_AT, 0f)
        assertEquals(1, PiecesTimeline.sent(2100f))
        assertEquals(1, PiecesTimeline.sent(2799f))
        assertEquals(2, PiecesTimeline.sent(2800f))
        assertEquals(2, PiecesTimeline.sent(3149f))
        assertEquals(3, PiecesTimeline.sent(3150f))
        assertEquals(3400f, PiecesTimeline.FINISH_RISES_AT, 0f)
        assertEquals(3, PiecesTimeline.sent(PiecesTimeline.END))
        assertTrue("the last row has risen in by the end", WelcomeMotion.Rise.settle(PiecesTimeline.END - PiecesTimeline.FINISH_RISES_AT) > 0.99f)
        assertTrue(PiecesTimeline.END <= 4000f)
    }

    // --- Screen 3: Keep it reporting ---

    @Test fun `screen 3's rows rise in a quarter of a second apart, each icon moving after its row`() {
        assertEquals(listOf(150f, 400f, 650f), List(3, HabitsTimeline::rowRisesAt))
        assertEquals(listOf(400f, 650f, 900f), List(3, HabitsTimeline::iconMovesAt))
    }

    @Test fun `the label and the Reports paused card come last, and it rests by 1600 ms`() {
        assertEquals(1000f, HabitsTimeline.LABEL_SHOWS_AT, 0f)
        assertEquals(1100f, HabitsTimeline.CARD_RISES_AT, 0f)
        assertTrue(WelcomeMotion.Rise.settle(HabitsTimeline.END - HabitsTimeline.CARD_RISES_AT) > 0.99f)
        assertTrue(WelcomeMotion.Pop.settle(HabitsTimeline.END - HabitsTimeline.iconMovesAt(2)) > 0.99f)
        assertEquals(1600f, HabitsTimeline.END, 0f)
    }

    // --- The clock a picture reads ---

    /** One frame at [millis] on the frame clock, and whatever it sets going. */
    private fun TestScope.frame(frames: BroadcastFrameClock, millis: Long) {
        frames.sendFrame(millis * NANOS_PER_MILLI)
        runCurrent()
    }

    @Test fun `the clock counts frames, not time on the wall`() = runTest {
        val frames = BroadcastFrameClock()
        val clock = PictureClock(endMillis = 3900f) { false }
        val playing = launch(frames) { clock.play() }
        runCurrent()
        assertEquals("nothing moves before a frame is drawn", 0f, clock.millis, 0f)

        frame(frames, 10_000)
        assertEquals(0f, clock.millis, 0f)
        frame(frames, 11_700)
        assertEquals(1700f, clock.millis, 1f)
        // A slow frame moves the whole picture on by as much, so no part is out of step with another.
        frame(frames, 12_900)
        assertEquals(2900f, clock.millis, 1f)
        frame(frames, 20_000)
        assertEquals(PictureClock.AT_REST, clock.millis, 0f)
        runCurrent()
        assertTrue("it plays once and stops", playing.isCompleted)
    }

    @Test fun `a picture cut off plays on from where it was, and one rewound starts clean`() = runTest {
        val frames = BroadcastFrameClock()
        val clock = PictureClock(endMillis = 3900f) { false }
        val first = launch(frames) { clock.play() }
        runCurrent()
        frame(frames, 0)
        frame(frames, 1000)
        first.cancelAndJoin()
        assertEquals(1000f, clock.millis, 1f)

        val second = launch(frames) { clock.play() }
        runCurrent()
        frame(frames, 5000)
        frame(frames, 5500)
        assertEquals("no jump back, and no time lost while it was cut off", 1500f, clock.millis, 1f)
        frame(frames, 9000)
        assertEquals(PictureClock.AT_REST, clock.millis, 0f)
        second.join()

        clock.rewind()
        assertEquals(0f, clock.millis, 0f)
    }

    @Test fun `with animations removed the picture is finished from the first frame`() = runTest {
        val clock = PictureClock(endMillis = 1600f) { true }

        assertEquals(PictureClock.AT_REST, clock.millis, 0f)
    }

    @Test fun `at rest every part is exactly in its place, not a fraction of a pixel out`() {
        val rest = PictureClock.AT_REST
        for (curve in listOf(WelcomeMotion.Rise, WelcomeMotion.Pop, WelcomeMotion.Fade, WelcomeMotion.SlowFade, WelcomeMotion.Glide)) {
            assertEquals(1f, curve.settle(rest), 0f)
            assertEquals(0f, curve.hop(rest), 0f)
        }
        assertEquals(1f, RidersTimeline.glide(rest), 0f)
        assertEquals(1f, RidersTimeline.live(rest), 0f)
        assertEquals(1f, RidersTimeline.rest(rest), 0f)
        assertEquals(3, RidersTimeline.sent(rest))
        assertEquals(3, PiecesTimeline.sent(rest))
        for (ring in 0 until RidersTimeline.RINGS) assertEquals(-1f, RidersTimeline.ring(ring, rest), 0f)
        assertTrue("the spring alone is still a little short of it at the last millisecond", WelcomeMotion.Rise.settle(500f) != 1f)
    }
}
