// TooltipBox, RichTooltip, TooltipState and ModalBottomSheet are experimental at material3 1.4.0. This file is the only
// one that uses them.
@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.tips

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.foundation.MutatePriority
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RichTooltip
import androidx.compose.material3.SheetValue
import androidx.compose.material3.Text
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.TooltipScope
import androidx.compose.material3.TooltipState
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.layout
import androidx.compose.ui.layout.onPlaced
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.theme.AppMotion
import java.text.NumberFormat
import kotlin.math.roundToInt

/** How long after its anchor is drawn a tip comes out, so it lands on a screen that has settled. */
internal const val TIP_DELAY_MILLIS = 400L

/** How small a tip is as it starts to come out from its caret, and as it has gone back into it. */
private const val TIP_IN_SCALE = 0.8f

/** How small the check on "First trip done" is before it pops. */
private const val CHECK_IN_SCALE = 0.4f

private val TipShape = RoundedCornerShape(24.dp)

/** Clear of each edge of a narrow phone, where a tip of Material's full width would not fit. */
private val TipEdgeMargin = 16.dp

/**
 * Where a tip points, and whether it is out. Made once above the screen and handed down: its [anchor] goes on what the
 * tip points at, and [clearOf] on what it sits in and must not cover, such as the trip bar round the sent count. Each
 * is the same Modifier every time, so nothing under the tip is recomposed when it comes or goes. They only hand over
 * their places as they are placed; the tip reads them once, as it comes out, and stays there. Any tap that is not on
 * the tip closes it, so it never points at a place that has since scrolled away.
 */
@Stable
class ScreenTip internal constructor() {
    private var anchorCoordinates: LayoutCoordinates? = null
    private var clearCoordinates: LayoutCoordinates? = null
    private var hostCoordinates: LayoutCoordinates? = null

    /** Whether the anchor has been placed: the screen under the tip has been drawn. */
    internal var anchorPlaced by mutableStateOf(false)
        private set

    /** The anchor's place over the screen when the tip came out. Read only as the tip is laid out and drawn. */
    internal var anchorBounds by mutableStateOf(Rect.Zero)
        private set

    /** Whether the tip is out, or on its way. */
    var isOpen by mutableStateOf(false)
        private set

    /** On what the tip points at. */
    val anchor: Modifier = Modifier.onPlaced { coordinates ->
        anchorCoordinates = coordinates
        if (!anchorPlaced) anchorPlaced = true
    }

    /** On what the anchor sits in: the tip comes out above it, its caret still over the anchor. */
    val clearOf: Modifier = Modifier.onPlaced { clearCoordinates = it }

    internal val host: Modifier = Modifier.onPlaced { hostCoordinates = it }

    /** Out at the anchor, where it is now. False when the anchor is not on screen to point at. */
    internal fun open(): Boolean {
        val anchor = anchorCoordinates?.takeIf { it.isAttached } ?: return false
        val host = hostCoordinates?.takeIf { it.isAttached } ?: return false
        val onScreen = host.localBoundingBoxOf(anchor, clipBounds = true)
        if (onScreen.isEmpty) return false
        val clearTop = clearCoordinates?.takeIf { it.isAttached }?.let { host.localBoundingBoxOf(it, clipBounds = false).top }
        anchorBounds = if (clearTop != null && clearTop < onScreen.top) onScreen.copy(top = clearTop) else onScreen
        isOpen = true
        return true
    }

    internal fun close() {
        isOpen = false
    }

    /** The anchor's middle on the phone's screen, which the tip's caret points at. */
    internal fun anchorCentreOnScreen(): Offset? = hostCoordinates?.takeIf { it.isAttached }?.localToScreen(anchorBounds.center)
}

@Composable
fun rememberScreenTip(): ScreenTip = remember { ScreenTip() }

/**
 * The tip as [TooltipBox] sees it. Its transition stays at rest, so the box's own scale from the middle never runs: the
 * tip comes out from its caret on the app's springs instead. [isVisible] keeps it composed until it has gone back in.
 */
@Stable
private class TipBubbleState : TooltipState {
    override val transition = MutableTransitionState(true)
    var composed by mutableStateOf(false)
    override val isVisible: Boolean get() = composed
    override val isPersistent: Boolean get() = true

    override suspend fun show(mutatePriority: MutatePriority) {
        composed = true
    }

    override fun dismiss() {
        composed = false
    }

    override fun onDispose() = Unit
}

/**
 * Lays a screen's [tip] over it: Material's rich tooltip, in the green container, with a [title], the [text] and Got it.
 * It comes out [TIP_DELAY_MILLIS] after its anchor is first drawn, while [show] says it may, and goes when it no longer
 * may. Got it and a tap anywhere else close it, and the driver has [onSeen] it; one that goes as the screen does has
 * [onLeft]. [onShown] is asked first, and is false while another tip is out. It dims nothing and holds nothing up: a
 * tap elsewhere goes on to what was tapped as well.
 */
@Composable
internal fun TipHost(
    tip: ScreenTip,
    title: String,
    text: String,
    show: StateFlow<Boolean>,
    onShown: () -> Boolean,
    onLeft: () -> Unit,
    onSeen: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val bubble = remember { TipBubbleState() }
    val scale = remember { Animatable(TIP_IN_SCALE) }
    val alpha = remember { Animatable(0f) }
    val close = {
        tip.close()
        onSeen()
    }
    // Read here, in the screen's window: the tip's own is only as wide as the tip.
    val windowWidth = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.width.toDp() }
    val maxWidth = minOf(TooltipDefaults.richTooltipMaxWidth, windowWidth - TipEdgeMargin * 2)

    LaunchedEffect(tip, show) {
        snapshotFlow { tip.anchorPlaced }.first { it }
        delay(TIP_DELAY_MILLIS)
        show.collect { wanted ->
            when {
                wanted && !tip.isOpen -> if (onShown() && !tip.open()) onLeft()
                !wanted && tip.isOpen -> tip.close()
            }
        }
    }
    // In and out on springs, so a tip closed as it comes out turns back from where it is.
    LaunchedEffect(tip) {
        snapshotFlow { tip.isOpen }.collectLatest { open ->
            if (open) bubble.composed = true
            coroutineScope {
                launch { scale.animateTo(if (open) 1f else TIP_IN_SCALE, AppMotion.defaultSpatialSpec()) }
                alpha.animateTo(if (open) 1f else 0f, AppMotion.defaultEffectsSpec())
            }
            if (!open) bubble.composed = false
        }
    }
    DisposableEffect(tip) {
        onDispose { if (tip.isOpen) onLeft() }
    }

    Box(modifier.fillMaxSize().then(tip.host)) {
        // An empty box over the anchor, for TooltipBox to place the tip and its caret against.
        Box(Modifier.atAnchorOf(tip)) {
            TooltipBox(
                positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
                tooltip = { TipBubble(tip, title, text, maxWidth, scale = { scale.value }, alpha = { alpha.value }, onGotIt = close) },
                state = bubble,
                onDismissRequest = close,
                focusable = false,
                enableUserInput = false,
                hasAction = true,
                modifier = Modifier.fillMaxSize(),
            ) {
                Spacer(Modifier.fillMaxSize())
            }
        }
    }
}

/** At the anchor's place and size, read as this is placed and measured: a new place lays it out again, nothing more. */
private fun Modifier.atAnchorOf(tip: ScreenTip): Modifier =
    offset { IntOffset(tip.anchorBounds.left.roundToInt(), tip.anchorBounds.top.roundToInt()) }
        .layout { measurable, _ ->
            val width = tip.anchorBounds.width.roundToInt()
            val height = tip.anchorBounds.height.roundToInt()
            val placeable = measurable.measure(Constraints.fixed(width, height))
            layout(width, height) { placeable.place(0, 0) }
        }

/** Where on itself the tip grows from: the tip of its caret, which points at the anchor's middle. */
private class CaretPoint {
    var bubble: LayoutCoordinates? = null

    fun origin(tip: ScreenTip): TransformOrigin {
        val bubble = bubble?.takeIf { it.isAttached } ?: return TransformOrigin.Center
        val anchor = tip.anchorCentreOnScreen() ?: return TransformOrigin.Center
        val topLeft = bubble.localToScreen(Offset.Zero)
        val width = bubble.size.width.toFloat()
        val height = bubble.size.height.toFloat()
        if (width <= 0f || height <= 0f) return TransformOrigin.Center
        val x = ((anchor.x - topLeft.x) / width).coerceIn(0f, 1f)
        // Above the anchor its caret is on its bottom edge; pushed below it by the top of the screen, on its top one.
        val y = if (anchor.y > topLeft.y + height / 2) 1f else 0f
        return TransformOrigin(x, y)
    }
}

@Composable
private fun TooltipScope.TipBubble(
    tip: ScreenTip,
    title: String,
    text: String,
    maxWidth: Dp,
    scale: () -> Float,
    alpha: () -> Float,
    onGotIt: () -> Unit,
) {
    val colors = MaterialTheme.colorScheme
    val caret = remember { CaretPoint() }
    RichTooltip(
        modifier = Modifier
            .onPlaced { caret.bubble = it }
            .graphicsLayer {
                scaleX = scale()
                scaleY = scale()
                this.alpha = alpha()
                transformOrigin = caret.origin(tip)
            },
        title = { Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() }) },
        action = {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) {
                Button(
                    onClick = onGotIt,
                    colors = ButtonDefaults.buttonColors(containerColor = colors.onPrimaryContainer, contentColor = colors.primaryContainer),
                    contentPadding = PaddingValues(horizontal = 18.dp),
                ) {
                    Text(stringResource(R.string.tip_got_it), style = MaterialTheme.typography.labelLarge)
                }
            }
        },
        caretShape = TooltipDefaults.caretShape(),
        maxWidth = maxWidth,
        shape = TipShape,
        colors = TooltipDefaults.richTooltipColors(
            containerColor = colors.primaryContainer,
            contentColor = colors.onPrimaryContainer,
            titleContentColor = colors.onPrimaryContainer,
            actionContentColor = colors.onPrimaryContainer,
        ),
    ) {
        Text(text)
    }
}

/**
 * "First trip done", as a modal sheet over the step after the trip: the trip's [sent] count large, and OK. Its check
 * pops once the sheet is up, on the expressive spring the welcome's checks land on. Swiping it down, Back and a tap
 * above it close it as OK does.
 */
@Composable
internal fun FirstTripDoneSheet(sent: Int, onClose: () -> Unit) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    // Once: a sheet put back after a rotation shows its check where it landed.
    var popped by rememberSaveable { mutableStateOf(false) }
    val checkScale = remember { Animatable(if (popped) 1f else CHECK_IN_SCALE) }
    val checkAlpha = remember { Animatable(if (popped) 1f else 0f) }
    LaunchedEffect(sheetState) {
        snapshotFlow { sheetState.currentValue == SheetValue.Expanded }.first { it }
        coroutineScope {
            launch { checkScale.animateTo(1f, AppMotion.expressiveFastSpatialSpec()) }
            checkAlpha.animateTo(1f, AppMotion.defaultEffectsSpec())
        }
        popped = true
    }
    ModalBottomSheet(onDismissRequest = onClose, sheetState = sheetState) {
        FirstTripDoneContent(
            sent = sent,
            checkScale = { checkScale.value },
            checkAlpha = { checkAlpha.value },
            onOk = {
                scope.launch { sheetState.hide() }.invokeOnCompletion { if (!sheetState.isVisible) onClose() }
            },
        )
    }
}

@Composable
internal fun FirstTripDoneContent(sent: Int, checkScale: () -> Float, checkAlpha: () -> Float, onOk: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val locale = LocalConfiguration.current.locales[0]
    Column(Modifier.fillMaxWidth().padding(start = 24.dp, end = 24.dp, bottom = 24.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            Box(
                modifier = Modifier
                    .size(52.dp)
                    .graphicsLayer {
                        scaleX = checkScale()
                        scaleY = checkScale()
                        alpha = checkAlpha()
                    }
                    .background(colors.primary, RoundedCornerShape(18.dp)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(painterResource(R.drawable.ic_check), contentDescription = null, tint = colors.onPrimary, modifier = Modifier.size(30.dp))
            }
            Text(
                text = stringResource(R.string.first_trip_done_title),
                style = MaterialTheme.typography.headlineMedium,
                modifier = Modifier.semantics { heading() },
            )
        }
        // Read as one: "142 reports sent".
        Row(
            modifier = Modifier
                .padding(top = 20.dp)
                .fillMaxWidth()
                .background(colors.surfaceContainerHigh, RoundedCornerShape(24.dp))
                .padding(horizontal = 20.dp, vertical = 16.dp)
                .semantics(mergeDescendants = true) {},
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Text(
                text = remember(sent, locale) { NumberFormat.getIntegerInstance(locale).format(sent) },
                style = MaterialTheme.typography.displayMedium.copy(fontWeight = FontWeight.Medium),
                color = colors.primary,
                modifier = Modifier.alignByBaseline(),
            )
            Text(
                text = pluralStringResource(R.plurals.first_trip_done_reports, sent),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.onSurfaceVariant,
                modifier = Modifier.alignByBaseline(),
            )
        }
        Text(
            text = stringResource(R.string.first_trip_done_text),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurfaceVariant,
            modifier = Modifier.padding(top = 16.dp),
        )
        Button(onClick = onOk, modifier = Modifier.padding(top = 20.dp).fillMaxWidth().heightIn(min = 56.dp)) {
            Text(stringResource(R.string.first_trip_done_ok), style = MaterialTheme.typography.titleMedium)
        }
    }
}
