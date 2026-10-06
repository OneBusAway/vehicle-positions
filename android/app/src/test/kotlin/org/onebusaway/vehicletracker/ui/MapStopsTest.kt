package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import org.maplibre.geojson.Point
import org.onebusaway.vehicletracker.engine.TripFixtures
import org.onebusaway.vehicletracker.ui.map.NEXT_STOP_PROPERTY
import org.onebusaway.vehicletracker.ui.map.stopFeatures

class MapStopsTest {
    private val stops = TripFixtures.t1.stops

    @Test fun `every stop is a point, in stop order, longitude first`() {
        val features = requireNotNull(stopFeatures(stops, nextStopIndex = 0).features())

        assertEquals(stops.size, features.size)
        features.forEachIndexed { index, feature ->
            val point = feature.geometry() as Point
            assertEquals(stops[index].lon, point.longitude(), 0.0)
            assertEquals(stops[index].lat, point.latitude(), 0.0)
        }
    }

    @Test fun `only the next stop is marked`() {
        val features = requireNotNull(stopFeatures(stops, nextStopIndex = 1).features())

        assertEquals(listOf(false, true, false), features.map { it.getBooleanProperty(NEXT_STOP_PROPERTY) })
    }

    @Test fun `an index that is no stop marks none`() {
        val features = requireNotNull(stopFeatures(stops, nextStopIndex = -1).features())

        assertEquals(listOf(false, false, false), features.map { it.getBooleanProperty(NEXT_STOP_PROPERTY) })
    }
}
