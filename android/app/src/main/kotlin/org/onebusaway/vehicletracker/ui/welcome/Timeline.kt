package org.onebusaway.vehicletracker.ui.welcome

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationVector1D
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.VectorConverter
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Stable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import kotlin.math.roundToInt

/**
 * One welcome picture's timeline: the milliseconds since its screen appeared, counted on the frame clock. Every
 * part of the picture is worked out from [millis] where it is drawn, so a frame of it redraws and composes nothing,
 * a slow frame can't put one part out of step with another, and a test can step it.
 *
 * Once it has played out it reads [AT_REST], and with animations removed ([still]) it does from the first frame.
 */
@Stable
internal class PictureClock(private val endMillis: Float, private val still: () -> Boolean) {
    private val time = Animatable(0f)

    val millis: Float get() = if (still() || time.value >= endMillis) AT_REST else time.value

    /** Runs on to the end from wherever it is, so a picture cut off by a tap and shown again is never left half played. */
    suspend fun play() {
        val left = endMillis - time.value
        if (left > 0f) time.animateTo(endMillis, tween(left.roundToInt(), easing = LinearEasing))
    }

    suspend fun rewind() = time.snapTo(0f)

    companion object {
        /**
         * What [millis] reads when the picture has played out. A spring only ever nears its place, so a picture that
         * stopped at its last millisecond would rest a fraction of a pixel out; at this every part is exactly home.
         */
        const val AT_REST = Float.POSITIVE_INFINITY
    }
}

/**
 * A spring's path from rest, read off at a time rather than run. The times in a timeline say when a part starts; this
 * says how it settles.
 */
internal class SpringCurve(spec: FiniteAnimationSpec<Float>) {
    private val spring = spec.vectorize(Float.VectorConverter)
    private val zero = AnimationVector1D(0f)
    private val one = AnimationVector1D(1f)

    // The push that sends a part at rest up to exactly 1 before the spring brings it back.
    private val unitPush = AnimationVector1D(1f / (1..HOP_SEARCH_MILLIS).maxOf { pushed(it.toFloat(), one) })

    /** From 0 towards 1, [millis] after it was let go: 0 before that, and exactly 1 [PictureClock.AT_REST]. */
    fun settle(millis: Float): Float = when {
        millis <= 0f -> 0f
        millis == PictureClock.AT_REST -> 1f
        else -> spring.getValueFromNanos(millis.toNanos(), zero, one, zero).value
    }

    /** Up to 1 and back down to 0, as after one push: 0 before it, and exactly 0 [PictureClock.AT_REST]. */
    fun hop(millis: Float): Float = if (millis <= 0f || millis == PictureClock.AT_REST) 0f else pushed(millis, unitPush)

    private fun pushed(millis: Float, push: AnimationVector1D): Float =
        spring.getValueFromNanos(millis.toNanos(), zero, zero, push).value

    private fun Float.toNanos(): Long = (this * NANOS_PER_MILLI).toLong()

    private companion object {
        const val NANOS_PER_MILLI = 1_000_000L
        const val HOP_SEARCH_MILLIS = 400
    }
}

/** The curves the three pictures share. Colour and opacity take the effects springs, which never overshoot. */
internal object WelcomeMotion {
    /** For a card or a chip rising into its place. */
    val Rise = SpringCurve(AppMotion.expressiveDefaultSpatialSpec())

    /** For something small landing: a check, a rider's wave, an icon. */
    val Pop = SpringCurve(AppMotion.expressiveFastSpatialSpec())

    val Fade = SpringCurve(AppMotion.defaultEffectsSpec())

    val SlowFade = SpringCurve(AppMotion.slowEffectsSpec())

    /**
     * The vehicle's glide up its route, the one move here that lasts seconds: Material's slowest spring is over in
     * half of one. This one has no bounce and is all but there in the 2.4 s the timeline gives it.
     */
    val Glide = SpringCurve(spring(dampingRatio = Spring.DampingRatioNoBouncy, stiffness = 8f))
}

/** How far "rises in" rises from. */
private val RiseFrom = 10.dp

/**
 * "Rises in": from 10 dp below and invisible to its place, with a fade, starting [atMillis] into [clock]'s timeline.
 * The piece is laid out in its place from the first frame; only its layer moves.
 */
internal fun Modifier.risesIn(clock: PictureClock, atMillis: Float): Modifier = graphicsLayer {
    val since = clock.millis - atMillis
    translationY = (1f - WelcomeMotion.Rise.settle(since)) * RiseFrom.toPx()
    alpha = WelcomeMotion.Fade.settle(since)
}

/**
 * Screen 1, "Riders can see you coming": when each part starts, in milliseconds from the screen appearing. The
 * vehicle goes live and glides up to its place, rings leave it, the riders at the stop turn and wave, the same
 * vehicle shows on a rider's phone and the sent count ticks up. It ends inside 4 seconds and does not loop.
 */
internal object RidersTimeline {
    const val END = 3900f
    const val RIDERS = 3
    const val RINGS = 3

    private const val GOES_LIVE = 500f
    private const val SHOWS_ON_PHONE = 800f
    private const val RING_EVERY = 500f
    private const val RING_LASTS = 1500f
    private const val CHIP_LIGHTS = 1200f
    private const val FIRST_WAVE = 1300f
    private const val WAVE_EVERY = 320f
    private const val FIRST_SENT = 1500f
    private const val SENT_EVERY = 700f
    private const val RESTS = 3400f

    /** 0 while the vehicle waits with a grey ring, 1 once it reports and the ring is green. */
    fun live(millis: Float): Float = WelcomeMotion.SlowFade.settle(millis - GOES_LIVE)

    /** How far the vehicle is along its glide: 0 where it starts, 1 at its place. */
    fun glide(millis: Float): Float = WelcomeMotion.Glide.settle(millis - GOES_LIVE)

    /** How strongly the vehicle shows on the rider's phone. */
    fun onPhone(millis: Float): Float = WelcomeMotion.SlowFade.settle(millis - SHOWS_ON_PHONE)

    /** How far ring [index] is on its way out from the vehicle, 0 to 1, or -1 when it is not in flight. */
    fun ring(index: Int, millis: Float): Float {
        val flown = (millis - GOES_LIVE - index * RING_EVERY) / RING_LASTS
        return if (flown <= 0f || flown >= 1f) -1f else LinearOutSlowInEasing.transform(flown)
    }

    /** How far the riders' chip has turned from plain to lit. */
    fun chipLit(millis: Float): Float = WelcomeMotion.SlowFade.settle(millis - CHIP_LIGHTS)

    /** The milliseconds since rider [index] turned and waved; negative while that rider still stands plain. */
    fun sinceWave(index: Int, millis: Float): Float = millis - FIRST_WAVE - index * WAVE_EVERY

    /** How many reports the sent chip counts: 0 before it appears, then 1, 2 and 3. */
    fun sent(millis: Float): Int = when {
        millis < FIRST_SENT -> 0
        millis < FIRST_SENT + SENT_EVERY -> 1
        millis < FIRST_SENT + 2 * SENT_EVERY -> 2
        else -> 3
    }

    /** The milliseconds since the sent chip rose in. */
    fun sinceSentChip(millis: Float): Float = millis - FIRST_SENT

    /** How strongly the two still rings round the resting vehicle show. */
    fun rest(millis: Float): Float = WelcomeMotion.SlowFade.settle(millis - RESTS)
}

/**
 * Screen 2, "Vehicle, route, run": when each part starts. The three cards rise in empty, each fills in turn, the
 * green card rises in and its count ticks up, and the last row follows.
 */
internal object PiecesTimeline {
    const val END = 3900f
    const val LIVE_RISES_AT = 2100f
    const val FINISH_RISES_AT = 3400f

    private const val CARD_EVERY = 120f
    private const val FIRST_FILL = 600f
    private const val FILL_EVERY = 500f
    private const val SECOND_SENT = 2800f
    private const val THIRD_SENT = 3150f

    fun cardRisesAt(index: Int): Float = index * CARD_EVERY

    /** When card [index] fills: its background, its icon's holder and its check. */
    fun cardFillsAt(index: Int): Float = FIRST_FILL + index * FILL_EVERY

    /** What the green card's count says: 1 as it rises in, then 2 and 3. */
    fun sent(millis: Float): Int = when {
        millis < SECOND_SENT -> 1
        millis < THIRD_SENT -> 2
        else -> 3
    }
}

/**
 * Screen 3, "Keep it reporting": when each part starts. The three rows rise in, each one's icon makes one small
 * move, then the label shows and the Reports paused card rises in.
 */
internal object HabitsTimeline {
    const val END = 1600f
    const val LABEL_SHOWS_AT = 1000f
    const val CARD_RISES_AT = 1100f

    private const val FIRST_ROW = 150f
    private const val FIRST_ICON = 400f
    private const val ROW_EVERY = 250f

    fun rowRisesAt(index: Int): Float = FIRST_ROW + index * ROW_EVERY

    fun iconMovesAt(index: Int): Float = FIRST_ICON + index * ROW_EVERY
}
