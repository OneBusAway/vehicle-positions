package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** [MapFileStore] against a real directory. */
class MapFileStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val record = MapRecord(
        current = StoredMap("https://maps.example.org/a.pmtiles", "map-a.pmtiles", FileVersion(etag = "\"v1\""), 1_000),
        pending = PendingDownload("https://maps.example.org/a.pmtiles", "map-b.pmtiles", FileVersion(etag = "\"v2\"")),
        rejected = RejectedMap("https://maps.example.org/old.pmtiles", FileVersion(lastModified = "Mon, 05 Oct 2026 10:00:00 GMT"), 900),
    )

    @Test fun `a saved record reads back unchanged`() = runTest {
        val store = MapFileStore(tempFolder.newFolder())

        store.save(record)

        assertEquals(record, store.load())
    }

    @Test fun `saving creates the directory`() = runTest {
        val store = MapFileStore(File(tempFolder.root, "not-yet/map"))

        store.save(record)

        assertEquals(record, store.load())
    }

    @Test fun `nothing saved reads back as an empty record`() = runTest {
        assertEquals(MapRecord(), MapFileStore(tempFolder.newFolder()).load())
    }

    @Test fun `a record this build cannot read loads as empty, not a crash`() = runTest {
        val dir = tempFolder.newFolder()
        File(dir, "map.json").writeText("""{"current":{"url":""")

        assertEquals(MapRecord(), MapFileStore(dir).load())
    }

    @Test fun `a save that fails leaves the record before it intact`() = runTest {
        val dir = tempFolder.newFolder()
        val store = MapFileStore(dir)
        store.save(record)
        // A directory where the temporary file goes makes the write fail before the rename.
        File(dir, "map.json.tmp").mkdir()

        val failure = runCatching { store.save(MapRecord()) }.exceptionOrNull()

        assertTrue("expected an IOException, was $failure", failure is IOException)
        assertEquals(record, store.load())
    }

    @Test fun `start-up cleaning keeps the record, the current map and the download under way`() = runTest {
        val dir = tempFolder.newFolder()
        val store = MapFileStore(dir)
        store.save(record)
        listOf("map-a.pmtiles", "map-b.pmtiles.part", "map-old.pmtiles", "map-c.pmtiles.part", "map.json.tmp")
            .forEach { File(dir, it).writeText("x") }

        store.deleteUnlisted(record)

        assertEquals(setOf("map.json", "map-a.pmtiles", "map-b.pmtiles.part"), dir.list()!!.toSet())
    }

    @Test fun `start-up cleaning with nothing recorded empties the directory but for the record`() = runTest {
        val dir = tempFolder.newFolder()
        val store = MapFileStore(dir)
        store.save(MapRecord())
        File(dir, "map-a.pmtiles").writeText("x")

        store.deleteUnlisted(MapRecord())

        assertEquals(setOf("map.json"), dir.list()!!.toSet())
    }

    @Test fun `cleaning a directory that does not exist yet does nothing`() = runTest {
        MapFileStore(File(tempFolder.root, "missing")).deleteUnlisted(record)
    }

    @Test fun `new file names never repeat`() {
        val store = MapFileStore(tempFolder.newFolder())

        assertNotEquals(store.newFileName(), store.newFileName())
        assertTrue(store.newFileName().endsWith(".pmtiles"))
    }

    @Test fun `a part file sits beside the file it becomes`() {
        val dir = tempFolder.newFolder()
        val store = MapFileStore(dir)

        assertEquals(File(dir, "map-a.pmtiles.part"), store.partFile("map-a.pmtiles"))
        assertEquals(File(dir, "map-a.pmtiles"), store.file("map-a.pmtiles"))
        assertFalse(store.file("map-a.pmtiles").exists())
    }
}
