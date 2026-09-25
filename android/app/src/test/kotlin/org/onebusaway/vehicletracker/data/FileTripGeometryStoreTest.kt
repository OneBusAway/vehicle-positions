package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.onebusaway.vehicletracker.engine.TripFixtures
import java.io.File
import java.io.IOException

/** [FileTripGeometryStore] against a real file in a temporary directory. */
class FileTripGeometryStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val t1Trip = ActiveTrip(7L, "T1", "bus-1", "R1", "20260902", 100L)

    private fun storeIn(dir: File): FileTripGeometryStore = FileTripGeometryStore(File(dir, "active_trip_geometry.json"))

    @Test fun `a saved geometry reads back unchanged`() = runTest {
        val store = storeIn(tempFolder.newFolder())

        store.save(TripFixtures.t1)

        assertEquals(TripFixtures.t1, store.load())
    }

    @Test fun `nothing saved reads back as nothing`() = runTest {
        assertNull(storeIn(tempFolder.newFolder()).load())
    }

    @Test fun `clear removes the saved geometry`() = runTest {
        val store = storeIn(tempFolder.newFolder())
        store.save(TripFixtures.t1)

        store.clear()

        assertNull(store.load())
    }

    @Test fun `a save replaces the geometry before it`() = runTest {
        val store = storeIn(tempFolder.newFolder())
        store.save(TripFixtures.t1)

        store.save(TripFixtures.loop)

        assertEquals(TripFixtures.loop, store.load())
    }

    @Test fun `a save that fails leaves the geometry before it intact`() = runTest {
        val dir = tempFolder.newFolder()
        val store = storeIn(dir)
        store.save(TripFixtures.t1)
        // A directory where the temporary file goes makes the write fail before the rename.
        File(dir, "active_trip_geometry.json.tmp").mkdir()

        val failure = runCatching { store.save(TripFixtures.loop) }.exceptionOrNull()

        assertTrue("expected an IOException, was $failure", failure is IOException)
        assertEquals(TripFixtures.t1, store.load())
    }

    @Test fun `a file this build cannot read loads as nothing, not a crash`() = runTest {
        val dir = tempFolder.newFolder()
        File(dir, "active_trip_geometry.json").writeText("""{"tripId":"T1","shape":""")

        assertNull(storeIn(dir).load())
    }

    @Test fun `a geometry is loaded only for the trip it was saved for`() = runTest {
        val store = storeIn(tempFolder.newFolder())
        store.save(TripFixtures.t1)

        assertEquals(TripFixtures.t1, store.loadFor(t1Trip))
        // A file some earlier trip left behind, when this trip's own save failed.
        assertNull(store.loadFor(t1Trip.copy(gtfsTripId = "T4")))
        assertNull(store.loadFor(t1Trip.copy(startDate = "20260903")))
    }
}
