package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createEmptyComposeRule
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.datastore.preferences.core.edit
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.MainActivity
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.sessionDataStore
import org.onebusaway.vehicletracker.data.tripStateDataStore
import java.io.Closeable
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

private const val TIMEOUT_SEC = 15L

/**
 * Signing in, in the app itself with its own stores, when the server answers only after a rotation has recreated the
 * screen that asked.
 */
@RunWith(AndroidJUnit4::class)
class SignInRotationTest {
    @get:Rule val compose = createEmptyComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val server = HeldServer()

    @Before fun setUp() = runBlocking { forgetAll() }

    @After fun tearDown() = runBlocking {
        server.close()
        forgetAll()
    }

    private suspend fun forgetAll() {
        context.sessionDataStore.edit { it.clear() }
        context.tripStateDataStore.edit { it.clear() }
    }

    private fun shows(id: Int) = compose.onAllNodes(hasText(context.getString(id))).fetchSemanticsNodes().isNotEmpty()

    private fun field(label: Int) =
        compose.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.EditableText) and hasText(context.getString(label)))

    @Test
    fun aSignInAnsweredAfterARotation_goesOnToTheVehicleStep() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            compose.waitUntil(TIMEOUT_SEC * 1000) { shows(R.string.login_button) }
            field(R.string.login_server_url_label).performTextReplacement(server.url)
            field(R.string.login_email_label).performTextReplacement("driver@example.com")
            field(R.string.login_password_label).performTextReplacement("driverpass123")
            compose.onNode(hasText(context.getString(R.string.login_button)) and hasClickAction()).performScrollTo().performClick()
            assertTrue("the sign-in reached the server", server.signInArrived.await(TIMEOUT_SEC, TimeUnit.SECONDS))

            scenario.recreate()
            server.answerSignIn()

            compose.waitUntil(TIMEOUT_SEC * 1000) { shows(R.string.vehicles_title) }
        }
    }
}

/** A server on this phone that holds its answer to the sign-in until [answerSignIn], and has no vehicles to give. */
private class HeldServer : Closeable {
    private val socket = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    val url = "http://127.0.0.1:${socket.localPort}/"
    val signInArrived = CountDownLatch(1)
    private val signInAnswered = CountDownLatch(1)

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

    fun answerSignIn() = signInAnswered.countDown()

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
        val (status, body) = when {
            path.endsWith("/api/v1/auth/login") -> {
                signInArrived.countDown()
                signInAnswered.await(TIMEOUT_SEC, TimeUnit.SECONDS)
                "200 OK" to """{"access_token":"jwt","refresh_token":"r1","expires_in":900}"""
            }
            path.endsWith("/api/v1/vehicles") -> "200 OK" to "[]"
            else -> "404 Not Found" to """{"error":"not found"}"""
        }
        val bytes = body.toByteArray()
        client.getOutputStream().run {
            write("HTTP/1.1 $status\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
            write(bytes)
            flush()
        }
    }

    override fun close() = socket.close()
}
