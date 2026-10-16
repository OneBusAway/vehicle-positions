package org.onebusaway.vehicletracker.ui.map

import android.annotation.SuppressLint
import android.app.ActivityManager
import android.content.ComponentCallbacks2
import android.content.Context
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import android.location.Location
import android.os.SystemClock
import android.text.TextPaint
import android.text.TextUtils
import android.util.TypedValue
import android.view.Gravity
import androidx.annotation.StringRes
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.delay
import org.maplibre.android.MapLibre
import org.maplibre.android.camera.CameraUpdateFactory
import org.maplibre.android.geometry.LatLng
import org.maplibre.android.geometry.LatLngBounds
import org.maplibre.android.location.LocationComponentActivationOptions
import org.maplibre.android.location.LocationComponentOptions
import org.maplibre.android.location.OnCameraTrackingChangedListener
import org.maplibre.android.location.OnLocationCameraTransitionListener
import org.maplibre.android.location.modes.CameraMode
import org.maplibre.android.location.modes.RenderMode
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapView
import org.maplibre.android.maps.Style
import org.maplibre.android.style.expressions.Expression
import org.maplibre.android.style.layers.CircleLayer
import org.maplibre.android.style.layers.Layer
import org.maplibre.android.style.layers.LineLayer
import org.maplibre.android.style.layers.Property
import org.maplibre.android.style.layers.PropertyFactory.circleColor
import org.maplibre.android.style.layers.PropertyFactory.circleRadius
import org.maplibre.android.style.layers.PropertyFactory.circleStrokeColor
import org.maplibre.android.style.layers.PropertyFactory.circleStrokeWidth
import org.maplibre.android.style.layers.PropertyFactory.iconAllowOverlap
import org.maplibre.android.style.layers.PropertyFactory.iconAnchor
import org.maplibre.android.style.layers.PropertyFactory.iconIgnorePlacement
import org.maplibre.android.style.layers.PropertyFactory.iconImage
import org.maplibre.android.style.layers.PropertyFactory.lineCap
import org.maplibre.android.style.layers.PropertyFactory.lineColor
import org.maplibre.android.style.layers.PropertyFactory.lineDasharray
import org.maplibre.android.style.layers.PropertyFactory.lineJoin
import org.maplibre.android.style.layers.PropertyFactory.lineWidth
import org.maplibre.android.style.layers.PropertyFactory.visibility
import org.maplibre.android.style.layers.SymbolLayer
import org.maplibre.android.style.sources.GeoJsonSource
import org.maplibre.geojson.FeatureCollection
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.map.MapFailure
import org.onebusaway.vehicletracker.data.map.MapFileState
import org.onebusaway.vehicletracker.data.map.downloadPercent
import org.onebusaway.vehicletracker.engine.Adherence
import org.onebusaway.vehicletracker.engine.ShapeGeometry
import org.onebusaway.vehicletracker.engine.TripGeometry
import java.io.File
import kotlin.math.roundToInt

// The line's widths are iOS's, from RouteMapViewController and MapGlyphs (spec §6.3); the stops
// and the off-route line are the design's. MapLibre measures in density-independent pixels, as
// iOS does in points.
private const val CASING_WIDTH = 10f
private const val ROUTE_WIDTH = 6f

/**
 * The off-route line's dots: 2 long and 6 apart at 2.5 wide. MapLibre counts dashes in line
 * widths, and round caps make them dots.
 */
private const val OFF_ROUTE_LINK_WIDTH = 2.5f
private val OFF_ROUTE_DASHES = arrayOf(0.8f, 2.4f)
private const val STOP_RING_WIDTH = 2f
private const val MARKED_STOP_RING_WIDTH = 3f

/**
 * Across, ring included: a stop is 12, the last stop 17 and the next 23, in a white halo 25
 * across. Where an off-route vehicle was matched looks like the last stop. MapLibre adds the ring
 * outside the radius.
 */
private const val STOP_RADIUS = 6f - STOP_RING_WIDTH
private const val LAST_STOP_RADIUS = 8.5f - MARKED_STOP_RING_WIDTH
private const val NEXT_STOP_RADIUS = 11.5f - MARKED_STOP_RING_WIDTH
private const val NEXT_STOP_HALO_RADIUS = 12.5f
private const val FIT_PADDING_DP = 40
private const val ATTRIBUTION_MARGIN_DP = 16f
private const val LABEL_TEXT_SP = 13f
private const val LABEL_GAP_DP = 6f
private const val LABEL_HALO_DP = 1.5f
private const val LABEL_MAX_WIDTH_DP = 200f

private const val ROUTE_SOURCE = "route"
private const val STOPS_SOURCE = "stops"
private const val NEXT_STOP_SOURCE = "next-stop"
private const val SNAPPED_SOURCE = "snapped"
private const val OFF_ROUTE_SOURCE = "off-route"
private const val CASING_LAYER = "route-casing"
private const val ROUTE_LAYER = "route-line"
private const val STOPS_LAYER = "stops"
private const val NEXT_STOP_HALO_LAYER = "next-stop-halo"
private const val NEXT_STOP_LABEL_LAYER = "next-stop-label"
private const val OFF_ROUTE_LAYER = "off-route-link"
private const val SNAPPED_LAYER = "snapped"
private const val NEXT_STOP_LABEL_IMAGE = "next-stop-label"

private const val FOLLOW_ANIMATION_MS = 750L

/** How long the map stays where the driver dragged it before it goes back to following. */
private const val RETURN_TO_FOLLOWING_MS = 10_000L

/** `ConfigurationInfo.reqGlEsVersion` for this phone, the argument [supportsMap] takes. */
fun Context.glEsVersion(): Int = getSystemService(ActivityManager::class.java).deviceConfigurationInfo.reqGlEsVersion

/**
 * The trip on a map: the shape in the route colour over a casing, the stops with the next one
 * enlarged and named, and the vehicle pointed along its course and ringed in [vehicleRing], ARGB,
 * the status colour of how it is keeping to the schedule. Off the route, a dotted line joins it to
 * where it was matched. The camera follows heading-up with the vehicle a third of the way up the
 * part of the map that [contentPadding], the screen's own cards and bars over it, leaves
 * uncovered. Under it is the agency's street map once the file is on the phone, and a plain
 * background until then, with a note while the first copy downloads. The note and Re-center sit
 * at the bottom left, inside [controlsPadding].
 */
@Composable
fun RouteMap(
    geometry: TripGeometry,
    adherence: Adherence?,
    mapFile: MapFileState,
    vehicleRing: Int,
    contentPadding: PaddingValues,
    controlsPadding: PaddingValues,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val dark = isSystemInDarkTheme()
    val labelColor = MaterialTheme.colorScheme.onSurface.toArgb()
    val haloColor = MaterialTheme.colorScheme.surface.toArgb()
    // One map for as long as the screen is up. A theme change recreates the activity, so the
    // colours read here hold for the map's whole life.
    val controller = remember { RouteMapController(context, dark, labelColor, haloColor) }

    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, controller) {
        val observer = LifecycleEventObserver { _, event -> controller.onLifecycleEvent(event) }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    LaunchedEffect(geometry, adherence) { controller.show(geometry, adherence) }
    LaunchedEffect(vehicleRing) { controller.ringVehicle(vehicleRing) }
    val density = LocalDensity.current
    val coveredTop = with(density) { contentPadding.calculateTopPadding().roundToPx() }
    val coveredBottom = with(density) { contentPadding.calculateBottomPadding().roundToPx() }
    LaunchedEffect(coveredTop, coveredBottom) { controller.cover(coveredTop, coveredBottom) }
    val streetFile = (mapFile as? MapFileState.Ready)?.file
    LaunchedEffect(streetFile) { controller.useStreets(streetFile) }
    // Restarted by every drag, so the map goes back only once the driver has left it alone.
    LaunchedEffect(controller.following, controller.drags) {
        if (!controller.following) {
            delay(RETURN_TO_FOLLOWING_MS)
            controller.follow()
        }
    }

    Box(modifier) {
        AndroidView(
            factory = { controller.mapView },
            modifier = Modifier.fillMaxSize(),
            onRelease = { controller.destroy() },
        )
        // Clear of the attribution button at the bottom right.
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .padding(controlsPadding)
                .padding(start = 12.dp, end = 64.dp, bottom = 12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            MapFileNote(mapFile)
            if (!controller.following) RecenterChip(onClick = controller::follow)
        }
    }
}

private val NoteShape = RoundedCornerShape(22.dp)

@Composable
private fun RecenterChip(onClick: () -> Unit) {
    Surface(
        onClick = onClick,
        shape = CircleShape,
        color = MaterialTheme.colorScheme.surfaceContainerLow,
        shadowElevation = 1.dp,
    ) {
        Row(
            modifier = Modifier.heightIn(min = 48.dp).padding(start = 14.dp, end = 20.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(painterResource(R.drawable.ic_navigation), contentDescription = null, modifier = Modifier.size(22.dp))
            Text(stringResource(R.string.tracking_map_recenter), style = MaterialTheme.typography.titleSmall)
        }
    }
}

/** A note over the map while it has no streets to show yet. Nothing once it has, or when the agency has no map. */
@Composable
private fun MapFileNote(state: MapFileState) {
    val icon: Int
    val text: String
    when (state) {
        is MapFileState.Downloading -> {
            icon = R.drawable.ic_download
            text = stringResource(R.string.tracking_map_downloading, downloadPercent(state.bytes, state.total))
        }
        is MapFileState.Failed -> {
            icon = if (state.reason == MapFailure.NO_SPACE) R.drawable.ic_sd_card_alert else R.drawable.ic_sync_problem
            text = stringResource(mapFailureNote(state.reason))
        }
        MapFileState.None, is MapFileState.Ready -> return
    }
    Surface(shape = NoteShape, color = MaterialTheme.colorScheme.surfaceContainerLow, shadowElevation = 1.dp) {
        Column(
            modifier = Modifier.padding(start = 14.dp, end = 18.dp, top = 11.dp, bottom = 11.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    painter = painterResource(icon),
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(20.dp),
                )
                Text(text, style = MaterialTheme.typography.titleSmall)
            }
            if (state is MapFileState.Downloading) {
                LinearProgressIndicator(
                    progress = { downloadPercent(state.bytes, state.total) / 100f },
                    modifier = Modifier.fillMaxWidth(),
                    color = MaterialTheme.colorScheme.onSurface,
                    trackColor = MaterialTheme.colorScheme.surfaceContainerHighest,
                )
            }
        }
    }
}

/**
 * The note for a first copy that could not be fetched. Only a failure the app retries on a timer
 * says "will retry"; an unexpected one waits for the app to come back to the front, and the rest
 * need the driver or the agency to change something.
 */
@StringRes
internal fun mapFailureNote(reason: MapFailure): Int = when (reason) {
    MapFailure.NETWORK -> R.string.tracking_map_retrying
    MapFailure.UNEXPECTED -> R.string.tracking_map_retry_on_open
    MapFailure.NO_SPACE -> R.string.tracking_map_no_space
    MapFailure.INVALID_FILE -> R.string.tracking_map_unusable
    MapFailure.CLEARTEXT_BLOCKED -> R.string.tracking_map_needs_https
}

/**
 * Owns the [MapView] and pushes the trip and each fix into it: a port of iOS's
 * `RouteMapViewController`. It touches the map only for what changed since the fix before, which
 * is what keeps a fix a second cheap. Everything here runs on the main thread.
 */
private class RouteMapController(
    private val context: Context,
    private val dark: Boolean,
    private val labelColor: Int,
    private val haloColor: Int,
) {
    val mapView: MapView

    /** False once the driver has dragged the map away from the vehicle. */
    var following by mutableStateOf(true)
        private set

    /** Counts the driver's drags, so the return to following can wait for the last of them. */
    var drags by mutableIntStateOf(0)
        private set

    private val density = context.resources.displayMetrics.density
    private var map: MapLibreMap? = null

    /** Pixels at the map's top and bottom edges that the screen draws over, so the trip is kept out of them. */
    private var coveredTop = 0
    private var coveredBottom = 0


    private var style: Style? = null
    private var started = false
    private var resumed = false
    private var destroyed = false
    private var vehicleActive = false

    /** The status colour round the vehicle, as ARGB. */
    private var ring = 0

    /**
     * The street map file the map reads, from the first one it is given. It never changes after
     * that: MapLibre crashes if the file under an open map is replaced (maplibre-native #3658), so
     * a newer file waits until the screen opens again.
     */
    private var streetFile: File? = null

    private var latestGeometry: TripGeometry? = null
    private var latestAdherence: Adherence? = null
    private var drawnGeometry: TripGeometry? = null
    private var drawnAdherence: Adherence? = null
    private var shape: ShapeGeometry? = null
    private var state: RouteMapState? = null
    private var heading = FollowHeading()

    private val lowMemory = object : ComponentCallbacks2 {
        override fun onLowMemory() = mapView.onLowMemory()
        override fun onTrimMemory(level: Int) = Unit
        override fun onConfigurationChanged(newConfig: Configuration) = Unit
    }

    init {
        MapLibre.getInstance(context)
        mapView = MapView(context)
        // No saved state: the camera follows the vehicle, so there is nothing of its own to restore.
        mapView.onCreate(null)
        context.registerComponentCallbacks(lowMemory)
        // MapLibre drops the padding when the map changes size, as on a turn of the phone or in
        // split screen, which would let the vehicle drift back to the middle.
        mapView.addOnLayoutChangeListener { _, _, top, _, bottom, _, oldTop, _, oldBottom ->
            if (bottom - top != oldBottom - oldTop && following && state != null) applyFollowPadding()
        }
        mapView.getMapAsync { map ->
            if (destroyed) return@getMapAsync
            this.map = map
            map.uiSettings.apply {
                // As on iOS: the driver may pan and zoom, but the map never tilts, and it turns
                // only with the vehicle.
                isRotateGesturesEnabled = false
                isTiltGesturesEnabled = false
                isCompassEnabled = false
                isLogoEnabled = false
            }
            map.uiSettings.attributionGravity = Gravity.BOTTOM or Gravity.END
            placeAttribution(map)
            map.addOnCameraMoveStartedListener { reason ->
                if (reason == MapLibreMap.OnCameraMoveStartedListener.REASON_API_GESTURE) drags++
            }
            loadStyle(map)
        }
    }

    /** Puts the streets in [file] under the trip, unless the map already has a file. */
    fun useStreets(file: File?) {
        if (file == null || streetFile != null || destroyed) return
        streetFile = file
        map?.let(::loadStyle)
    }

    private fun loadStyle(map: MapLibreMap) {
        val file = streetFile
        val json = if (file != null) streetStyle(context, dark, file) else plainStyle(dark)
        // setStyle retires the current style at once, and MapLibre throws on any later use of it.
        // Until the new one has loaded there is nothing to draw on: render() waits, and
        // onStyleLoaded puts the trip back.
        style = null
        map.setStyle(Style.Builder().fromJson(json)) { style ->
            // A plain style that finishes after the streets were asked for is already being replaced.
            if (destroyed || (file == null) != (streetFile == null)) return@setStyle
            onStyleLoaded(map, style, streets = file != null)
        }
    }

    private fun onStyleLoaded(map: MapLibreMap, style: Style, streets: Boolean) {
        this.style = style
        // Above every road but below the street names, so they stay readable over the route.
        addLayers(style, below = if (streets) style.layers.firstOrNull { it is SymbolLayer }?.id else null)
        // OpenStreetMap's licence asks for its credit wherever its data is on screen.
        map.uiSettings.isAttributionEnabled = streets
        if (!vehicleActive) {
            activateVehicle(map, style)
            vehicleActive = true
        }
        repaint(style)
        render()
    }

    fun show(geometry: TripGeometry, adherence: Adherence?) {
        latestGeometry = geometry
        latestAdherence = adherence
        render()
    }

    /** Keeps the trip and the attribution out of [top] and [bottom] pixels of the map, which the screen covers. */
    fun cover(top: Int, bottom: Int) {
        if (top == coveredTop && bottom == coveredBottom) return
        coveredTop = top
        coveredBottom = bottom
        val map = map ?: return
        placeAttribution(map)
        val geometry = drawnGeometry ?: return
        when {
            state == null -> fitRoute(map, geometry)
            following -> applyFollowPadding()
        }
    }

    /**
     * OpenStreetMap's credit stays where the driver can reach it: at the bottom right, opposite the
     * map's own notes, and above whatever covers the bottom of the map.
     */
    private fun placeAttribution(map: MapLibreMap) {
        map.uiSettings.setAttributionMargins(0, 0, dp(ATTRIBUTION_MARGIN_DP), coveredBottom + dp(ATTRIBUTION_MARGIN_DP))
    }

    /** Goes back to following the vehicle, at whatever zoom the driver left the map. */
    fun follow() {
        val map = map ?: return
        if (state == null) return
        startFollowing(map, zoom = null)
    }

    /**
     * Moves the camera to the vehicle and keeps it there, heading-up. The padding is what puts
     * the vehicle a third of the way up the uncovered map (spec §6.3), with the road ahead above
     * it. It is set once the camera has arrived, because MapLibre ignores it while the camera is
     * on its way.
     */
    private fun startFollowing(map: MapLibreMap, zoom: Double?) {
        val component = map.locationComponent
        component.setCameraMode(
            CameraMode.TRACKING_GPS,
            FOLLOW_ANIMATION_MS,
            zoom,
            null,
            0.0,
            object : OnLocationCameraTransitionListener {
                override fun onLocationCameraTransitionFinished(cameraMode: Int) = applyFollowPadding()

                override fun onLocationCameraTransitionCanceled(cameraMode: Int) = Unit
            },
        )
        following = true
    }

    private fun applyFollowPadding(animationMs: Long = FOLLOW_ANIMATION_MS) {
        val map = map ?: return
        if (destroyed) return
        map.locationComponent.paddingWhileTracking(followPadding(mapView.height, coveredTop, coveredBottom), animationMs)
    }

    private fun uncoveredHeight(): Int = (mapView.height - coveredTop - coveredBottom).coerceAtLeast(0)

    fun onLifecycleEvent(event: Lifecycle.Event) {
        if (destroyed) return
        when (event) {
            Lifecycle.Event.ON_START -> { mapView.onStart(); started = true }
            Lifecycle.Event.ON_RESUME -> { mapView.onResume(); resumed = true }
            Lifecycle.Event.ON_PAUSE -> { mapView.onPause(); resumed = false }
            Lifecycle.Event.ON_STOP -> { mapView.onStop(); started = false }
            else -> Unit
        }
    }

    /** Leaving the screen with the activity still running sends no pause or stop, so they are made up here. */
    fun destroy() {
        if (destroyed) return
        destroyed = true
        context.unregisterComponentCallbacks(lowMemory)
        if (resumed) mapView.onPause()
        if (started) mapView.onStop()
        mapView.onDestroy()
    }

    private fun render() {
        val map = map ?: return
        val style = style ?: return
        val geometry = latestGeometry ?: return
        if (geometry != drawnGeometry) drawTrip(map, style, geometry)

        // The screen recomposes for reasons that have nothing to do with the vehicle. Only a new
        // fix moves anything.
        val adherence = latestAdherence ?: return
        if (adherence == drawnAdherence) return
        drawnAdherence = adherence
        val shape = shape ?: return
        val next = routeMapState(geometry, shape, adherence, heading)
        drawFix(map, style, geometry, previous = state, next = next)
        state = next
    }

    /**
     * The trip's layers. The line, its casing and the stops go below the layer called [below] when
     * there is one; the next stop's name and the matched position always go on top.
     */
    private fun addLayers(style: Style, below: String?) {
        fun addRouteLayer(layer: Layer) = if (below != null) style.addLayerBelow(layer, below) else style.addLayer(layer)

        style.addSource(GeoJsonSource(ROUTE_SOURCE))
        style.addSource(GeoJsonSource(STOPS_SOURCE))
        style.addSource(GeoJsonSource(NEXT_STOP_SOURCE))
        style.addSource(GeoJsonSource(SNAPPED_SOURCE))
        style.addSource(GeoJsonSource(OFF_ROUTE_SOURCE))
        // Casing first: layers draw in the order they are added, so the line sits centred on the
        // wider one underneath it.
        addRouteLayer(
            LineLayer(CASING_LAYER, ROUTE_SOURCE).withProperties(
                lineWidth(CASING_WIDTH),
                lineCap(Property.LINE_CAP_ROUND),
                lineJoin(Property.LINE_JOIN_ROUND),
            ),
        )
        addRouteLayer(
            LineLayer(ROUTE_LAYER, ROUTE_SOURCE).withProperties(
                lineWidth(ROUTE_WIDTH),
                lineCap(Property.LINE_CAP_ROUND),
                lineJoin(Property.LINE_JOIN_ROUND),
            ),
        )
        // A stop is white in a grey ring. The next stop, the one the driver looks for, is orange in
        // an ink ring and a white halo; the last stop is white in an ink ring.
        val isNext = Expression.eq(Expression.get(NEXT_STOP_PROPERTY), Expression.literal(true))
        val isLast = Expression.eq(Expression.get(LAST_STOP_PROPERTY), Expression.literal(true))
        addRouteLayer(
            CircleLayer(NEXT_STOP_HALO_LAYER, STOPS_SOURCE)
                .withProperties(circleRadius(NEXT_STOP_HALO_RADIUS), circleColor(STOP_FILL))
                .withFilter(isNext),
        )
        addRouteLayer(
            CircleLayer(STOPS_LAYER, STOPS_SOURCE).withProperties(
                circleRadius(
                    Expression.switchCase(
                        isNext, Expression.literal(NEXT_STOP_RADIUS),
                        isLast, Expression.literal(LAST_STOP_RADIUS),
                        Expression.literal(STOP_RADIUS),
                    ),
                ),
                circleColor(Expression.switchCase(isNext, Expression.color(NEXT_STOP_FILL), Expression.color(STOP_FILL))),
                circleStrokeColor(
                    Expression.switchCase(Expression.any(isNext, isLast), Expression.color(MARKER_INK), Expression.color(STOP_RING)),
                ),
                circleStrokeWidth(
                    Expression.switchCase(
                        Expression.any(isNext, isLast),
                        Expression.literal(MARKED_STOP_RING_WIDTH),
                        Expression.literal(STOP_RING_WIDTH),
                    ),
                ),
            ),
        )
        // The name is an image drawn by Android, not map text: map text needs font files, and
        // cannot shape scripts such as Bengali or Tamil. iOS draws its stop names the same way.
        style.addLayer(
            SymbolLayer(NEXT_STOP_LABEL_LAYER, NEXT_STOP_SOURCE).withProperties(
                iconImage(NEXT_STOP_LABEL_IMAGE),
                iconAnchor(Property.ICON_ANCHOR_LEFT),
                iconAllowOverlap(true),
                iconIgnorePlacement(true),
            ),
        )
        style.addLayer(
            LineLayer(OFF_ROUTE_LAYER, OFF_ROUTE_SOURCE).withProperties(
                lineWidth(OFF_ROUTE_LINK_WIDTH),
                lineDasharray(OFF_ROUTE_DASHES),
                lineCap(Property.LINE_CAP_ROUND),
                lineColor(labelColor),
                visibility(Property.NONE),
            ),
        )
        style.addLayer(
            CircleLayer(SNAPPED_LAYER, SNAPPED_SOURCE).withProperties(
                circleRadius(LAST_STOP_RADIUS),
                circleColor(STOP_FILL),
                circleStrokeColor(MARKER_INK),
                circleStrokeWidth(MARKED_STOP_RING_WIDTH),
                visibility(Property.NONE),
            ),
        )
    }

    /**
     * MapLibre's location component draws the vehicle and moves the camera with it, gliding both
     * between fixes in the same frame. Despite its name it reads no GPS here: its own location
     * engine is off, and [drawFix] hands it the fixes the tracking service already takes.
     */
    @SuppressLint("MissingPermission") // The permission is for the engine, which is switched off.
    private fun activateVehicle(map: MapLibreMap, style: Style) {
        val component = map.locationComponent
        component.activateLocationComponent(
            LocationComponentActivationOptions.builder(context, style)
                .useDefaultLocationEngine(false)
                .useSpecializedLocationLayer(true)
                .locationComponentOptions(vehicleOptions())
                .build(),
        )
        component.isLocationComponentEnabled = true
        component.renderMode = RenderMode.GPS
        component.addOnCameraTrackingChangedListener(object : OnCameraTrackingChangedListener {
            override fun onCameraTrackingDismissed() {
                following = false
            }

            override fun onCameraTrackingChanged(currentMode: Int) = Unit
        })
    }

    /**
     * Rings the vehicle in [argb], the status card's colour, so the map says what the card says.
     * Not the route colour iOS uses: a vehicle on its own line would be lost against it.
     */
    fun ringVehicle(argb: Int) {
        if (argb == ring) return
        ring = argb
        val map = map ?: return
        if (!vehicleActive) return
        map.locationComponent.applyStyle(vehicleOptions())
        // applyStyle also sets the map's padding to the options' own, none, which would drop the
        // vehicle to the middle. Put the follow padding straight back, in the same frame.
        if (following && state != null) applyFollowPadding(animationMs = 0)
    }

    private fun vehicleOptions(): LocationComponentOptions =
        LocationComponentOptions.builder(context)
            // The disc and arrow on top, the status ring under them. The ring is the background,
            // which MapLibre draws behind the vehicle and tints.
            .gpsDrawable(R.drawable.ic_map_vehicle_puck)
            .backgroundDrawable(R.drawable.ic_map_vehicle_ring)
            .backgroundTintColor(ring)
            .accuracyAlpha(0f)
            .elevation(0f)
            .enableStaleState(false)
            // The same size at every zoom, like the stops.
            .minZoomIconScale(1f)
            .maxZoomIconScale(1f)
            .trackingGesturesManagement(true)
            .build()

    private fun drawTrip(map: MapLibreMap, style: Style, geometry: TripGeometry) {
        drawnGeometry = geometry
        drawnAdherence = null
        state = null
        heading = FollowHeading()
        shape = ShapeGeometry.of(geometry.shapePoints)

        paintRoute(style, geometry)
        drawStops(style, geometry, nextStopIndex = -1)
        setSnappedVisible(style, visible = false)

        // The whole route until the first fix arrives. Posted, because the view may not have
        // been measured yet and the fit needs its size.
        mapView.post { if (!destroyed && state == null) fitRoute(map, geometry) }
    }

    /** The whole route in the uncovered map, until the first fix takes the camera to the vehicle. */
    private fun fitRoute(map: MapLibreMap, geometry: TripGeometry) {
        val points = geometry.shapePoints
        if (points.size < 2) return
        val bounds = LatLngBounds.Builder().includes(points.map { LatLng(it.lat, it.lon) }).build()
        val fit = dp(FIT_PADDING_DP.toFloat())
        map.moveCamera(CameraUpdateFactory.newLatLngBounds(bounds, fit, coveredTop + fit, fit, coveredBottom + fit))
    }

    private fun drawFix(map: MapLibreMap, style: Style, geometry: TripGeometry, previous: RouteMapState?, next: RouteMapState) {
        val component = map.locationComponent
        component.forceLocationUpdate(
            Location(LOCATION_PROVIDER).apply {
                latitude = next.vehicle.lat
                longitude = next.vehicle.lon
                bearing = next.course.toFloat()
                time = System.currentTimeMillis()
                elapsedRealtimeNanos = SystemClock.elapsedRealtimeNanos()
            },
        )

        // The line is only ever drawn two ways, so it is restyled only when it crosses between
        // them, or on the first fix, which decides which.
        if (previous?.isOnRoute != next.isOnRoute) paintOnRoute(style, next.isOnRoute)
        drawSnapped(style, next)
        if (previous?.nextStopIndex != next.nextStopIndex) drawStops(style, geometry, next.nextStopIndex)

        if (previous == null) {
            // The first fix: leave the whole-route view for the vehicle.
            val aheadDp = uncoveredHeight() / density * 2 / 3
            startFollowing(map, followZoom(next.vehicle.lat, aheadDp.toDouble()))
        }
    }

    /**
     * Draws the trip on a style that has just replaced another, as it was on the old one: a new
     * style starts with none of the trip's data. The camera stays where it is.
     */
    private fun repaint(style: Style) {
        val geometry = drawnGeometry ?: return
        val current = state
        paintRoute(style, geometry)
        drawStops(style, geometry, current?.nextStopIndex ?: -1)
        paintOnRoute(style, current?.isOnRoute ?: true)
        current?.let { drawSnapped(style, it) }
    }

    /** The shape in the route colour over its casing. */
    private fun paintRoute(style: Style, geometry: TripGeometry) {
        val routeColor = routeLineColor(geometry.routeColor, dark)
        style.getSourceAs<GeoJsonSource>(ROUTE_SOURCE)?.setGeoJson(routeLineString(geometry.shapePoints))
        style.getLayer(CASING_LAYER)?.setProperties(lineColor(casingColor(routeColor, dark)))
        style.getLayer(ROUTE_LAYER)?.setProperties(lineColor(routeColor))
    }

    /**
     * Off the route the matched position shows, with a dotted line to it from the vehicle. The
     * route keeps its full colour: that is when the driver needs to find it again. The vehicle's
     * ring turns grey with the status, through [ringVehicle].
     */
    private fun paintOnRoute(style: Style, onRoute: Boolean) = setSnappedVisible(style, visible = !onRoute)

    /** Where an off-route vehicle was matched on the route, and the dotted line to it. Nothing on the route. */
    private fun drawSnapped(style: Style, state: RouteMapState) {
        val snapped = state.snapped ?: return
        style.getSourceAs<GeoJsonSource>(SNAPPED_SOURCE)?.setGeoJson(Point.fromLngLat(snapped.lon, snapped.lat))
        style.getSourceAs<GeoJsonSource>(OFF_ROUTE_SOURCE)?.setGeoJson(offRouteLink(state))
    }

    private fun drawStops(style: Style, geometry: TripGeometry, nextStopIndex: Int) {
        style.getSourceAs<GeoJsonSource>(STOPS_SOURCE)?.setGeoJson(stopFeatures(geometry.stops, nextStopIndex))
        val nextStop = geometry.stops.getOrNull(nextStopIndex)
        val label = style.getSourceAs<GeoJsonSource>(NEXT_STOP_SOURCE) ?: return
        if (nextStop == null) {
            label.setGeoJson(FeatureCollection.fromFeatures(emptyList()))
            return
        }
        style.addImage(NEXT_STOP_LABEL_IMAGE, stopLabel(nextStop.name))
        label.setGeoJson(Point.fromLngLat(nextStop.lon, nextStop.lat))
    }

    private fun setSnappedVisible(style: Style, visible: Boolean) {
        val value = visibility(if (visible) Property.VISIBLE else Property.NONE)
        style.getLayer(OFF_ROUTE_LAYER)?.setProperties(value)
        style.getLayer(SNAPPED_LAYER)?.setProperties(value)
    }

    /**
     * The stop's name, outlined so it reads over the line and the background. The image starts
     * at the stop's centre and leaves room for the stop itself before the text, so anchoring it
     * by its left edge puts the name just clear of the circle.
     */
    private fun stopLabel(name: String): Bitmap {
        val paint = TextPaint(Paint.ANTI_ALIAS_FLAG).apply {
            textSize = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, LABEL_TEXT_SP, context.resources.displayMetrics)
            typeface = Typeface.DEFAULT_BOLD
        }
        val text = TextUtils.ellipsize(name, paint, dp(LABEL_MAX_WIDTH_DP).toFloat(), TextUtils.TruncateAt.END).toString()
        val halo = dp(LABEL_HALO_DP).toFloat()
        val left = dp(NEXT_STOP_HALO_RADIUS + LABEL_GAP_DP).toFloat()
        val metrics = paint.fontMetrics
        val width = (left + paint.measureText(text) + 2 * halo).roundToInt().coerceAtLeast(1)
        val height = (metrics.descent - metrics.ascent + 2 * halo).roundToInt().coerceAtLeast(1)
        val bitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val baseline = halo - metrics.ascent
        paint.style = Paint.Style.STROKE
        paint.strokeWidth = 2 * halo
        paint.strokeJoin = Paint.Join.ROUND
        paint.color = haloColor
        canvas.drawText(text, left + halo, baseline, paint)
        paint.style = Paint.Style.FILL
        paint.color = labelColor
        canvas.drawText(text, left + halo, baseline, paint)
        return bitmap
    }

    private fun dp(value: Float): Int = (value * density).roundToInt()
}

private const val LOCATION_PROVIDER = "tracker"

/** A style with nothing in it but a background, for the route to be drawn on. */
private fun plainStyle(dark: Boolean): String {
    val background = "#%06X".format(0xFFFFFF and if (dark) PLAIN_BACKGROUND_DARK else PLAIN_BACKGROUND_LIGHT)
    return """{"version":8,"sources":{},"layers":[{"id":"background","type":"background","paint":{"background-color":"$background"}}]}"""
}
