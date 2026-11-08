package org.onebusaway.vehicletracker.data

import android.content.Context
import android.content.pm.ApplicationInfo
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** The hosts the debug build's network security config lets plain http reach (`src/debug/res/xml`). */
private val DEBUG_HTTP_HOSTS = setOf("10.0.2.2", "localhost", "127.0.0.1")

/**
 * An address the driver didn't type: the agency's managed configuration, the build default or a setup link. It must be
 * https, or in a debug build http to a host the debug network config allows, and carry no user info:
 * https://tracker.example.org@evil.example goes to evil.example, and nobody reading it would think so.
 *
 * Unlike a typed address, it gets no https:// added: the agency wrote it, and one that is wrong is reported, not
 * guessed at.
 */
internal fun presetServerOf(raw: String, debuggable: Boolean): HttpUrl? {
    val url = raw.toHttpUrlOrNull() ?: return null
    if (url.username.isNotEmpty() || url.password.isNotEmpty()) return null
    return when {
        url.isHttps -> url
        debuggable && url.host in DEBUG_HTTP_HOSTS -> url
        else -> null
    }
}

/**
 * Whether [a] and [b] are the same server, read as addresses: "https://tracker.example.org" is
 * "https://tracker.example.org/". A sign-in saves the address it went to, and the builds before #188 sent it as typed.
 * False when either is null.
 */
internal fun sameServer(a: String?, b: String?): Boolean {
    if (a == null || b == null) return false
    val urlA = a.trim().toHttpUrlOrNull()
    val urlB = b.trim().toHttpUrlOrNull()
    return if (urlA != null && urlB != null) urlA == urlB else a.trim() == b.trim()
}

/** A debug build, whose network config lets plain http reach the hosts above. */
internal val Context.isDebuggable: Boolean
    get() = applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
