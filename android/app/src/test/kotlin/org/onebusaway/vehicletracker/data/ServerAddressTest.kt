package org.onebusaway.vehicletracker.data

import org.junit.Assert.assertEquals
import org.junit.Test

class ServerAddressTest {
    @Test fun `a preset server must be https, or http to a debug host in a debug build, with no user info`() {
        // The address it gives, in a release build and in a debug build; null when refused.
        val cases = listOf(
            Triple("https://tracker.example.org", "https://tracker.example.org/", "https://tracker.example.org/"),
            Triple("https://tracker.example.org/vp", "https://tracker.example.org/vp", "https://tracker.example.org/vp"),
            Triple("https://tracker.example.org:8443", "https://tracker.example.org:8443/", "https://tracker.example.org:8443/"),
            Triple("http://tracker.example.org", null, null),
            Triple("http://localhost:8080", null, "http://localhost:8080/"),
            Triple("http://10.0.2.2:8080", null, "http://10.0.2.2:8080/"),
            Triple("http://127.0.0.1:8080", null, "http://127.0.0.1:8080/"),
            Triple("http://192.168.1.5", null, null),
            Triple("http://printer.local", null, null),
            Triple("https://tracker.example.org@evil.example", null, null),
            Triple("https://a:b@tracker.example.org", null, null),
            Triple("http://localhost@evil.example", null, null),
            Triple("tracker.example.org", null, null),
            Triple("ftp://x", null, null),
            Triple("javascript:alert(1)", null, null),
            Triple("https://", null, null),
            Triple("", null, null),
        )
        for ((raw, release, debug) in cases) {
            assertEquals("'$raw' in a release build", release, presetServerOf(raw, debuggable = false)?.toString())
            assertEquals("'$raw' in a debug build", debug, presetServerOf(raw, debuggable = true)?.toString())
        }
    }

    @Test fun `the managed server is unset, a server, or one the app can't use`() {
        val cases = listOf(
            null to ManagedServer(url = null, invalid = false),
            "" to ManagedServer(url = null, invalid = false),
            "  " to ManagedServer(url = null, invalid = false),
            "https://tracker.example.org" to ManagedServer(url = "https://tracker.example.org/", invalid = false),
            "http://tracker.example.org" to ManagedServer(url = null, invalid = true),
            "tracker.example.org" to ManagedServer(url = null, invalid = true),
            "https://tracker.example.org@evil.example" to ManagedServer(url = null, invalid = true),
        )
        for ((raw, managed) in cases) assertEquals("for '$raw'", managed, managedServerOf(raw, debuggable = false))
    }
}
