package org.onebusaway.vehicletracker.data.map

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/** No map file is deleted while a map is up, nor a map brought up halfway through a delete. */
class OpenMapsTest {
    @Test fun `a delete runs while no map is up, and not while one is`() {
        var deletes = 0

        assertTrue(OpenMaps.whileNoneOpen { deletes++ })
        OpenMaps.opened()
        try {
            assertFalse(OpenMaps.whileNoneOpen { deletes++ })
        } finally {
            OpenMaps.closed()
        }
        assertTrue(OpenMaps.whileNoneOpen { deletes++ })

        assertEquals(2, deletes)
    }

    // A map that came up after the check, but before the files went, could load one of them as it went.
    @Test fun `a map that comes up during a delete waits for it to finish`() {
        val deleting = CountDownLatch(1)
        val finish = CountDownLatch(1)
        val delete = thread { OpenMaps.whileNoneOpen { deleting.countDown(); finish.await() } }
        assertTrue(deleting.await(5, TimeUnit.SECONDS))
        val up = CountDownLatch(1)
        val map = thread { OpenMaps.opened(); up.countDown() }
        try {
            // Blocked on the lock the delete holds: before that, the map may not have tried to come up at all.
            assertTrue("it waits on the delete", within(5_000) { map.state == Thread.State.BLOCKED })
            assertEquals("not up while the delete runs", 1L, up.count)
            finish.countDown()
            assertTrue("once it is done", up.await(5, TimeUnit.SECONDS))
            assertFalse("and then no delete while it is up", OpenMaps.whileNoneOpen {})
        } finally {
            finish.countDown()
            delete.join()
            map.join()
            OpenMaps.closed()
        }
    }

    /** Whether [condition] holds within [ms], checked every 10 ms. */
    private fun within(ms: Long, condition: () -> Boolean): Boolean {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(ms)
        while (System.nanoTime() < deadline) {
            if (condition()) return true
            Thread.sleep(10)
        }
        return condition()
    }
}
