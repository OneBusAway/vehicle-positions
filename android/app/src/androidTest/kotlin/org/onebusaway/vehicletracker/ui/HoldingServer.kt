package org.onebusaway.vehicletracker.ui

import java.io.Closeable
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

internal const val HOLD_TIMEOUT_SEC = 15L

/**
 * A server on this phone that answers each request with [reply] for its path, as a status line and a JSON body. Its
 * answer to the request whose path ends with [held] waits for [release], so a test can recreate the screen that asked
 * while the request is out.
 */
internal class HoldingServer(
    private val held: String,
    private val reply: (path: String) -> Pair<String, String>,
) : Closeable {
    private val socket = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    val url = "http://127.0.0.1:${socket.localPort}/"

    /** Counted down once the held request has arrived. */
    val heldArrived = CountDownLatch(1)
    private val released = CountDownLatch(1)

    init {
        thread(isDaemon = true) {
            while (true) {
                val client = try {
                    socket.accept()
                } catch (e: IOException) {
                    break
                }
                thread(isDaemon = true) { client.use(::answer) }
            }
        }
    }

    fun release() = released.countDown()

    private fun answer(client: Socket) {
        val request = client.getInputStream().bufferedReader()
        val path = request.readLine()?.split(' ')?.getOrNull(1) ?: return
        var length = 0
        while (true) {
            val header = request.readLine() ?: return
            if (header.isEmpty()) break
            if (header.startsWith("Content-Length:", ignoreCase = true)) length = header.substringAfter(':').trim().toInt()
        }
        repeat(length) { request.read() }
        if (path.endsWith(held)) {
            heldArrived.countDown()
            released.await(HOLD_TIMEOUT_SEC, TimeUnit.SECONDS)
        }
        val (status, body) = reply(path)
        val bytes = body.toByteArray()
        client.getOutputStream().run {
            write("HTTP/1.1 $status\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
            write(bytes)
            flush()
        }
    }

    override fun close() = socket.close()
}
