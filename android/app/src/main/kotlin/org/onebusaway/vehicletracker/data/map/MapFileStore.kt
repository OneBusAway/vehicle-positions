package org.onebusaway.vehicletracker.data.map

import android.util.Log
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.UUID

private const val TAG = "MapFileStore"
private const val METADATA = "map.json"
private const val PART_SUFFIX = ".part"

/** What the phone holds of the agency's map, as recorded in `map.json`. */
@Serializable
data class MapRecord(
    /** The file maps open, and where it came from. */
    val current: StoredMap? = null,
    /** A download under way, kept so it carries on after the app restarts. */
    val pending: PendingDownload? = null,
    /** A version the host served that was not a usable map, so it is not fetched again. */
    val rejected: RejectedMap? = null,
)

@Serializable
data class StoredMap(val url: String, val fileName: String, val version: FileVersion, val checkedAtEpochSec: Long)

@Serializable
data class PendingDownload(val url: String, val fileName: String, val version: FileVersion? = null)

@Serializable
data class RejectedMap(val url: String, val version: FileVersion, val atEpochSec: Long)

/**
 * The agency's map files and `map.json`, the record of which one is current and what is being
 * downloaded, in one directory. The app puts that directory in `noBackupFilesDir`: the app is
 * backed up to the cloud, and a map tens of megabytes long would push it past Android's 25 MB
 * backup limit, so that nothing would be backed up at all.
 *
 * Takes a [File] rather than a `Context` so it can be tested on the JVM, as
 * [org.onebusaway.vehicletracker.data.FileTripGeometryStore] does.
 */
class MapFileStore(
    private val directory: File,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    private val json = Json { ignoreUnknownKeys = true }
    private val metadata = File(directory, METADATA)
    private val temp = File(directory, "$METADATA.tmp")

    /** What `map.json` says, or an empty record when there is none or it cannot be read. */
    suspend fun load(): MapRecord = withContext(io) {
        if (!metadata.exists()) return@withContext MapRecord()
        try {
            json.decodeFromString(MapRecord.serializer(), metadata.readText())
        } catch (e: Exception) {
            // Unreadable is as good as empty: the map is downloaded again, and the files the
            // record named go at the next start.
            Log.w(TAG, "Could not read the map record", e)
            MapRecord()
        }
    }

    suspend fun save(record: MapRecord) = withContext(io) {
        directory.mkdirs()
        // Written in full beside the real file and then renamed over it, so a crash mid-write
        // leaves the old record or the new one, never half of one.
        FileOutputStream(temp).use { out ->
            out.write(json.encodeToString(MapRecord.serializer(), record).toByteArray())
            out.fd.sync()
        }
        Files.move(temp.toPath(), metadata.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        Unit
    }

    fun file(name: String): File = File(directory, name)

    fun partFile(name: String): File = File(directory, name + PART_SUFFIX)

    /** A name no file in the directory has had. */
    fun newFileName(): String = "map-${UUID.randomUUID()}.pmtiles"

    /**
     * Deletes every file [record] does not name: maps replaced by a newer one or dropped by the
     * agency, and downloads given up on. Only safe while no map has a file open, which is why the
     * app does it at start, before any map opens, and never later.
     */
    suspend fun deleteUnlisted(record: MapRecord) = withContext(io) {
        val keep = setOfNotNull(
            METADATA,
            record.current?.fileName,
            record.pending?.fileName?.let { it + PART_SUFFIX },
        )
        directory.listFiles()?.filter { it.name !in keep }?.forEach { stray ->
            if (!stray.deleteRecursively()) Log.w(TAG, "Could not delete ${stray.name}")
        }
        Unit
    }
}
