package org.onebusaway.vehicletracker.ui.map

import org.maplibre.geojson.Feature
import org.maplibre.geojson.FeatureCollection
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.engine.TripStop

/** The feature property that marks the next stop, which the map draws larger. */
const val NEXT_STOP_PROPERTY = "next"

/** The trip's stops as map points, in stop order, each saying whether it is the next one. */
fun stopFeatures(stops: List<TripStop>, nextStopIndex: Int): FeatureCollection =
    FeatureCollection.fromFeatures(
        stops.mapIndexed { index, stop ->
            Feature.fromGeometry(Point.fromLngLat(stop.lon, stop.lat)).apply {
                addBooleanProperty(NEXT_STOP_PROPERTY, index == nextStopIndex)
            }
        },
    )
