package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okio.BufferedSource
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.RandomAccessFile
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** The headers that tell one version of the file on its host from another. */
@Serializable
data class FileVersion(val etag: String? = null, val lastModified: String? = null) {
    val isKnown: Boolean get() = etag != null || lastModified != null

    /** True only when both name the same version. Two versions with nothing to compare are not the same. */
    fun sameAs(other: FileVersion?): Boolean = when {
        other == null -> false
        etag != null && other.etag != null -> etag == other.etag
        lastModified != null && other.lastModified != null -> lastModified == other.lastModified
        else -> false
    }
}

data class FetchRequest(
    val url: String,
    /** Where the bytes go while they arrive. It may hold the start of the file from an earlier try. */
    val part: File,
    /** Where the finished file is moved to. */
    val target: File,
    /** The version the bytes already in [part] belong to, to carry on from; null starts over. */
    val partVersion: FileVersion?,
    /** A version the phone already has, or turned down. The fetch ends [FetchResult.Unchanged] if the host still serves it. */
    val knownVersion: FileVersion?,
)

sealed interface FetchResult {
    /** The host still serves [FetchRequest.knownVersion]. Nothing was written. */
    data object Unchanged : FetchResult

    /** The whole file is at [FetchRequest.target], checked. */
    data class Complete(val version: FileVersion, val length: Long) : FetchResult

    /** [version] is what the host sent, when it sent anything. */
    data class Failed(val reason: MapFailure, val version: FileVersion? = null) : FetchResult
}

/** Fetches the agency's map file. An interface so [MapRepository] can be tested without a network. */
fun interface MapFileFetcher {
    /**
     * Downloads [request]'s file. [onVersion] runs once the host has said which version it is
     * sending and before any of it is written, so the caller can record what the part file holds;
     * [onProgress] runs as the bytes arrive, with a total of -1 when the host gives none.
     */
    suspend fun fetch(
        request: FetchRequest,
        onVersion: suspend (FileVersion) -> Unit,
        onProgress: (bytes: Long, total: Long) -> Unit,
    ): FetchResult
}

/**
 * Downloads a PMTiles file over plain HTTP from wherever the agency hosts it.
 *
 * A broken download carries on from where it stopped: the bytes collect in a part file, and the
 * next try asks for the rest with `Range`, guarded by `If-Range` so that a file replaced in
 * between is fetched afresh rather than spliced onto the old one. Only a file that is complete
 * and whose own header agrees with its length is moved into place, so a map never opens a
 * truncated file.
 */
class MapFileDownloader(
    private val client: OkHttpClient,
    private val freeSpace: (File) -> Long = File::getUsableSpace,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) : MapFileFetcher {

    override suspend fun fetch(
        request: FetchRequest,
        onVersion: suspend (FileVersion) -> Unit,
        onProgress: (bytes: Long, total: Long) -> Unit,
    ): FetchResult = withContext(io) {
        try {
            val resume = request.partVersion?.takeIf { it.isKnown && request.part.length() >= HEADER_BYTES }
            if (resume == null) request.part.delete()
            when (val first = attempt(request, resume, onVersion, onProgress)) {
                // The host would not serve the rest of the part: start over, once.
                Attempt.RangeRefused -> {
                    request.part.delete()
                    (attempt(request, null, onVersion, onProgress) as? Attempt.Done)?.result
                        ?: FetchResult.Failed(MapFailure.NETWORK)
                }
                is Attempt.Done -> first.result
            }
        } catch (e: IOException) {
            // The part keeps what arrived, for the next try to carry on from.
            FetchResult.Failed(MapFailure.NETWORK)
        }
    }

    private sealed interface Attempt {
        data object RangeRefused : Attempt
        data class Done(val result: FetchResult) : Attempt
    }

    private suspend fun attempt(
        request: FetchRequest,
        resume: FileVersion?,
        onVersion: suspend (FileVersion) -> Unit,
        onProgress: (Long, Long) -> Unit,
    ): Attempt {
        val offset = if (resume != null) request.part.length() else 0L
        val http = Request.Builder()
            .url(request.url)
            // OkHttp otherwise asks for gzip and unzips it quietly, which hides the file's length
            // and makes byte ranges meaningless.
            .header("Accept-Encoding", "identity")
        if (resume != null) {
            http.header("Range", "bytes=$offset-")
            http.header("If-Range", resume.etag ?: resume.lastModified!!)
        } else {
            request.knownVersion?.etag?.let { http.header("If-None-Match", it) }
                ?: request.knownVersion?.lastModified?.let { http.header("If-Modified-Since", it) }
        }

        client.newCall(http.build()).await().use { response ->
            val version = FileVersion(response.header("ETag"), response.header("Last-Modified"))
            val start: Long
            val total: Long
            when (response.code) {
                HTTP_NOT_MODIFIED -> return Attempt.Done(FetchResult.Unchanged)
                HTTP_RANGE_NOT_SATISFIABLE ->
                    return if (resume != null) Attempt.RangeRefused else Attempt.Done(FetchResult.Failed(MapFailure.NETWORK, version))
                HTTP_OK -> {
                    // A host that ignores If-None-Match still names the version it sends.
                    if (resume == null && version.sameAs(request.knownVersion)) return Attempt.Done(FetchResult.Unchanged)
                    start = 0
                    total = response.body?.contentLength() ?: -1
                }
                HTTP_PARTIAL_CONTENT -> {
                    val range = contentRange(response.header("Content-Range"))
                    if (resume == null || range == null || range.first != offset) {
                        request.part.delete()
                        return Attempt.Done(FetchResult.Failed(MapFailure.NETWORK, version))
                    }
                    start = offset
                    total = range.second
                }
                else -> return Attempt.Done(FetchResult.Failed(MapFailure.NETWORK, version))
            }
            val source = response.body?.source() ?: return Attempt.Done(FetchResult.Failed(MapFailure.NETWORK, version))

            val directory = requireNotNull(request.part.absoluteFile.parentFile)
            directory.mkdirs()
            val needed = (if (total > 0) total - start else 0L) + SPACE_TO_SPARE
            if (freeSpace(directory) < needed) return Attempt.Done(FetchResult.Failed(MapFailure.NO_SPACE, version))

            // The first bytes say whether this is a map file at all, so a wrong URL costs a few
            // bytes rather than the whole download.
            if (start == 0L && !source.startsWith(MAGIC)) return Attempt.Done(FetchResult.Failed(MapFailure.INVALID_FILE, version))

            onVersion(version)
            var written = start
            FileOutputStream(request.part, start > 0).use { out ->
                val buffer = ByteArray(BUFFER_BYTES)
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val read = source.read(buffer)
                    if (read == -1) break
                    out.write(buffer, 0, read)
                    written += read
                    onProgress(written, total)
                }
                out.fd.sync()
            }

            if (total > 0 && written != total) return Attempt.Done(FetchResult.Failed(MapFailure.NETWORK, version))
            if (!headerMatchesLength(request.part)) {
                request.part.delete()
                return Attempt.Done(FetchResult.Failed(MapFailure.INVALID_FILE, version))
            }
            Files.move(request.part.toPath(), request.target.toPath(), StandardCopyOption.ATOMIC_MOVE)
            return Attempt.Done(FetchResult.Complete(version, written))
        }
    }

    companion object {
        /** The client map files are fetched with. Not the API's: the driver's token must never reach the file's host. */
        fun client(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build()
    }
}

/** Room left free on top of the file, so the download never fills the phone. */
internal const val SPACE_TO_SPARE = 50L * 1024 * 1024

private const val BUFFER_BYTES = 64 * 1024
private const val HTTP_OK = 200
private const val HTTP_PARTIAL_CONTENT = 206
private const val HTTP_NOT_MODIFIED = 304
private const val HTTP_RANGE_NOT_SATISFIABLE = 416

/** A PMTiles version 3 file starts with these 8 bytes. */
private val MAGIC = "PMTiles".toByteArray(Charsets.US_ASCII) + 3.toByte()

/** The PMTiles version 3 header's length. */
internal const val HEADER_BYTES = 127

private const val TILE_TYPE_VECTOR = 1

/** `bytes first-last/total` as (first, total); null for anything else, including an unknown total. */
private fun contentRange(header: String?): Pair<Long, Long>? {
    val match = header?.let { Regex("""bytes (\d+)-(\d+)/(\d+)""").matchEntire(it.trim()) } ?: return null
    return match.groupValues[1].toLong() to match.groupValues[3].toLong()
}

/**
 * True when [file] is a PMTiles version 3 vector map whose header accounts for exactly its
 * length: the root directory, metadata, leaf directories and tile data all lie inside it, and the
 * last of them ends where the file does. A file cut short anywhere fails this.
 */
internal fun headerMatchesLength(file: File): Boolean {
    val length = file.length()
    if (length < HEADER_BYTES) return false
    val header = ByteArray(HEADER_BYTES)
    RandomAccessFile(file, "r").use { it.readFully(header) }
    if (!header.copyOfRange(0, MAGIC.size).contentEquals(MAGIC)) return false
    val bytes = ByteBuffer.wrap(header).order(ByteOrder.LITTLE_ENDIAN)
    if (bytes.get(99).toInt() != TILE_TYPE_VECTOR) return false
    // Root directory, metadata, leaf directories, tile data: each an offset and a length.
    val sections = listOf(8, 24, 40, 56).map { at -> bytes.getLong(at) to bytes.getLong(at + 8) }
    if (sections.any { (offset, size) -> offset < 0 || size < 0 }) return false
    if (sections.first().first < HEADER_BYTES) return false
    return sections.maxOf { (offset, size) -> offset + size } == length
}

/** True when the next bytes are [prefix]. Reads ahead without consuming them. */
private fun BufferedSource.startsWith(prefix: ByteArray): Boolean =
    request(prefix.size.toLong()) && peek().readByteArray(prefix.size.toLong()).contentEquals(prefix)

/** Runs the call without holding a thread, and cancels it if the coroutine is cancelled. */
private suspend fun Call.await(): Response = suspendCancellableCoroutine { continuation ->
    continuation.invokeOnCancellation { cancel() }
    enqueue(object : Callback {
        override fun onResponse(call: Call, response: Response) {
            continuation.resume(response) { _, value, _ -> value.close() }
        }

        override fun onFailure(call: Call, e: IOException) {
            continuation.resumeWithException(e)
        }
    })
}
