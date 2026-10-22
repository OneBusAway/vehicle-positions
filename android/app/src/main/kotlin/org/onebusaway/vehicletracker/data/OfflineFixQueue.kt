package org.onebusaway.vehicletracker.data

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import org.onebusaway.vehicletracker.data.api.LocationReportDto
import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption

/**
 * A day of fixes at one every ten seconds. A phone out of coverage for longer than that loses its
 * oldest fixes first; the server takes none older than a day anyway.
 */
const val OFFLINE_QUEUE_CAPACITY = 8_640

/**
 * Reports the phone could not send for want of a connection, oldest first, kept to be uploaded
 * for location history once it can (`POST /api/v1/locations/history`). They never go to the
 * realtime feed: by then they describe where the bus was, not where it is.
 */
interface OfflineFixQueue {
    suspend fun add(report: LocationReportDto)
    suspend fun oldest(max: Int): List<LocationReportDto>
    suspend fun removeOldest(count: Int)

    /**
     * Hands the queue to [account], the driver who just signed in. Fixes queued under anyone else
     * are dropped: they would upload under this driver's token and be stored as this driver's.
     */
    suspend fun claimFor(account: String)
}

/**
 * Keeps the queue as one JSON report per line in [file], so it survives the tracking service and
 * the process being stopped. An add appends a line; a removal rewrites the file beside the real
 * one and renames it over, as [FileTripGeometryStore] does, so a crash never leaves it truncated.
 * Takes a [File] rather than a `Context` so it can be tested on the JVM.
 */
class FileOfflineFixQueue(
    private val file: File,
    private val capacity: Int = OFFLINE_QUEUE_CAPACITY,
) : OfflineFixQueue {
    private val json = Json { ignoreUnknownKeys = true }
    private val temp = File("${file.path}.tmp")
    private val ownerFile = File("${file.path}.owner")
    private val mutex = Mutex()
    private var queue: ArrayDeque<LocationReportDto>? = null

    override suspend fun add(report: LocationReportDto) = locked { q ->
        q.addLast(report)
        if (q.size > capacity) {
            repeat(q.size - capacity) { q.removeFirst() }
            rewrite(q)
        } else {
            file.appendText(json.encodeToString(LocationReportDto.serializer(), report) + "\n")
        }
    }

    override suspend fun oldest(max: Int): List<LocationReportDto> = locked { q -> q.take(max) }

    override suspend fun removeOldest(count: Int) = locked { q ->
        repeat(minOf(count, q.size)) { q.removeFirst() }
        rewrite(q)
    }

    override suspend fun claimFor(account: String) = locked { q ->
        if (ownerFile.takeIf { it.exists() }?.readText() == account) return@locked
        if (q.isNotEmpty()) {
            Log.w(TAG, "Dropping ${q.size} fixes queued under another account")
            q.clear()
            rewrite(q)
        }
        ownerFile.writeText(account)
    }

    private suspend fun <T> locked(block: (ArrayDeque<LocationReportDto>) -> T): T =
        withContext(Dispatchers.IO) { mutex.withLock { block(queue ?: load().also { queue = it }) } }

    private fun load(): ArrayDeque<LocationReportDto> {
        if (!file.exists()) return ArrayDeque()
        // A line cut short by a crash mid-append is skipped, not allowed to cost the rest.
        val text = file.readText()
        val lines = text.lines().filter { it.isNotBlank() }
        val loaded = lines.mapNotNullTo(ArrayDeque()) { line ->
            runCatching { json.decodeFromString(LocationReportDto.serializer(), line) }
                .onFailure { Log.w(TAG, "Skipping an unreadable queued fix", it) }
                .getOrNull()
        }
        // Left in place, the cut-short line would join the next append and cost that fix too.
        if (loaded.size != lines.size || (text.isNotEmpty() && !text.endsWith("\n"))) rewrite(loaded)
        return loaded
    }

    private fun rewrite(q: ArrayDeque<LocationReportDto>) {
        FileOutputStream(temp).use { out ->
            q.forEach { out.write((json.encodeToString(LocationReportDto.serializer(), it) + "\n").toByteArray()) }
            out.fd.sync()
        }
        Files.move(temp.toPath(), file.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
    }

    private companion object {
        const val TAG = "OfflineFixQueue"
    }
}
