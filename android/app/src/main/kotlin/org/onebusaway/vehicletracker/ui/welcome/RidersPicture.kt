package org.onebusaway.vehicletracker.ui.welcome

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.map.MARKER_INK
import org.onebusaway.vehicletracker.ui.map.NEXT_STOP_FILL
import org.onebusaway.vehicletracker.ui.map.STOP_FILL
import org.onebusaway.vehicletracker.ui.map.STOP_RING
import org.onebusaway.vehicletracker.ui.map.StreetColors
import org.onebusaway.vehicletracker.ui.map.casingColor
import org.onebusaway.vehicletracker.ui.map.streetColors
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import kotlin.math.max
import kotlin.math.min

/** The streets behind the picture: the only colours in the welcome that are not the theme's or the Tracking map's. */
/** The picture's colours, read from the theme once in composition so that drawing a frame reads none. */
private class RidersColors(
    val streets: StreetColors,
    val route: Color,
    /** The route's casing, and the edge that keeps a vehicle marker clear of the map: white by day, ink by night. */
    val casing: Color,
    val ringWaiting: Color,
    val ringLive: Color,
    val chip: Color,
    val chipLit: Color,
    val rider: Color,
    val riderLit: Color,
    val phone: Color,
    val phoneEdge: Color,
    val ink: Color,
    val quietInk: Color,
    val shadow: Color,
)

private val StopFill = Color(STOP_FILL)
private val StopRing = Color(STOP_RING)
private val NextStopFill = Color(NEXT_STOP_FILL)
private val MarkerInk = Color(MARKER_INK)

private val CardShape = RoundedCornerShape(28.dp)

// Where things sit, as fractions of the card, so the picture stretches with the screen.
private const val ROUTE_X = 0.36f
private const val NEXT_STOP_Y = 0.24f
private const val SMALL_STOP_Y = 0.68f
private const val VEHICLE_STARTS_Y = 0.82f
private const val VEHICLE_ENDS_Y = 0.50f

private val RiderSize = 24.dp
private val RidersChipWidth = 94.dp
private val RidersChipHeight = 40.dp
private val RidersChipFromRoute = 22.dp

// A narrow card moves the route over rather than push the riders' chip off its edge.
private val RidersChipFromEdge = 8.dp
private val RiderLift = 5.dp
private val VehicleSize = 52.dp
private val Inset = 16.dp
private const val PHONE_WIDTH_OF_HEIGHT = 0.58f
private const val PHONE_MOST_OF_CARD = 0.55f

/** The rider's phone: 190 dp tall, less on a small phone and at the largest text, where the words need the room. */
private val PhoneHeight = 190.dp
private val PhoneHeightLargeText = 150.dp
private val PhoneHeightCompact = 132.dp
private const val LARGE_TEXT_FROM = 1.5f

/**
 * Screen 1's picture, in one canvas: a vehicle goes live and glides up its route to the riders at the next stop, who
 * see it on a phone. The streets, route and stops are worked out once for a size; the parts that move are drawn from
 * [clock] and from nothing else, so a frame of the picture is a redraw.
 */
@Composable
internal fun RidersPicture(clock: PictureClock, compact: Boolean, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    val status = AppTheme.statusColors
    // As the Tracking map decides it, and as the vehicle marker's own day and night drawables do.
    val dark = isSystemInDarkTheme()
    val colors = remember(scheme, status, dark) {
        RidersColors(
            streets = streetColors(dark),
            route = scheme.primary,
            casing = Color(casingColor(scheme.primary.toArgb(), dark)),
            ringWaiting = status.neutral.text,
            ringLive = status.onTime.text,
            chip = if (dark) scheme.surfaceContainer else scheme.surface,
            chipLit = scheme.primaryContainer,
            rider = scheme.onSurfaceVariant,
            riderLit = scheme.onPrimaryContainer,
            phone = scheme.surface,
            phoneEdge = scheme.outlineVariant,
            ink = scheme.onSurface,
            quietInk = scheme.onSurfaceVariant,
            shadow = Color.Black.copy(alpha = if (dark) 0.45f else 0.16f),
        )
    }
    val vehicle = painterResource(R.drawable.ic_map_vehicle_puck)
    val standing = painterResource(R.drawable.ic_person)
    val waving = painterResource(R.drawable.ic_emoji_people)
    val cloud = painterResource(R.drawable.ic_cloud_done)
    val measurer = rememberTextMeasurer()
    val sentStyle = MaterialTheme.typography.labelLarge
    val sentWords = List(RidersTimeline.RIDERS) { pluralStringResource(R.plurals.tracking_bar_sent, it + 1, it + 1) }
    val phoneHeight = when {
        compact -> PhoneHeightCompact
        LocalDensity.current.fontScale >= LARGE_TEXT_FROM -> PhoneHeightLargeText
        else -> PhoneHeight
    }
    val words = stringResource(R.string.welcome_riders_picture)

    Spacer(
        modifier
            .semantics {
                contentDescription = words
                role = Role.Image
            }
            .clip(CardShape)
            .drawWithCache {
                val width = size.width
                val height = size.height
                val routeX = max(width * ROUTE_X, (RidersChipFromEdge + RidersChipWidth + RidersChipFromRoute).toPx())
                val nextStop = Offset(routeX, height * NEXT_STOP_Y)
                val smallStop = Offset(routeX, height * SMALL_STOP_Y)
                val vehicleStartsY = height * VEHICLE_STARTS_Y
                val vehicleTravel = height * (VEHICLE_ENDS_Y - VEHICLE_STARTS_Y)

                val riderFilter = ColorFilter.tint(colors.rider)
                val riderLitFilter = ColorFilter.tint(colors.riderLit)
                val quietFilter = ColorFilter.tint(colors.quietInk)
                val thinRing = Stroke(2.dp.toPx())
                val stopRing = Stroke(3.dp.toPx())
                val phoneHigh = min(phoneHeight.toPx(), height * PHONE_MOST_OF_CARD)
                val phone = PhoneFrame(
                    left = width - Inset.toPx() - phoneHigh * PHONE_WIDTH_OF_HEIGHT,
                    top = Inset.toPx(),
                    width = phoneHigh * PHONE_WIDTH_OF_HEIGHT,
                    height = phoneHigh,
                )
                val sent = sentWords.map { measurer.measure(it, sentStyle) }
                val sentWidth = sent.maxOf { it.size.width }.toFloat()
                val sentHeight = sent.maxOf { it.size.height }.toFloat()

                onDrawBehind {
                    val millis = clock.millis
                    val glide = RidersTimeline.glide(millis)
                    val live = RidersTimeline.live(millis)

                    streets(colors.streets, routeX)
                    drawRect(colors.casing, Offset(routeX - 6.dp.toPx(), 0f), Size(12.dp.toPx(), height))
                    drawRect(colors.route, Offset(routeX - 4.dp.toPx(), 0f), Size(8.dp.toPx(), height))
                    drawCircle(StopFill, 6.dp.toPx(), smallStop)
                    drawCircle(StopRing, 6.dp.toPx(), smallStop, style = thinRing)
                    drawCircle(StopFill, 15.5.dp.toPx(), nextStop)
                    drawCircle(NextStopFill, 11.5.dp.toPx(), nextStop)
                    drawCircle(MarkerInk, 11.5.dp.toPx(), nextStop, style = stopRing)

                    riders(colors, millis, nextStop, standing, waving, riderFilter, riderLitFilter)
                    riderPhone(colors, phone, glide, RidersTimeline.onPhone(millis), thinRing, standing, riderLitFilter)

                    val vehicleAt = Offset(routeX, vehicleStartsY + vehicleTravel * glide)
                    for (ring in 0 until RidersTimeline.RINGS) {
                        val flown = RidersTimeline.ring(ring, millis)
                        if (flown >= 0f) {
                            scale(0.55f + 2.15f * flown, vehicleAt) {
                                drawCircle(colors.ringLive, VehicleSize.toPx() / 2, vehicleAt, 0.55f * (1f - flown), thinRing)
                            }
                        }
                    }
                    val rest = RidersTimeline.rest(millis)
                    if (rest > 0f) {
                        scale(2.1f, vehicleAt) { drawCircle(colors.ringLive, VehicleSize.toPx() / 2, vehicleAt, 0.28f * rest, thinRing) }
                        scale(1.5f, vehicleAt) { drawCircle(colors.ringLive, VehicleSize.toPx() / 2, vehicleAt, 0.45f * rest, thinRing) }
                    }
                    drawCircle(colors.shadow, VehicleSize.toPx() / 2, vehicleAt + Offset(0f, 1.dp.toPx()))
                    drawCircle(colors.ringWaiting, 25.dp.toPx(), vehicleAt)
                    drawCircle(colors.ringLive, 25.dp.toPx(), vehicleAt, alpha = live)
                    translate(vehicleAt.x - VehicleSize.toPx() / 2, vehicleAt.y - VehicleSize.toPx() / 2) {
                        with(vehicle) { draw(Size(VehicleSize.toPx(), VehicleSize.toPx())) }
                    }

                    val count = RidersTimeline.sent(millis)
                    if (count > 0) {
                        sentChip(colors, sent[count - 1], sentWidth, sentHeight, RidersTimeline.sinceSentChip(millis), cloud, quietFilter)
                    }
                }
            },
    )
}

/** Where the rider's phone sits, and the screen inside its body. */
private class PhoneFrame(val left: Float, val top: Float, val width: Float, val height: Float)

private fun DrawScope.streets(streets: StreetColors, routeX: Float) {
    val width = size.width
    val height = size.height
    drawRect(streets.land)
    drawRect(streets.road, Offset(0f, height * 0.40f), Size(width, 12.dp.toPx()))
    drawRect(streets.road, Offset(0f, height * SMALL_STOP_Y), Size(width, 14.dp.toPx()))
    drawRect(streets.road, Offset(routeX - 10.dp.toPx(), 0f), Size(20.dp.toPx(), height))
    drawRect(streets.road, Offset(width * 0.86f - 6.dp.toPx(), 0f), Size(12.dp.toPx(), height))
    drawRect(streets.road, Offset(width * 0.08f, height * 0.40f), Size(12.dp.toPx(), height * 0.60f))
    drawRoundRect(streets.park, Offset(width * 0.56f, height * 0.50f), Size(width * 0.28f, height * 0.14f), CornerRadius(4.dp.toPx()))
}

/** The three riders in their chip, left of the next stop. Each stands plain until its turn to wave. */
private fun DrawScope.riders(
    colors: RidersColors,
    millis: Float,
    nextStop: Offset,
    standing: Painter,
    waving: Painter,
    plain: ColorFilter,
    lit: ColorFilter,
) {
    val chipSize = Size(RidersChipWidth.toPx(), RidersChipHeight.toPx())
    val chipAt = Offset(nextStop.x - RidersChipFromRoute.toPx() - chipSize.width, nextStop.y - chipSize.height / 2)
    val corner = CornerRadius(chipSize.height / 2)
    drawRoundRect(colors.shadow, chipAt + Offset(0f, 1.dp.toPx()), chipSize, corner)
    drawRoundRect(colors.chip, chipAt, chipSize, corner)
    drawRoundRect(colors.chipLit, chipAt, chipSize, corner, alpha = RidersTimeline.chipLit(millis))

    val rider = Size(RiderSize.toPx(), RiderSize.toPx())
    val top = chipAt.y + (chipSize.height - rider.height) / 2
    for (index in 0 until RidersTimeline.RIDERS) {
        val left = chipAt.x + 9.dp.toPx() + index * 26.dp.toPx()
        val since = RidersTimeline.sinceWave(index, millis)
        if (since < 0f) {
            translate(left, top) { with(standing) { draw(rider, colorFilter = plain) } }
        } else {
            val grown = 0.6f + 0.4f * WelcomeMotion.Pop.settle(since)
            translate(left, top - RiderLift.toPx() * WelcomeMotion.Pop.hop(since)) {
                scale(grown, Offset(rider.width / 2, rider.height / 2)) { with(waving) { draw(rider, colorFilter = lit) } }
            }
        }
    }
}

/** A rider's phone in the corner, showing the same route and stop, and the vehicle once it reports. */
private fun DrawScope.riderPhone(
    colors: RidersColors,
    phone: PhoneFrame,
    glide: Float,
    vehicleShows: Float,
    edge: Stroke,
    person: Painter,
    personFilter: ColorFilter,
) {
    val body = Size(phone.width, phone.height)
    val bodyAt = Offset(phone.left, phone.top)
    val bodyCorner = CornerRadius(20.dp.toPx())
    drawRoundRect(colors.shadow, bodyAt + Offset(0f, 1.dp.toPx()), body, bodyCorner)
    drawRoundRect(colors.phone, bodyAt, body, bodyCorner)
    drawRoundRect(colors.phoneEdge, bodyAt, body, bodyCorner, style = edge)

    val bezel = 6.dp.toPx()
    val screenAt = Offset(phone.left + bezel, phone.top + bezel)
    val screen = Size(phone.width - 2 * bezel, phone.height - 2 * bezel)
    val middle = screenAt.x + screen.width / 2
    drawRoundRect(colors.streets.land, screenAt, screen, CornerRadius(13.dp.toPx()))
    drawRect(colors.streets.road, Offset(screenAt.x, screenAt.y + screen.height * 0.62f), Size(screen.width, 8.dp.toPx()))
    drawRect(colors.streets.road, Offset(middle - 7.dp.toPx(), screenAt.y), Size(14.dp.toPx(), screen.height))
    drawRect(colors.route, Offset(middle - 2.5.dp.toPx(), screenAt.y), Size(5.dp.toPx(), screen.height))
    val stop = Offset(middle, screenAt.y + screen.height * NEXT_STOP_Y)
    drawCircle(NextStopFill, 5.dp.toPx(), stop)
    drawCircle(MarkerInk, 5.dp.toPx(), stop, style = edge)

    val vehicleY = VEHICLE_STARTS_Y + (VEHICLE_ENDS_Y - VEHICLE_STARTS_Y) * glide
    val vehicle = Offset(middle, screenAt.y + screen.height * vehicleY)
    drawCircle(colors.casing, 11.5.dp.toPx(), vehicle, alpha = vehicleShows)
    drawCircle(colors.ringLive, 10.dp.toPx(), vehicle, alpha = vehicleShows)
    drawCircle(colors.ink, 6.dp.toPx(), vehicle, alpha = vehicleShows)

    // Whose phone it is: a rider, on a badge over its corner.
    val badge = Offset(phone.left + 5.dp.toPx(), phone.top + 5.dp.toPx())
    drawCircle(colors.shadow, 14.dp.toPx(), badge + Offset(0f, 1.dp.toPx()))
    drawCircle(colors.chipLit, 14.dp.toPx(), badge)
    translate(badge.x - 9.dp.toPx(), badge.y - 9.dp.toPx()) {
        with(person) { draw(Size(18.dp.toPx(), 18.dp.toPx()), colorFilter = personFilter) }
    }
}

/** The trip bar's cloud and count, in the bottom corner: it rises in with "1 sent" and counts on. */
private fun DrawScope.sentChip(
    colors: RidersColors,
    words: TextLayoutResult,
    wordsWidth: Float,
    wordsHeight: Float,
    sinceShown: Float,
    cloud: Painter,
    cloudFilter: ColorFilter,
) {
    val shown = WelcomeMotion.Fade.settle(sinceShown)
    val chipHeight = max(36.dp.toPx(), wordsHeight + 8.dp.toPx())
    val chip = Size(10.dp.toPx() + 18.dp.toPx() + 6.dp.toPx() + wordsWidth + 14.dp.toPx(), chipHeight)
    val risen = (1f - WelcomeMotion.Rise.settle(sinceShown)) * 10.dp.toPx()
    val chipAt = Offset(Inset.toPx(), size.height - Inset.toPx() - chipHeight + risen)
    val corner = CornerRadius(18.dp.toPx())
    drawRoundRect(colors.shadow, chipAt + Offset(0f, 1.dp.toPx()), chip, corner, alpha = shown)
    drawRoundRect(colors.chip, chipAt, chip, corner, alpha = shown)
    translate(chipAt.x + 10.dp.toPx(), chipAt.y + (chipHeight - 18.dp.toPx()) / 2) {
        with(cloud) { draw(Size(18.dp.toPx(), 18.dp.toPx()), alpha = shown, colorFilter = cloudFilter) }
    }
    drawText(
        textLayoutResult = words,
        color = colors.ink,
        topLeft = Offset(chipAt.x + 34.dp.toPx(), chipAt.y + (chipHeight - words.size.height) / 2),
        alpha = shown,
    )
}
