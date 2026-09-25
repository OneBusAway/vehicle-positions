package org.onebusaway.vehicletracker.data

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import org.onebusaway.vehicletracker.engine.TripGeometry
import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption

private const val TAG = "TripGeometryStore"

/**
 * The active trip's shape, stops and thresholds, kept on the phone so that judging adherence needs
 * no network and survives a relaunch.
 */
interface TripGeometryStore {
    suspend fun save(geometry: TripGeometry)

    /** Null when there is nothing to read: none was saved, or it is in a form this build cannot read. */
    suspend fun load(): TripGeometry?

    suspend fun clear()
}

/**
 * The stored geometry, if it is [trip]'s. Saving it is best-effort, so when this trip's own save
 * failed the file can be one an earlier trip left behind — and judging this trip against another
 * trip's shape is worse than not judging it at all.
 */
suspend fun TripGeometryStore.loadFor(trip: ActiveTrip): TripGeometry? =
    load()?.takeIf { it.tripId == trip.gtfsTripId && it.serviceDate == trip.startDate }

/**
 * Keeps the geometry as JSON in one file, where iOS keeps `active-trip.json`: a shape is hundreds
 * of points, the wrong size for a Preferences DataStore value. Takes a [File] rather than a
 * `Context` so it can be tested on the JVM, the same trade [DataStoreTripStateStore] makes.
 */
class FileTripGeometryStore(private val file: File) : TripGeometryStore {
    private val json = Json { ignoreUnknownKeys = true }
    private val temp = File("${file.path}.tmp")

    override suspend fun save(geometry: TripGeometry) = withContext(Dispatchers.IO) {
        val bytes = json.encodeToString(TripGeometry.serializer(), geometry).toByteArray()
        // Written in full beside the real file and then renamed over it, so a crash mid-write
        // leaves the old file or none — never a truncated one.
        FileOutputStream(temp).use { out ->
            out.write(bytes)
            out.fd.sync()
        }
        Files.move(temp.toPath(), file.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        Unit
    }

    override suspend fun load(): TripGeometry? = withContext(Dispatchers.IO) {
        if (!file.exists()) return@withContext null
        try {
            json.decodeFromString(TripGeometry.serializer(), file.readText())
        } catch (e: Exception) {
            // However it fails, unreadable is as good as absent: the trip still reports and the
            // screen says the schedule is unavailable, where throwing would take the tracking
            // service down on every restart.
            Log.w(TAG, "Could not read the stored trip geometry", e)
            null
        }
    }

    override suspend fun clear() = withContext(Dispatchers.IO) {
        file.delete()
        Unit
    }
}
