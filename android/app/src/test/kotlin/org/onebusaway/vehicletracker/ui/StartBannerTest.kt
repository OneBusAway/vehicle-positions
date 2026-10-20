package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.runs.BannerAction
import org.onebusaway.vehicletracker.ui.runs.TripError
import org.onebusaway.vehicletracker.ui.runs.startBannerOf

class StartBannerTest {
    @Test fun `each refused start says what happened and offers the one action that fixes it`() {
        val expected = mapOf(
            TripError.NOT_ASSIGNED to (R.string.runs_error_not_assigned to BannerAction.CHANGE_BUS),
            TripError.TRIP_ACTIVE to (R.string.runs_error_trip_active to null),
            TripError.VEHICLE_BUSY to (R.string.runs_error_vehicle_busy to BannerAction.CHANGE_BUS),
            TripError.VEHICLE_INACTIVE to (R.string.runs_error_vehicle_inactive to BannerAction.CHANGE_BUS),
            TripError.PREVIOUS_TRIP_NOT_ENDED to (R.string.runs_error_previous_trip_not_ended to BannerAction.TRY_AGAIN),
            TripError.NETWORK to (R.string.runs_error_network to BannerAction.TRY_AGAIN),
            TripError.NO_GEOMETRY to (R.string.runs_error_no_geometry to null),
            TripError.TRIP_NOT_ACTIVE to (R.string.runs_error_trip_not_active to BannerAction.RELOAD),
            TripError.OTHER to (R.string.runs_error_other to BannerAction.TRY_AGAIN),
        )
        assertEquals("every refusal has a banner", TripError.entries.toSet(), expected.keys)
        for ((error, banner) in expected) {
            assertEquals("$error text", banner.first, startBannerOf(error).text)
            assertEquals("$error action", banner.second, startBannerOf(error).action)
        }
    }
}
