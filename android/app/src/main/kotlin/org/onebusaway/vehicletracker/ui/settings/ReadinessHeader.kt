package org.onebusaway.vehicletracker.ui.settings

import android.icu.text.ListFormatter
import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.text.BasicText
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.layoutId
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.isTraversalGroup
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.traversalIndex
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Velocity
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.textOf
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.tracking.waitingWords

private val BarHeight = 64.dp

/** The words start this far up into the bar, whose own line is centred in it. */
private val WordsIntoBar = 4.dp

/** From the words down to the header's lower edge, and what the checks' icons add to it. */
private val LineZone = 42.dp
private val IconZone = 28.dp

/** What is left under the bar once the header has folded. */
private val FoldedEdge = 8.dp

/** How far the title is scrolled before it has faded. */
private val TitleFade = 30.dp

/** From this text size up the checks' icons give way to the words. */
private const val LARGE_TEXT_FROM = 1.3f

/** How long a change waits before the header shows it, so a driver back from the phone's settings sees it happen. */
private const val SHOW_CHANGE_AFTER_MILLIS = 250L

/**
 * How far the header has folded. It folds as the page under it scrolls: it moves up with the page until only its bar
 * and its line are left, then stays. [range] is how far that is, set as the header is laid out.
 */
@Stable
internal class HeaderFold(private val scroll: ScrollState) {
    var range by mutableIntStateOf(0)

    /** How tall the header is once folded, set with [range]. */
    var folded by mutableIntStateOf(0)

    val offset: Float get() = scroll.value.coerceAtMost(range).toFloat()
    val fraction: Float get() = if (range == 0) 0f else offset / range

    /** How far past the fold the page is, which the header stays put against. */
    val pastFold: Float get() = (scroll.value - range).coerceAtLeast(0).toFloat()

    /**
     * Let go in the middle of the fold, it settles to the nearer end, unless the page is too short to fold it all. Only
     * a finger's scroll settles: a row that TalkBack or a keyboard scrolled to stays in view.
     */
    val settling = object : NestedScrollConnection {
        override suspend fun onPostFling(consumed: Velocity, available: Velocity): Velocity {
            if (range <= scroll.maxValue && scroll.value in 1 until range) {
                scroll.animateScrollTo(if (scroll.value * 2 < range) 0 else range, AppMotion.defaultSpatialSpec())
            }
            return Velocity.Zero
        }
    }
}

/** What the header says: a pure reading of [Readiness], so two that say the same are the same. */
private data class HeaderWords(
    val title: String,
    val sentence: String,
    @param:DrawableRes val icon: Int?,
    val iconIsProblem: Boolean,
    /** What the folded bar says under "Settings". */
    val folded: String,
)

@Composable
private fun headerWords(readiness: Readiness): HeaderWords {
    val ready = stringResource(R.string.settings_ready)
    val notReady = stringResource(R.string.settings_not_ready)
    return when (val state = readiness.state) {
        ReadinessState.LocationRefused -> HeaderWords(notReady, stringResource(R.string.settings_location_refused), null, false, notReady)
        ReadinessState.LocationOff -> HeaderWords(notReady, stringResource(R.string.settings_location_off), null, false, notReady)
        is ReadinessState.Ready -> {
            val toFix = state.toFix
            val waiting = state.waiting
            when {
                toFix.isNotEmpty() -> {
                    val count = pluralStringResource(R.plurals.settings_fix_many, toFix.size, toFix.size)
                    HeaderWords(ready, if (toFix.size <= NAMED_AT_MOST) toFixText(toFix.map { stringResource(textOf(it).title) }) else count, R.drawable.ic_error, true, count)
                }
                waiting != null -> HeaderWords(
                    title = ready,
                    sentence = pluralStringResource(waitingWords(waiting.outlook), waiting.count, waiting.count),
                    icon = if (waiting.outlook == WaitingOutlook.NEVER) R.drawable.ic_sync_problem else R.drawable.ic_schedule_outline,
                    iconIsProblem = waiting.outlook == WaitingOutlook.NEVER,
                    folded = ready,
                )
                else -> HeaderWords(ready, stringResource(R.string.settings_ready_body), null, false, ready)
            }
        }
    }
}

/** What the button under the words says it does, or null for none: a trip can start. */
@Composable
private fun fixNameOf(state: ReadinessState, locationFix: Fix): String? = when {
    state is ReadinessState.Ready -> null
    state == ReadinessState.LocationOff -> stringResource(R.string.settings_turn_on_location)
    locationFix == Fix.OPEN_SETTINGS -> stringResource(R.string.preflight_open_settings)
    else -> stringResource(R.string.settings_allow_location)
}

private const val NAMED_AT_MOST = 2

/** "2 things to fix: Notifications, Battery", the names joined as the app's language joins a list. */
@Composable
private fun toFixText(names: List<String>): String {
    val list = ListFormatter.getInstance(LocalConfiguration.current.locales[0]).format(names)
    return pluralStringResource(R.plurals.settings_fix, names.size, names.size, list)
}

/** [readiness], except that a change the driver can see happen is shown a moment late, or at once with animations removed. */
@Composable
private fun shownReadiness(readiness: Readiness): Readiness {
    var shown by remember { mutableStateOf(readiness) }
    LaunchedEffect(readiness) {
        val sameLine = readiness.vehicleAt == shown.vehicleAt && readiness.stops == shown.stops
        val still = currentCoroutineContext()[MotionDurationScale]?.scaleFactor == 0f
        if (!sameLine && !still) delay(SHOW_CHANGE_AFTER_MILLIS)
        shown = readiness
    }
    return shown
}

/** Whether the app has less than [NarrowWidth] to draw in. */
@Composable
internal fun isNarrow(): Boolean = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.width.toDp() } < NarrowWidth

private enum class HeaderPart { TAPS, WORDS, STRIP, LINE, BAR }

/**
 * The top of Settings: whether this phone can report a trip, and the line that says why. It goes behind the status bar.
 * As the page scrolls it folds to its bar with the line as its lower edge: the words go up under the bar, and nothing is
 * resized, recomposed or laid out for a frame of it. A tap on it, folded or not, opens Permission check. While location
 * stops a trip, [onFix] is what its button does, by [locationFix].
 */
@Composable
internal fun ReadinessHeader(
    readiness: Readiness,
    locationFix: Fix,
    fold: HeaderFold,
    onBack: () -> Unit,
    onFix: () -> Unit,
    onPermissionCheck: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shown = shownReadiness(readiness)
    val words = headerWords(shown)
    val colors = MaterialTheme.colorScheme
    // A change of state fades. A change of theme doesn't: the whole screen is in the new one at once.
    val block = key(colors) {
        animateColorAsState(
            if (shown.canReport) colors.primaryContainer else AppTheme.problemColors.attentionContainer,
            AppMotion.slowEffectsSpec(),
            label = "header",
        )
    }
    val content = key(colors) {
        animateColorAsState(
            if (shown.canReport) colors.onPrimaryContainer else colors.onSurface,
            AppMotion.slowEffectsSpec(),
            label = "header content",
        )
    }
    val largeText = LocalDensity.current.fontScale >= LARGE_TEXT_FROM
    val somethingMissing = StopState.MISSING in shown.stops
    val iconsIn = animateFloatAsState(if (somethingMissing && !largeText) 1f else 0f, AppMotion.defaultSpatialSpec(), label = "icons")

    Layout(
        content = {
            // Under the rest, so it has the taps that nothing else there takes. The words say it to TalkBack.
            Box(Modifier.layoutId(HeaderPart.TAPS).clickable(onClick = onPermissionCheck).clearAndSetSemantics {})
            Words(words, fixNameOf(shown.state, locationFix), content, fold, onFix, onPermissionCheck, Modifier.layoutId(HeaderPart.WORDS))
            // The words go up under it, and its button with them. Where it is, it takes the taps they would.
            Box(
                Modifier
                    .layoutId(HeaderPart.STRIP)
                    .graphicsLayer { translationY = fold.offset }
                    .drawBehind { drawRect(block.value) }
                    .pointerInput(onPermissionCheck) { detectTapGestures { onPermissionCheck() } },
            )
            ReadinessLine(shown, content, { fold.fraction }, iconsIn, Modifier.layoutId(HeaderPart.LINE))
            Bar(words.folded, content, fold, onBack, Modifier.layoutId(HeaderPart.BAR).graphicsLayer { translationY = fold.offset })
        },
        modifier = modifier
            // Read out before the rows it is drawn over.
            .semantics {
                isTraversalGroup = true
                traversalIndex = -1f
            }
            .graphicsLayer { translationY = fold.pastFold }
            .drawBehind { drawRect(block.value) },
    ) { measurables, constraints ->
        fun part(part: HeaderPart) = measurables.first { it.layoutId == part }
        val width = constraints.maxWidth
        val loose = Constraints(minWidth = width, maxWidth = width)
        val bar = part(HeaderPart.BAR).measure(loose)
        val said = part(HeaderPart.WORDS).measure(loose)
        val hasRoom = LineGeometry(width.toDp(), shown.stops.size).hasRoomForIcons
        val zone = (LineZone + if (hasRoom) IconZone * iconsIn.value else 0.dp).roundToPx()
        val line = part(HeaderPart.LINE).measure(Constraints.fixed(width, zone.coerceAtLeast(0)))
        val strip = part(HeaderPart.STRIP).measure(Constraints.fixed(width, bar.height))

        val wordsTop = bar.height - WordsIntoBar.roundToPx()
        val height = wordsTop + said.height + line.height
        val taps = part(HeaderPart.TAPS).measure(Constraints.fixed(width, height))
        fold.folded = bar.height + FoldedEdge.roundToPx()
        fold.range = (height - fold.folded).coerceAtLeast(0)
        layout(width, height) {
            taps.place(0, 0)
            said.place(0, wordsTop)
            strip.place(0, 0)
            line.place(0, height - line.height)
            bar.place(0, 0)
        }
    }
}

@Composable
private fun Words(
    words: HeaderWords,
    fixName: String?,
    content: State<Color>,
    fold: HeaderFold,
    onFix: () -> Unit,
    onPermissionCheck: () -> Unit,
    modifier: Modifier,
) {
    val colors = MaterialTheme.colorScheme
    val amber = AppTheme.problemColors.iconOnSurface
    val titleStyle = if (isNarrow()) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.headlineLarge
    val bodyStyle = MaterialTheme.typography.bodyLarge
    val said = stringResource(R.string.preflight_row_state, words.title, words.sentence)
    val check = stringResource(R.string.permission_check_title)
    Column(
        modifier
            .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal))
            .padding(horizontal = 24.dp),
    ) {
        AnimatedContent(
            targetState = words,
            // One node says it, and says it again when it changes, rather than the two that fade past each other.
            modifier = Modifier.clearAndSetSemantics {
                contentDescription = said
                liveRegion = LiveRegionMode.Polite
                role = Role.Button
                onClick(label = check) {
                    onPermissionCheck()
                    true
                }
            },
            transitionSpec = {
                fadeIn(AppMotion.defaultEffectsSpec()) togetherWith fadeOut(AppMotion.defaultEffectsSpec()) using
                    SizeTransform(clip = false) { _, _ -> AppMotion.defaultSpatialSpec() }
            },
            label = "state",
        ) { state ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                BasicText(
                    text = state.title,
                    style = titleStyle,
                    color = { content.value },
                    modifier = Modifier.graphicsLayer { alpha = 1f - (fold.offset / TitleFade.toPx()).coerceAtMost(1f) },
                )
                Row(
                    modifier = Modifier.graphicsLayer { alpha = 1f - within(fold.fraction, 0.3f, 0.8f) },
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    if (state.icon != null) {
                        Icon(
                            painter = painterResource(state.icon),
                            contentDescription = null,
                            tint = if (state.iconIsProblem) amber else colors.onPrimaryContainer,
                            // On the first line of the sentence, however large.
                            modifier = Modifier.heightIn(min = with(LocalDensity.current) { bodyStyle.lineHeight.toDp() }).size(20.dp),
                        )
                    }
                    BasicText(text = state.sentence, style = bodyStyle, color = { content.value })
                }
            }
        }
        // The button keeps its name while it fades out.
        var buttonName by remember { mutableStateOf(fixName.orEmpty()) }
        if (fixName != null) buttonName = fixName
        AnimatedVisibility(
            visible = fixName != null,
            enter = fadeIn(AppMotion.defaultEffectsSpec()) + expandVertically(AppMotion.defaultSpatialSpec()),
            exit = fadeOut(AppMotion.defaultEffectsSpec()) + shrinkVertically(AppMotion.defaultSpatialSpec()),
        ) {
            Button(
                onClick = onFix,
                colors = ButtonDefaults.buttonColors(containerColor = colors.onSurface, contentColor = colors.surface),
                modifier = Modifier
                    .padding(top = 14.dp)
                    .heightIn(min = 48.dp)
                    .graphicsLayer { alpha = 1f - within(fold.fraction, 0.3f, 0.8f) },
            ) {
                Text(buttonName)
            }
        }
    }
}

@Composable
private fun Bar(foldedLine: String, content: State<Color>, fold: HeaderFold, onBack: () -> Unit, modifier: Modifier) {
    Row(
        modifier = modifier
            .statusBarsPadding()
            .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal))
            .fillMaxWidth()
            .heightIn(min = BarHeight)
            .padding(start = 4.dp, end = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onBack) {
            Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.setup_navigate_up), tint = content.value)
        }
        val shows = { within(fold.fraction, 0.6f, 1f) }
        val lineStyle = MaterialTheme.typography.bodyMedium
        val halfLine = with(LocalDensity.current) { lineStyle.lineHeight.toPx() } / 2
        Column(Modifier.weight(1f)) {
            BasicText(
                text = stringResource(R.string.settings_title),
                style = MaterialTheme.typography.titleLarge,
                color = { content.value },
                // Centred in the bar until the line under it comes in.
                modifier = Modifier.semantics { heading() }.graphicsLayer { translationY = halfLine * (1f - shows()) },
            )
            BasicText(
                text = foldedLine,
                style = lineStyle,
                color = { content.value },
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                // The header says it, folded or not.
                modifier = Modifier.clearAndSetSemantics {}.graphicsLayer { alpha = shows() },
            )
        }
    }
}
