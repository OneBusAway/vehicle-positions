package org.onebusaway.vehicletracker.ui.map

import org.maplibre.geojson.Feature
import org.maplibre.geojson.FeatureCollection
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.engine.TripStop

// The stop markers' colours, on the map and in the trip panel's stop list alike: a plain stop is white
// in a grey ring, the next stop orange in an ink ring with a white halo, and the last stop white in an ink
// ring. The design draws them the same in both themes.
const val STOP_FILL = 0xFFFFFFFF.toInt()
const val STOP_RING = 0xFF6B6B6B.toInt()
const val NEXT_STOP_FILL = 0xFFFFA726.toInt()
const val MARKER_INK = 0xFF1F1F1F.toInt()

/** The feature properties that mark the next stop and the end of the line, which the map draws apart. */
const val NEXT_STOP_PROPERTY = "next"
const val LAST_STOP_PROPERTY = "last"

/** The trip's stops as map points, in stop order, each saying whether it is the next one and whether it is the last. */
fun stopFeatures(stops: List<TripStop>, nextStopIndex: Int): FeatureCollection =
    FeatureCollection.fromFeatures(
        stops.mapIndexed { index, stop ->
            Feature.fromGeometry(Point.fromLngLat(stop.lon, stop.lat)).apply {
                addBooleanProperty(NEXT_STOP_PROPERTY, index == nextStopIndex)
                addBooleanProperty(LAST_STOP_PROPERTY, index == stops.lastIndex)
            }
        },
    )
