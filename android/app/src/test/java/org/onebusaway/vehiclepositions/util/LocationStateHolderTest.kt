package org.onebusaway.vehiclepositions.util

import android.location.Location
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.mockito.Mockito.mock
import org.mockito.Mockito.`when`

class LocationStateHolderTest {

    private lateinit var holder: LocationStateHolder

    @Before
    fun setup() {
        holder = LocationStateHolder()
    }

    @Test
    fun `initial location is null`() {
        assertNull(holder.lastLocation.value)
    }

    @Test
    fun `hasLocation returns false when no location set`() {
        assertFalse(holder.hasLocation())
    }

    @Test
    fun `hasLocation returns true after location updated`() {
        val mockLocation = mock(Location::class.java)

        holder.updateLocation(mockLocation)
        assertTrue(holder.hasLocation())
    }

    @Test
    fun `lastLocation value updated after updateLocation`() {
        val mockLocation = mock(Location::class.java)
        `when`(mockLocation.latitude).thenReturn(1.2921)
        `when`(mockLocation.longitude).thenReturn(36.8219)

        holder.updateLocation(mockLocation)

        assertTrue(holder.lastLocation.value?.latitude == 1.2921)
        assertTrue(holder.lastLocation.value?.longitude == 36.8219)
    }
}