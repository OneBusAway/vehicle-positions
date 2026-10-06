package org.onebusaway.vehicletracker.ui.map

import android.annotation.SuppressLint
import android.app.ActivityManager
import android.content.ComponentCallbacks2
import android.content.Context
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Typeface
import android.location.Location
import android.os.SystemClock
import android.text.TextPaint
import android.text.TextUtils
import android.util.TypedValue
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilledTonalButton
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
import org.maplibre.android.style.layers.PropertyFactory.lineJoin
import org.maplibre.android.style.layers.PropertyFactory.lineOpacity
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
import org.onebusaway.vehicletracker.engine.GeoPoint
import org.onebusaway.vehicletracker.engine.ShapeGeometry
import org.onebusaway.vehicletracker.engine.TripGeometry
import java.io.File
import kotlin.math.roundToInt

// The sizes and the off-route look are iOS's, from RouteMapViewController and MapGlyphs
// (spec §6.3). MapLibre measures in density-independent pixels, as iOS does in points.
private const val CASING_WIDTH = 10f
private const val ROUTE_WIDTH = 6f
private const val OFF_ROUTE_OPACITY = 0.35f
private const val STOP_RING_WIDTH = 2f

/** A stop is 10 across on iOS and the next one 18, ring included; MapLibre adds the ring outside the radius. */
private const val STOP_RADIUS = 5f - STOP_RING_WIDTH
private const val NEXT_STOP_RADIUS = 9f - STOP_RING_WIDTH
private const val SNAPPED_OUTER_RADIUS = 5f
private const val SNAPPED_INNER_RADIUS = 3f
private const val FIT_PADDING_DP = 40
private const val LABEL_TEXT_SP = 13f
private const val LABEL_GAP_DP = 6f
private const val LABEL_HALO_DP = 1.5f
private const val LABEL_MAX_WIDTH_DP = 200f

private const val ROUTE_SOURCE = "route"
private const val STOPS_SOURCE = "stops"
private const val NEXT_STOP_SOURCE = "next-stop"
private const val SNAPPED_SOURCE = "snapped"
private const val CASING_LAYER = "route-casing"
private const val ROUTE_LAYER = "route-line"
private const val STOPS_LAYER = "stops"
private const val NEXT_STOP_LABEL_LAYER = "next-stop-label"
private const val SNAPPED_OUTER_LAYER = "snapped-outer"
private const val SNAPPED_INNER_LAYER = "snapped-inner"
private const val NEXT_STOP_LABEL_IMAGE = "next-stop-label"

private const val FOLLOW_ANIMATION_MS = 750L

/** How long the map stays where the driver dragged it before it goes back to following. */
private const val RETURN_TO_FOLLOWING_MS = 10_000L

/** `ConfigurationInfo.reqGlEsVersion` for this phone, the argument [supportsMap] takes. */
fun Context.glEsVersion(): Int = getSystemService(ActivityManager::class.java).deviceConfigurationInfo.reqGlEsVersion

/**
 * The trip on a map, as spec §6.3 has it: the shape in the route colour over a darker casing,
 * the stops with the next one enlarged and named, the vehicle pointed along its course, and the
 * matched position while it is off the route. The camera follows heading-up with the vehicle a
 * third of the way up. Under it is the agency's street map once the file is on the phone, and a
 * plain background until then, with a note while the first copy downloads.
 */
@Composable
fun RouteMap(geometry: TripGeometry, adherence: Adherence?, mapFile: MapFileState, modifier: Modifier = Modifier) {
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
        MapFileNote(mapFile, Modifier.align(Alignment.TopStart).padding(8.dp))
        if (!controller.following) {
            FilledTonalButton(
                onClick = controller::follow,
                modifier = Modifier.align(Alignment.BottomEnd).padding(12.dp),
            ) {
                Text(stringResource(R.string.tracking_map_recenter))
            }
        }
    }
}

/** A line over the map while it has no streets to show yet. Nothing once it has, or when the agency has no map. */
@Composable
private fun MapFileNote(state: MapFileState, modifier: Modifier = Modifier) {
    val text = when (state) {
        is MapFileState.Downloading -> stringResource(R.string.tracking_map_downloading, downloadPercent(state.bytes, state.total))
        is MapFileState.Failed -> stringResource(
            if (state.reason == MapFailure.NO_SPACE) R.string.tracking_map_no_space else R.string.tracking_map_retrying,
        )
        MapFileState.None, is MapFileState.Ready -> return
    }
    Surface(
        modifier = modifier,
        shape = MaterialTheme.shapes.small,
        color = MaterialTheme.colorScheme.surface.copy(alpha = 0.9f),
        shadowElevation = 2.dp,
    ) {
        Text(text, Modifier.padding(horizontal = 10.dp, vertical = 6.dp), style = MaterialTheme.typography.labelMedium)
    }
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
    private var style: Style? = null
    private var started = false
    private var resumed = false
    private var destroyed = false
    private var vehicleActive = false

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
        // The panel above the map grows and shrinks with its text, and the map with it. MapLibre
        // drops the padding when the map changes size, which would let the vehicle drift back to
        // the middle.
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

    /** Goes back to following the vehicle, at whatever zoom the driver left the map. */
    fun follow() {
        val map = map ?: return
        if (state == null) return
        startFollowing(map, zoom = null)
    }

    /**
     * Moves the camera to the vehicle and keeps it there, heading-up. The padding is what puts
     * the vehicle a third of the way up the map (spec §6.3), with the road ahead above it. It is
     * set once the camera has arrived, because MapLibre ignores it while the camera is on its way.
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
        map.locationComponent.paddingWhileTracking(doubleArrayOf(0.0, mapView.height / 3.0, 0.0, 0.0), animationMs)
    }

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
        val isNext = Expression.eq(Expression.get(NEXT_STOP_PROPERTY), Expression.literal(true))
        addRouteLayer(
            CircleLayer(STOPS_LAYER, STOPS_SOURCE).withProperties(
                circleRadius(Expression.switchCase(isNext, Expression.literal(NEXT_STOP_RADIUS), Expression.literal(STOP_RADIUS))),
                circleStrokeColor(Color.WHITE),
                circleStrokeWidth(STOP_RING_WIDTH),
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
            CircleLayer(SNAPPED_OUTER_LAYER, SNAPPED_SOURCE).withProperties(
                circleRadius(SNAPPED_OUTER_RADIUS),
                circleColor(haloColor),
                visibility(Property.NONE),
            ),
        )
        style.addLayer(
            CircleLayer(SNAPPED_INNER_LAYER, SNAPPED_SOURCE).withProperties(
                circleRadius(SNAPPED_INNER_RADIUS),
                circleColor(labelColor),
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
                .locationComponentOptions(vehicleOptions(onRoute = true))
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

    private fun vehicleOptions(onRoute: Boolean): LocationComponentOptions =
        LocationComponentOptions.builder(context)
            // Blue rather than the route colour iOS uses: on a yellow route a yellow arrow is
            // lost against the line. Grey off the route, as on iOS.
            .gpsDrawable(if (onRoute) R.drawable.ic_map_vehicle else R.drawable.ic_map_vehicle_off_route)
            .backgroundDrawable(R.drawable.ic_map_vehicle_no_backing)
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
        val points = geometry.shapePoints
        if (points.size >= 2) {
            val bounds = LatLngBounds.Builder().includes(points.map { LatLng(it.lat, it.lon) }).build()
            mapView.post {
                if (!destroyed && state == null) {
                    map.moveCamera(CameraUpdateFactory.newLatLngBounds(bounds, dp(FIT_PADDING_DP.toFloat())))
                }
            }
        }
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
        next.snapped?.let { drawSnapped(style, it) }
        if (previous?.nextStopIndex != next.nextStopIndex) drawStops(style, geometry, next.nextStopIndex)

        if (previous == null) {
            // The first fix: leave the whole-route view for the vehicle.
            val aheadDp = mapView.height / density * 2 / 3
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
        current?.snapped?.let { drawSnapped(style, it) }
    }

    /** The shape in the route colour over its darker casing, and the stops in the same colour. */
    private fun paintRoute(style: Style, geometry: TripGeometry) {
        val routeColor = routeLineColor(geometry.routeColor)
        style.getSourceAs<GeoJsonSource>(ROUTE_SOURCE)?.setGeoJson(routeLineString(geometry.shapePoints))
        style.getLayer(CASING_LAYER)?.setProperties(lineColor(casingColor(routeColor)), lineOpacity(1f))
        style.getLayer(ROUTE_LAYER)?.setProperties(lineColor(routeColor), lineOpacity(1f))
        style.getLayer(STOPS_LAYER)?.setProperties(circleColor(routeColor))
    }

    /** Off the route the line fades, the vehicle turns grey and the matched position shows, as on iOS. */
    private fun paintOnRoute(style: Style, onRoute: Boolean) {
        val opacity = if (onRoute) 1f else OFF_ROUTE_OPACITY
        style.getLayer(CASING_LAYER)?.setProperties(lineOpacity(opacity))
        style.getLayer(ROUTE_LAYER)?.setProperties(lineOpacity(opacity))
        map?.locationComponent?.applyStyle(vehicleOptions(onRoute))
        // applyStyle also sets the map's padding to the options' own, none, which would drop the
        // vehicle to the middle. Put the follow padding straight back, in the same frame.
        if (following && state != null) applyFollowPadding(animationMs = 0)
        setSnappedVisible(style, visible = !onRoute)
    }

    private fun drawSnapped(style: Style, snapped: GeoPoint) {
        style.getSourceAs<GeoJsonSource>(SNAPPED_SOURCE)?.setGeoJson(Point.fromLngLat(snapped.lon, snapped.lat))
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
        style.getLayer(SNAPPED_OUTER_LAYER)?.setProperties(value)
        style.getLayer(SNAPPED_INNER_LAYER)?.setProperties(value)
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
        val left = dp(NEXT_STOP_RADIUS + STOP_RING_WIDTH + LABEL_GAP_DP).toFloat()
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

// iOS's systemGray6, light and dark.
private const val BACKGROUND_LIGHT = "#F2F2F7"
private const val BACKGROUND_DARK = "#1C1C1E"

/** A style with nothing in it but a background, for the route to be drawn on. */
private fun plainStyle(dark: Boolean): String {
    val background = if (dark) BACKGROUND_DARK else BACKGROUND_LIGHT
    return """{"version":8,"sources":{},"layers":[{"id":"background","type":"background","paint":{"background-color":"$background"}}]}"""
}
