package org.onebusaway.vehicletracker.engine

import kotlinx.serialization.Serializable

/** A WGS84 coordinate in degrees. */
@Serializable
data class GeoPoint(val lat: Double, val lon: Double)
