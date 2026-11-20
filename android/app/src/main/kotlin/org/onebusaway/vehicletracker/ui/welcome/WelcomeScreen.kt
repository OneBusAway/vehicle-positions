package org.onebusaway.vehicletracker.ui.welcome

import androidx.activity.compose.PredictiveBackHandler
import androidx.annotation.StringRes
import androidx.compose.animation.core.SeekableTransitionState
import androidx.compose.animation.core.Transition
import androidx.compose.animation.core.animate
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.rememberTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.isTraversalGroup
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.traversalIndex
import androidx.compose.ui.text.style.LineBreak
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.language.LanguageRow
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.SharedAxisSlide
import org.onebusaway.vehicletracker.ui.theme.sharedAxisFadeInSpec
import org.onebusaway.vehicletracker.ui.theme.sharedAxisFadeOutSpec
import org.onebusaway.vehicletracker.ui.theme.sharedAxisSlideSpec
import kotlin.math.max

internal const val WELCOME_SCREENS = 3
private const val LAST_SCREEN = WELCOME_SCREENS - 1
private const val NO_BACK_GESTURE = -1f
private const val NANOS_PER_MILLI = 1_000_000L

/** Inside the system bars, a window shorter than this gets the small-phone sizes, as a 360 x 640 dp phone does. */
private val CompactBelow = 700.dp
private val SideMargin = 24.dp

/** Screen 1's picture fills what the words leave, but is never shorter than this: the screen scrolls instead. */
private val LeastPicture = 200.dp

/**
 * The welcome: three short screens a driver sees once, after the first sign-in on this phone and before choosing a
 * vehicle. [onDone] is where it goes when the driver leaves it, by Skip, by Continue on the last screen or by Back
 * on the first. With [marksSeen] false nothing is recorded, for when the welcome is opened to be read again.
 */
@Composable
fun WelcomeScreen(onDone: () -> Unit, marksSeen: Boolean = true, viewModel: WelcomeViewModel = hiltViewModel()) {
    val left by viewModel.left.collectAsStateWithLifecycle()
    LaunchedEffect(left) { if (left) onDone() }
    WelcomeContent(onLeave = { viewModel.leave(marksSeen) })
}

/**
 * Which of the welcome's screens the driver is on, the move between them, and each screen's picture clock. With
 * animations removed on the phone, read from Compose's own [MotionDurationScale], it is [still]: every picture is
 * finished from its first frame and a back gesture scrubs nothing. The screens then change with no slide without
 * being told to, because Compose ends a move at once at that scale.
 */
@Stable
internal class WelcomeState(initialScreen: Int, private val motionScale: MotionDurationScale?) {
    /** The screen the driver is on, or on the way to. */
    var screen by mutableIntStateOf(initialScreen)
        private set

    /** How far a back gesture has pulled towards the screen before, 0 to 1, or [NO_BACK_GESTURE]. */
    var backProgress by mutableFloatStateOf(NO_BACK_GESTURE)
        private set

    val backGestureUnderWay: Boolean get() = backProgress != NO_BACK_GESTURE

    /** Seekable, so the back gesture can scrub the same move that Next and the back arrow play. */
    val steps = SeekableTransitionState(initialScreen)

    val clocks: List<PictureClock> =
        listOf(RidersTimeline.END, PiecesTimeline.END, HabitsTimeline.END).map { end -> PictureClock(end) { still } }

    val still: Boolean get() = motionScale?.scaleFactor == 0f

    /** On to the next screen. False on the last one, which has none: the driver is leaving the welcome. */
    fun next(): Boolean {
        if (screen == LAST_SCREEN) return false
        screen++
        return true
    }

    /** Back a screen, by the arrow or a finished back gesture. False on the first one: the driver is leaving. */
    fun back(): Boolean {
        backProgress = NO_BACK_GESTURE
        if (screen == 0) return false
        screen--
        return true
    }

    /** A back gesture under way. The first screen has nothing behind it to show, and [still] shows nothing moving. */
    fun pullBack(progress: Float) {
        if (screen > 0 && !still) backProgress = progress
    }

    /** The back gesture was let go without going back. */
    fun letGo() {
        backProgress = NO_BACK_GESTURE
    }

    /** Scrubs towards the screen before, as far as the gesture has pulled. */
    suspend fun followBackGesture() {
        steps.seekTo(backProgress.coerceIn(0f, 1f), screen - 1)
    }

    /**
     * Moves to [screen] from wherever the screens are, so that fast taps retarget the move rather than jump.
     * [totalMillis] is how long the whole move takes.
     */
    suspend fun settle(totalMillis: () -> Long) {
        val target = screen
        when {
            steps.currentState != target -> steps.animateTo(target)
            steps.targetState != target -> comeBackTo(target, totalMillis())
        }
    }

    /**
     * Back to the screen that was being left, from however far the move had got: after Next and Back in one breath,
     * or a back gesture let go. The move is played backwards to its start, as NavHost plays its own.
     */
    private suspend fun comeBackTo(target: Int, totalMillis: Long) = coroutineScope {
        val reached = steps.fraction
        animate(reached, 0f, animationSpec = tween((reached * totalMillis).toInt())) { fraction, _ ->
            launch { if (fraction > 0f) steps.seekTo(fraction) else steps.snapTo(target) }
        }
    }

    companion object {
        /** The screen survives a rotation and the process being stopped; the pictures play again. */
        fun saver(motionScale: MotionDurationScale?) =
            Saver<WelcomeState, Int>(save = { it.screen }, restore = { WelcomeState(it, motionScale) })
    }
}

@Composable
internal fun rememberWelcomeState(): WelcomeState {
    // Compose keeps the phone's animator duration scale in the composition's coroutine context.
    val motionScale = rememberCoroutineScope().coroutineContext[MotionDurationScale]
    return rememberSaveable(motionScale, saver = WelcomeState.saver(motionScale)) { WelcomeState(0, motionScale) }
}

/**
 * The three screens in one frame. The top row (Back, the step line, Skip) and the button stay where they are; only
 * the title, the sentence and the picture move between screens. Nothing but Back, Skip, the button and the first
 * screen's Language row can be tapped, and no screen moves on by itself.
 */
@Composable
internal fun WelcomeContent(onLeave: () -> Unit, modifier: Modifier = Modifier, state: WelcomeState = rememberWelcomeState()) {
    val steps = rememberTransition(state.steps, label = "welcome screen")
    StepsDriver(state, steps)
    PredictiveBackHandler { progress ->
        try {
            progress.collect { state.pullBack(it.progress) }
            // Back on the first screen leaves the welcome, as Skip does.
            if (!state.back()) onLeave()
        } catch (e: CancellationException) {
            state.letGo()
            throw e
        }
    }

    BoxWithConstraints(modifier.fillMaxSize()) {
        val compact = maxHeight < CompactBelow
        Column(Modifier.fillMaxSize()) {
            // The line's vehicle slides to its stop on a spring, and under the back gesture with the screens.
            val vehicleOnLine = steps.animateFloat({ AppMotion.expressiveDefaultSpatialSpec() }, label = "step") { screen ->
                screen / LAST_SCREEN.toFloat()
            }
            TopRow(state.screen, vehicleOnLine, compact, onBack = { state.back() }, onSkip = onLeave)
            Screens(state, steps, compact, Modifier.weight(1f).fillMaxWidth().semantics { isTraversalGroup = true })
            Button(
                onClick = { if (!state.next()) onLeave() },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(start = SideMargin, top = 24.dp, end = SideMargin, bottom = if (compact) 12.dp else 24.dp)
                    .heightIn(min = 56.dp)
                    // Read after the screen's own words and before the top row, whatever their places on screen.
                    .semantics { traversalIndex = 1f },
            ) {
                val words = if (state.screen == LAST_SCREEN) R.string.welcome_continue else R.string.welcome_next
                Text(stringResource(words), style = MaterialTheme.typography.titleMedium)
            }
        }
    }
}

/**
 * Keeps the screens' move in step with [state]: scrubbed by a back gesture while one is under way, and otherwise
 * settling on the screen the driver chose. On its own so that a gesture's progress recomposes nothing else.
 */
@Composable
private fun StepsDriver(state: WelcomeState, steps: Transition<Int>) {
    if (state.backGestureUnderWay) {
        LaunchedEffect(state.backProgress) { state.followBackGesture() }
    } else {
        LaunchedEffect(state.screen) { state.settle { steps.totalDurationNanos / NANOS_PER_MILLI } }
    }
}

@Composable
private fun TopRow(screen: Int, vehicleOnLine: State<Float>, compact: Boolean, onBack: () -> Unit, onSkip: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(56.dp)
            .padding(start = 4.dp, end = 12.dp)
            .semantics {
                isTraversalGroup = true
                traversalIndex = 2f
            },
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        BackSlot(shown = screen > 0, onBack = onBack)
        StepLine(screen, vehicleOnLine, compact, Modifier.weight(1f))
        TextButton(onClick = onSkip, modifier = Modifier.semantics { traversalIndex = 1f }) {
            Text(stringResource(R.string.welcome_skip))
        }
    }
}

/**
 * Back keeps its place when the first screen hides it, so the step line does not shift. Hidden, it is still
 * composed, as the screens ahead are, so the first Next has no button to build: it only fades in. It is no button to
 * TalkBack or to a tap until it shows.
 */
@Composable
private fun BackSlot(shown: Boolean, onBack: () -> Unit) {
    val showing = animateFloatAsState(if (shown) 1f else 0f, AppMotion.defaultEffectsSpec(), label = "back")
    IconButton(
        onClick = onBack,
        enabled = shown,
        modifier = Modifier
            .size(48.dp)
            .graphicsLayer { alpha = showing.value }
            .semantics { traversalIndex = 2f }
            .then(if (shown) Modifier else Modifier.clearAndSetSemantics { }),
    ) {
        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up))
    }
}

/**
 * The three screens' words and pictures, one over another. All three stay composed: the one the welcome opens on
 * first, the others in the frames after it, while nothing is moving yet. Going to a screen then only moves layers
 * along the app's shared axis, and composes nothing at the tap.
 */
@Composable
private fun Screens(state: WelcomeState, steps: Transition<Int>, compact: Boolean, modifier: Modifier = Modifier) {
    val towardsStart = if (LocalLayoutDirection.current == LayoutDirection.Rtl) -1f else 1f
    val slide = with(LocalDensity.current) { SharedAxisSlide.toPx() } * towardsStart
    val opensOn = remember { state.screen }
    var warmedUp by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) {
        repeat(LAST_SCREEN) {
            withFrameNanos { }
            warmedUp++
        }
    }
    Box(modifier) {
        for (index in 0 until WELCOME_SCREENS) {
            key(index) {
                // A screen the driver is already going to is not kept waiting for its turn.
                if (index == opensOn || index == state.screen || warmUpTurn(index, opensOn) <= warmedUp) {
                    Screen(index, state.clocks[index], steps, slide, compact)
                }
            }
        }
    }
}

/** The other screens' turns to be composed once the first is up: the ones ahead, then the ones behind, nearest first. */
private fun warmUpTurn(index: Int, opensOn: Int): Int =
    if (index > opensOn) index - opensOn else LAST_SCREEN - index

/** Where a screen is, to its picture: its timeline runs while it is the screen shown, and starts again once it has gone. */
private enum class Showing { SHOWN, LEAVING, GONE }

@Composable
private fun Screen(index: Int, clock: PictureClock, steps: Transition<Int>, slide: Float, compact: Boolean) {
    // The app's shared axis X, as its setup steps move: a short slide towards the start going forward, the old
    // screen fading out before the new one fades in.
    val offset = steps.animateFloat({ sharedAxisSlideSpec() }, label = "slide") { screen ->
        (index - screen).coerceIn(-1, 1) * slide
    }
    val alpha = steps.animateFloat(
        transitionSpec = { if (targetState == index) sharedAxisFadeInSpec() else sharedAxisFadeOutSpec() },
        label = "fade",
    ) { screen -> if (screen == index) 1f else 0f }
    val shown = steps.targetState == index

    LaunchedEffect(clock) {
        snapshotFlow {
            when {
                steps.targetState == index -> Showing.SHOWN
                alpha.value > 0f -> Showing.LEAVING
                else -> Showing.GONE
            }
        }.collectLatest { showing ->
            when (showing) {
                // From where it was: a picture cut off by a tap and come back to is played out, not left half done.
                Showing.SHOWN -> clock.play()
                Showing.LEAVING -> Unit
                Showing.GONE -> clock.rewind()
            }
        }
    }

    val title = remember { FocusRequester() }
    // TalkBack reads the screen the welcome opens on from its top anyway; one arrived at by Next or Back it is sent to.
    var opening by remember { mutableStateOf(shown) }
    LaunchedEffect(shown) {
        if (shown && !opening) title.requestFocus()
        opening = false
    }

    Box(
        Modifier
            .fillMaxSize()
            // The screen shown is the one a drag scrolls.
            .zIndex(if (shown) 1f else 0f)
            .graphicsLayer {
                translationX = offset.value
                this.alpha = alpha.value
            }
            .then(if (shown) Modifier else Modifier.clearAndSetSemantics { }),
    ) {
        when (index) {
            0 -> FillingScreen(
                words = {
                    ScreenWords(R.string.welcome_riders_title, R.string.welcome_riders_body, compact, title, shown)
                    // Its globe marks it for a driver who can't read the words around it.
                    LanguageRow(Modifier.padding(bottom = if (compact) 12.dp else 24.dp), enabled = shown)
                },
                picture = { RidersPicture(clock, compact) },
            )
            1 -> ScrollingScreen {
                ScreenWords(R.string.welcome_pieces_title, R.string.welcome_pieces_body, compact, title, shown)
                PiecesPicture(clock, compact)
            }
            else -> ScrollingScreen {
                ScreenWords(R.string.welcome_habits_title, R.string.welcome_habits_body, compact, title, shown)
                HabitsPicture(clock, compact)
            }
        }
    }
}

/** The title, a heading that takes focus only on the screen [shown], and the one sentence under it. */
@Composable
private fun ScreenWords(@StringRes title: Int, @StringRes body: Int, compact: Boolean, focus: FocusRequester, shown: Boolean) {
    val type = MaterialTheme.typography
    Text(
        text = stringResource(title),
        style = (if (compact) type.headlineLarge else type.displaySmall).copy(lineBreak = LineBreak.Heading),
        modifier = Modifier
            .padding(top = if (compact) 0.dp else 8.dp)
            .semantics { heading() }
            .focusRequester(focus)
            .focusable(enabled = shown),
    )
    Text(
        text = stringResource(body),
        style = type.bodyLarge.copy(lineBreak = LineBreak.Paragraph),
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 8.dp, bottom = if (compact) 12.dp else 24.dp),
    )
}

/** A screen whose picture is as tall as it is: with large text or on a short screen it scrolls under the button. */
@Composable
private fun ScrollingScreen(content: @Composable () -> Unit) {
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = SideMargin)) { content() }
}

/**
 * A screen whose [picture] takes the room its [words] leave, down to [LeastPicture]. When the words leave less, as
 * at the largest text on a short screen, the picture keeps that height and the screen scrolls.
 */
@Composable
private fun FillingScreen(words: @Composable () -> Unit, picture: @Composable () -> Unit) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val room = constraints.maxHeight
        Layout(
            content = {
                Column { words() }
                picture()
            },
            modifier = Modifier.verticalScroll(rememberScrollState()).padding(horizontal = SideMargin),
        ) { (wordsBox, pictureBox), constraints ->
            val wordsPlaced = wordsBox.measure(constraints.copy(minHeight = 0))
            val pictureHeight = max(room - wordsPlaced.height, LeastPicture.roundToPx())
            val picturePlaced = pictureBox.measure(Constraints.fixed(constraints.maxWidth, pictureHeight))
            layout(constraints.maxWidth, wordsPlaced.height + pictureHeight) {
                wordsPlaced.placeRelative(0, 0)
                picturePlaced.placeRelative(0, wordsPlaced.height)
            }
        }
    }
}
