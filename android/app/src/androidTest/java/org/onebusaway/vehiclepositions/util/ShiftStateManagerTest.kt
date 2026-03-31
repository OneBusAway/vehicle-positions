package org.onebusaway.vehiclepositions.util

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ShiftStateManagerTest {

    private lateinit var context: Context
    private lateinit var manager: ShiftStateManager

    @Before
    fun setup() {
        context = ApplicationProvider.getApplicationContext()
        manager = ShiftStateManager(context)

        runBlocking {
            context.dataStore.edit { preferences ->
                preferences.clear()
            }
        }
    }

    @After
    fun teardown() {
        runBlocking {
            context.dataStore.edit { preferences ->
                preferences.clear()
            }
        }
    }

    @Test
    fun initialShiftStateIsNotActive() = runTest {
        assertFalse(manager.isShiftActive.first())
    }

    @Test
    fun initialVehicleIdIsNull() = runTest {
        assertNull(manager.activeVehicleId.first())
    }

    @Test
    fun startShiftSetsShiftActiveAndVehicleId() = runTest {
        manager.startShift("2045")
        assertTrue(manager.isShiftActive.first())
        assertEquals("2045", manager.activeVehicleId.first())
    }

    @Test
    fun endShiftClearsShiftState() = runTest {
        manager.startShift("2045")
        manager.endShift()
        assertFalse(manager.isShiftActive.first())
        assertNull(manager.activeVehicleId.first())
    }

    @Test
    fun startShiftWithDifferentVehicleIdUpdatesCorrectly() = runTest {
        manager.startShift("2045")
        manager.endShift()
        manager.startShift("9999")
        assertEquals("9999", manager.activeVehicleId.first())
    }
}