package org.onebusaway.vehicletracker.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.LayoutDirection

private const val FIRST_STRONG_ISOLATE = '\u2068'
private const val POP_DIRECTIONAL_ISOLATE = '\u2069'

/**
 * [text], a name or number from the agency's feed or from the driver (a route's short name, a headsign, a stop, a
 * vehicle or run number, a server's address), set apart to keep its own direction inside a sentence that reads the
 * other way. Without it, an Arabic or Persian sentence shows a headsign like "12 Mile Rd" as "Mile Rd 12", and an
 * English one pulls the time after an Arabic stop name into the name. A name that already reads the sentence's way
 * comes back as it is, so English text with English names never changes.
 */
internal fun bidiIsolated(text: String, rtl: Boolean): String {
    val readsRtl = firstStrongIsRtl(text)
    val fits = if (rtl) readsRtl == true else readsRtl != true
    return if (fits) text else "$FIRST_STRONG_ISOLATE$text$POP_DIRECTIONAL_ISOLATE"
}

/** [text] set apart as [bidiIsolated] says, for a sentence in the direction the screen is laid out in. */
@Composable
@ReadOnlyComposable
internal fun isolated(text: String): String = bidiIsolated(text, LocalLayoutDirection.current == LayoutDirection.Rtl)

/** Whether [text]'s first letter with a direction reads right to left, or null when none has one, as in "4317". */
private fun firstStrongIsRtl(text: String): Boolean? {
    var i = 0
    while (i < text.length) {
        val codePoint = text.codePointAt(i)
        when (Character.getDirectionality(codePoint)) {
            Character.DIRECTIONALITY_LEFT_TO_RIGHT -> return false
            Character.DIRECTIONALITY_RIGHT_TO_LEFT, Character.DIRECTIONALITY_RIGHT_TO_LEFT_ARABIC -> return true
        }
        i += Character.charCount(codePoint)
    }
    return null
}
