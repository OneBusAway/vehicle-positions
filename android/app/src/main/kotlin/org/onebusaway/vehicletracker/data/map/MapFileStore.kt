package org.onebusaway.vehicletracker.data.map

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
import org.onebusaway.vehicletracker.data.AppLog

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
    /** The servers whose map the driver deleted from this phone: it stays off until they get it again. */
    val deleted: Set<String> = emptySet(),
)

/**
 * [server] is the server that gave [url], as [org.onebusaway.vehicletracker.data.normalizedServerUrl] gives it. It is
 * null in a record saved by a build from before maps were tied to a server.
 */
@Serializable
data class StoredMap(
    val url: String,
    val fileName: String,
    val version: FileVersion,
    val checkedAtEpochSec: Long,
    val server: String? = null,
)

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

    /**
     * What `map.json` says, or an empty record when there is none. A read that fails throws, so
     * nothing is downloaded or saved over a record nobody could see, such as which servers' maps
     * the driver deleted.
     */
    suspend fun load(): MapRecord = withContext(io) {
        if (!metadata.exists()) return@withContext MapRecord()
        val text = metadata.readText()
        try {
            json.decodeFromString(MapRecord.serializer(), text)
        } catch (e: IllegalArgumentException) {
            // Read, but not a record this build understands, and it never will be: as good as
            // empty, so the map is downloaded again and the files it named go at the next start.
            AppLog.w(TAG, "Could not make sense of the map record", e)
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

    /** How long the file [name] is and when it was last written, or null when it is not here. */
    suspend fun stat(name: String): Pair<Long, Long>? = withContext(io) {
        val file = file(name)
        if (file.isFile) file.length() to file.lastModified() else null
    }

    fun partFile(name: String): File = File(directory, name + PART_SUFFIX)

    /** A name no file in the directory has had. */
    fun newFileName(): String = "map-${UUID.randomUUID()}.pmtiles"

    /**
     * Deletes every file [record] does not name: maps replaced by a newer one or dropped by the
     * agency, and downloads given up on. Only while no map is up, as [OpenMaps] has it: the app
     * does it at start, before any map opens, and after the driver deletes the map. Returns false,
     * having deleted nothing, while a map is up; the files then wait for the next start.
     */
    suspend fun deleteUnlisted(record: MapRecord): Boolean = withContext(io) {
        OpenMaps.whileNoneOpen {
            val keep = setOfNotNull(
                METADATA,
                record.current?.fileName,
                record.pending?.fileName?.let { it + PART_SUFFIX },
            )
            directory.listFiles()?.filter { it.name !in keep }?.forEach { stray ->
                if (!stray.deleteRecursively()) AppLog.w(TAG, "Could not delete ${stray.name}")
            }
        }
    }
}
