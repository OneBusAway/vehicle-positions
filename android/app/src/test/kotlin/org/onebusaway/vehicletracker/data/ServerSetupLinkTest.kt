package org.onebusaway.vehicletracker.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.onebusaway.vehicletracker.data.ServerSetupLink.Decision

/** The same links as iOS's ServerSetupLinkTests, with Android's own rule for plain http: debug builds only. */
class ServerSetupLinkTest {
    /** Every byte but a letter or a digit percent-encoded, as iOS's test encodes them. */
    private fun link(server: String) = "obavehicletracker://setup?server=" +
        server.toByteArray().joinToString("") { byte ->
            val c = byte.toInt().toChar()
            if (c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9') "$c" else "%%%02X".format(byte.toInt() and 0xff)
        }

    @Test fun `a setup link gives its server`() {
        // The server it gives in a release build, and in a debug build.
        val cases = listOf(
            Triple(link("https://tracker.example.org"), "https://tracker.example.org/", "https://tracker.example.org/"),
            Triple(link("https://tracker.example.org/vp"), "https://tracker.example.org/vp", "https://tracker.example.org/vp"),
            Triple("obavehicletracker://setup?server=https://tracker.example.org/vp", "https://tracker.example.org/vp", "https://tracker.example.org/vp"),
            Triple("obavehicletracker://setup/?server=https%3A%2F%2Ftracker.example.org", "https://tracker.example.org/", "https://tracker.example.org/"),
            Triple(link("http://localhost:8080"), null, "http://localhost:8080/"),
            // iOS allows a .local host over http; Android's debug build allows only the hosts in its network config.
            Triple(link("http://printer.local"), null, null),
        )
        for ((link, release, debug) in cases) {
            assertEquals("$link in a release build", release, ServerSetupLink.parse(link, debuggable = false)?.toString())
            assertEquals("$link in a debug build", debug, ServerSetupLink.parse(link, debuggable = true)?.toString())
        }
    }

    @Test fun `anything else is not a setup link`() {
        val refused = listOf(
            "obavehicletracker://other?server=https%3A%2F%2Ftracker.example.org",
            "otherapp://setup?server=https%3A%2F%2Ftracker.example.org",
            "obavehicletracker://setup",
            "obavehicletracker://setup?",
            "obavehicletracker://setup?host=https%3A%2F%2Ftracker.example.org",
            "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org&server=https%3A%2F%2Fevil.example",
            "obavehicletracker://setup?server=https%3A%2F%2Ftracker.example.org&from=qr",
            "obavehicletracker://setup?server=",
            "obavehicletracker://setup?server",
            "obavehicletracker://setup?server=%zz",
            "obavehicletracker://setup?server=https://tracker.example.org/a b",
            link("http://tracker.example.org"),
            link("ftp://tracker.example.org"),
            link("javascript:alert(1)"),
            link("https://tracker.example.org@evil.example"),
            link("https://user:pw@tracker.example.org"),
            link("http://localhost@evil.example"),
            link("not a url"),
            link("http://printer.locals"),
            link("https://"),
            link("https://:8080"),
        )
        for (link in refused) {
            assertNull("$link in a release build", ServerSetupLink.parse(link, debuggable = false))
            assertNull("$link in a debug build", ServerSetupLink.parse(link, debuggable = true))
        }
    }

    private val valid = link("https://tracker.example.org")

    @Test fun `signed out and not managed asks, naming the host`() {
        assertEquals(
            Decision.Confirm("https://tracker.example.org/", "tracker.example.org"),
            ServerSetupLink.decide(valid, signedInTo = null, managed = false, debuggable = false),
        )
    }

    @Test fun `a look-alike host shows as punycode`() {
        val lookAlike = "https://gооgle.com" // Cyrillic o
        // As iOS's test sends it, with the letters as they are, and fully percent-encoded.
        val links = listOf("obavehicletracker://setup?server=https%3A%2F%2Fgооgle%2Ecom", link(lookAlike))
        for (link in links) {
            val decision = ServerSetupLink.decide(link, signedInTo = null, managed = false, debuggable = false)
            assertEquals(link, Decision.Confirm("https://xn--ggle-55da.com/", "xn--ggle-55da.com"), decision)
        }
    }

    @Test fun `a phone that belongs to a server refuses, naming its host`() {
        assertEquals(
            Decision.RefusedSignedIn("positions.example.org"),
            ServerSetupLink.decide(valid, signedInTo = "https://positions.example.org:8443/", managed = false, debuggable = false),
        )
        // One that doesn't know its server, which a trip restored from backup can be on.
        assertEquals(
            Decision.RefusedSignedIn(null),
            ServerSetupLink.decide(valid, signedInTo = "", managed = false, debuggable = false),
        )
        // Before anything else is looked at.
        assertEquals(
            Decision.RefusedSignedIn("positions.example.org"),
            ServerSetupLink.decide("not a link", signedInTo = "https://positions.example.org/", managed = true, debuggable = false),
        )
    }

    @Test fun `managed refuses even when signed out`() {
        assertEquals(Decision.RefusedManaged, ServerSetupLink.decide(valid, signedInTo = null, managed = true, debuggable = false))
    }

    @Test fun `a link with no usable server is invalid`() {
        val userInfo = link("https://tracker.example.org@evil.example")
        assertEquals(Decision.Invalid, ServerSetupLink.decide(userInfo, signedInTo = null, managed = false, debuggable = false))
    }
}
