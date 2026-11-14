package org.onebusaway.vehicletracker.data

import kotlinx.serialization.Serializable

/**
 * How full the driver says the vehicle is: the four GTFS-RT `OccupancyStatus` values the Tracking screen offers, named
 * exactly as a location report sends them and the feed publishes them.
 */
@Serializable
enum class OccupancyStatus { MANY_SEATS_AVAILABLE, FEW_SEATS_AVAILABLE, STANDING_ROOM_ONLY, FULL }
