package org.onebusaway.vehicletracker.data

import android.util.Log

/** One warning as Share diagnostics may send it: when, the tag it was logged under, and its words, scrubbed. */
data class RecentProblem(val atEpochMs: Long, val tag: String, val message: String)

/**
 * The newest [capacity] warnings the app logged, oldest first, for Share diagnostics. They live in this process's memory
 * only: never written to disk, and gone when the app stops. Each is scrubbed as it comes in (see [scrubbed]), so what is
 * kept is already fit to send. Safe to call from any thread.
 */
class RecentProblems(private val capacity: Int = CAPACITY, private val clock: () -> Long = System::currentTimeMillis) {
    private val problems = ArrayDeque<RecentProblem>(capacity)

    fun add(tag: String, message: String, error: Throwable? = null) {
        val words = if (error == null) message else "$message (${error.javaClass.simpleName}${error.message?.let { ": $it" }.orEmpty()})"
        // Scrubbed before it is cut, so no secret is cut down to a piece too short to spot. A reply quoted in an
        // exception can run long, so only its start is read.
        val line = cut(scrubbed(words.take(READ_LIMIT)).replace(LINE_BREAKS, " "))
        val problem = RecentProblem(clock(), tag, line)
        synchronized(problems) {
            if (problems.size == capacity) problems.removeFirst()
            problems.addLast(problem)
        }
    }

    fun snapshot(): List<RecentProblem> = synchronized(problems) { problems.toList() }

    companion object {
        const val CAPACITY = 50

        /** A line's length at most, as on iOS: a system error's message can run to hundreds of characters. */
        const val LINE_LIMIT = 300
        private const val READ_LIMIT = 2_000
        private val LINE_BREAKS = Regex("\\s*[\\r\\n]+\\s*")

        /** [text] at most [LINE_LIMIT] long, with an ellipsis where it was cut. */
        fun cut(text: String): String = if (text.length <= LINE_LIMIT) text else text.take(LINE_LIMIT - 1) + "…"
    }
}

/**
 * Where the app's warnings go: to logcat, as `Log.w` sent them, and to [recent] for Share diagnostics. Reading logcat
 * back would bring up Android's "access all device logs?" prompt, so the app keeps its own.
 */
object AppLog {
    val recent = RecentProblems()

    fun w(tag: String, message: String, error: Throwable? = null) {
        if (error == null) Log.w(tag, message) else Log.w(tag, message, error)
        recent.add(tag, message, error)
    }
}

private val URL = Regex("""[A-Za-z][A-Za-z0-9+.\-]*://[^\s"'<>(){}\[\],;]+""")
private val AUTHORIZATION = Regex("""(?i)(authorization["']?\s*[:=]\s*)["']?(?:(?:bearer|basic|token)\s+)?[^\s"',;]+["']?""")
private val BEARER = Regex("""(?i)\bbearer\s+[A-Za-z0-9._~+/=\-]+""")
private val SECRET_FIELD = Regex(
    """(?i)(["']?\b(?:access_token|refresh_token|id_token|token|password|passwd|secret|client_secret|api_key|apikey)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s&,;"'}]+)""",
)
private val JWT = Regex("""eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+(?:\.[A-Za-z0-9_\-]*)?""")
private val EMAIL = Regex("""[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}""")
private val LONG_RUN = Regex("""[A-Za-z0-9+/_=\-]{20,}""")
private val DECIMAL = Regex("""-?\d+(?:\.\d+)+""")

/**
 * [text] with what must not leave the phone taken out, matching iOS's Share diagnostics. A URL keeps only its scheme and
 * host: its user and password, and a path or query naming a vehicle, a trip or a token, go. Authorization values,
 * bearer and JSON web tokens, and fields named as tokens, passwords, secrets or keys go. So do emails, any long run of
 * letters and digits mixed (a refresh token, a key: the app's own words have no digits in them), and degrees with
 * three or more decimals (a position). Addresses and versions, with more dots, stay.
 */
internal fun scrubbed(text: String): String = text
    .replace(URL) { match ->
        val scheme = match.value.substringBefore("://")
        val authority = match.value.substringAfter("://").substringBefore('/').substringBefore('?').substringBefore('#')
        val host = authority.substringAfterLast('@').substringBefore(':')
        if (host.isEmpty()) "<url>" else "$scheme://$host"
    }
    .replace(AUTHORIZATION) { "${it.groupValues[1]}<hidden>" }
    .replace(BEARER, "Bearer <token>")
    .replace(SECRET_FIELD) { "${it.groupValues[1]}<hidden>" }
    .replace(JWT, "<token>")
    .replace(EMAIL, "<email>")
    .replace(LONG_RUN) { run -> if (run.value.any(Char::isDigit) && run.value.any(Char::isLetter)) "<token>" else run.value }
    .replace(DECIMAL) { number ->
        val parts = number.value.split('.')
        val degrees = parts.size == 2 && parts[0].count(Char::isDigit) <= 3 && parts[1].length >= 3
        if (degrees) "<number>" else number.value
    }
