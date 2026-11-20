package org.onebusaway.vehicletracker.ui.help

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.RecentProblem
import org.onebusaway.vehicletracker.data.Session
import org.onebusaway.vehicletracker.ui.permissions.Grants
import java.time.Instant
import java.time.ZoneOffset

/** What Share diagnostics sends, and that nothing private in the session gets into it. */
class DiagnosticsTest {
    private val india = ZoneOffset.ofHoursMinutes(5, 30)

    private fun text(device: DeviceFacts, session: Session, problems: List<RecentProblem> = emptyList()) =
        diagnosticsText(device, session, problems, india)

    private val device = DeviceFacts(
        madeAt = "2026-11-16 18:25 +05:30",
        version = "0.1.0 (1)",
        androidRelease = "16",
        sdk = 36,
        phone = "Google Pixel 5",
        grants = Grants(sdk = 36, fine = true, coarse = true, background = false, notifications = true, batteryExempt = false),
        notificationsOn = true,
        locationServicesOn = true,
    )

    private val jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI0MiIsImVtYWlsIjoibWFyaWFAbWV0cm8uZXhhbXBsZSJ9.c2lnbmF0dXJl"

    private fun signedIn(serverUrl: String) = Session(
        serverUrl = serverUrl,
        token = jwt,
        issuedAtEpochSec = 1_700_000_000L,
        refreshToken = "r1-7f3a9c2e5b8d4f6a1c0e9b7d",
        accessExpiresAtEpochSec = 1_700_000_900L,
        email = "maria@metro.example",
    )

    @Test fun `it names the build, the phone, the server and each of the checklist's rows`() {
        val text = text(device, signedIn("https://tracker.example.org"))

        assertEquals(
            """
            Vehicle Tracker diagnostics
            Made: 2026-11-16 18:25 +05:30
            App: 0.1.0 (1)
            Android: 16 (API 36)
            Phone: Google Pixel 5
            Server: tracker.example.org
            Location: allowed
            Precise location: allowed
            All the time: not allowed
            Notifications: allowed
            Battery: not allowed
            Location services: on
            Recent problems
            None since the app started
            """.trimIndent(),
            text,
        )
    }

    @Test fun `a permission Android has not given reads as not allowed, and location services off as off`() {
        val text = text(
            device.copy(
                grants = Grants(
                    sdk = 36,
                    fine = false,
                    coarse = true,
                    background = false,
                    notifications = false,
                    batteryExempt = true,
                ),
                notificationsOn = false,
                locationServicesOn = false,
            ),
            signedIn("https://tracker.example.org"),
        )

        assertTrue(text, text.contains("Location: allowed\nPrecise location: not allowed\nAll the time: not allowed\nNotifications: not allowed\nBattery: allowed\nLocation services: off"))
    }

    @Test fun `below Android 13, notifications turned off in Settings read as not allowed`() {
        // There is no permission to refuse on Android 12, so the checklist's grants count notifications as allowed.
        val android12 = device.copy(
            androidRelease = "12",
            sdk = 31,
            grants = Grants(
                sdk = 31,
                fine = true,
                coarse = true,
                background = true,
                notifications = false,
                batteryExempt = true,
            ),
            notificationsOn = false,
        )

        val text = text(android12, signedIn("https://tracker.example.org"))

        assertTrue(text, text.contains("\nNotifications: not allowed\n"))
    }

    @Test fun `of the session only the host gets out, not the email, a token, a password, a path or a query`() {
        // A server address can carry a user and password, a path naming a vehicle and a trip, and a token in its query.
        val session = signedIn("https://maria:hunter2@tracker.example.org:8443/api/v1/vehicles/4317/trips/88123?token=$jwt&lat=47.6097")

        val text = text(device, session)

        assertTrue(text, text.contains("Server: tracker.example.org:8443\n"))
        for (secret in listOf("maria", "hunter2", "metro.example", jwt.take(12), "r1-7f3a", "4317", "88123", "47.6", "/api/v1", "token")) {
            assertFalse("\"$secret\" got through:\n$text", text.contains(secret, ignoreCase = true))
        }
    }

    @Test fun `recent problems come last, oldest first, each with its time and tag`() {
        val problems = listOf(
            RecentProblem(Instant.parse("2026-11-20T04:30:05Z").toEpochMilli(), "HistoryUploader", "Keeping queued fixes after network failure (IOException: timeout)"),
            RecentProblem(Instant.parse("2026-11-20T04:31:00Z").toEpochMilli(), "MapRepository", "Map file not downloaded: NETWORK"),
        )

        val text = text(device, signedIn("https://tracker.example.org"), problems)

        assertTrue(
            text,
            text.endsWith(
                """
                Location services: on
                Recent problems
                2026-11-20 10:00:05 +05:30 HistoryUploader: Keeping queued fixes after network failure (IOException: timeout)
                2026-11-20 10:01:00 +05:30 MapRepository: Map file not downloaded: NETWORK
                """.trimIndent(),
            ),
        )
        assertFalse(text, text.contains("None since the app started"))
    }

    @Test fun `with no problems since the app started it says so`() {
        val text = text(device, signedIn("https://tracker.example.org"))

        assertTrue(text, text.endsWith("\nRecent problems\nNone since the app started"))
    }

    @Test fun `a problem's line is cut short at the limit, time and tag included`() {
        val long = RecentProblem(Instant.parse("2026-11-20T04:30:05Z").toEpochMilli(), "TripReporter", "y".repeat(300))

        val line = text(device, signedIn("https://tracker.example.org"), listOf(long)).lines().last()

        assertEquals(300, line.length)
        assertTrue(line, line.startsWith("2026-11-20 10:00:05 +05:30 TripReporter: yyy") && line.endsWith("…"))
    }

    @Test fun `with nobody signed in to a server it says none`() {
        val text = text(device, Session(serverUrl = null, token = null, issuedAtEpochSec = null))

        assertTrue(text, text.contains("Server: none\n"))
    }
}
