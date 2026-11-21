package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.text.Bidi

/** Names and numbers from the feed keep their own order inside a sentence that reads the other way. */
class BidiTextTest {
    /** [text] in the order a screen shows it, left to right, with the invisible direction marks left out. */
    private fun shown(text: String, rtl: Boolean): String {
        val bidi = Bidi(text, if (rtl) Bidi.DIRECTION_RIGHT_TO_LEFT else Bidi.DIRECTION_LEFT_TO_RIGHT)
        val runs = (0 until bidi.runCount).map { i ->
            val run = text.substring(bidi.getRunStart(i), bidi.getRunLimit(i))
            if (bidi.getRunLevel(i) % 2 == 1) run.reversed() else run
        }
        val levels = ByteArray(bidi.runCount) { bidi.getRunLevel(it).toByte() }
        val order: Array<Any> = runs.toTypedArray()
        Bidi.reorderVisually(levels, 0, order, 0, order.size)
        return order.joinToString("").filterNot { it in "\u2068\u2069" }
    }

    @Test fun `a name that reads the sentence's way comes back as it is`() {
        for (name in listOf("7A", "4317", "Downtown Seattle", "tracker.example.org", "599-20")) {
            assertEquals(name, bidiIsolated(name, rtl = false))
        }
        assertEquals("محطة الحرية", bidiIsolated("محطة الحرية", rtl = true))
    }

    @Test fun `a Latin headsign keeps its order in a right-to-left sentence`() {
        val headsign = "12 Mile Rd"
        val bare = "إلى %s".format(headsign)
        val set = "إلى %s".format(bidiIsolated(headsign, rtl = true))

        assertFalse("without it, it shows as ${shown(bare, rtl = true)}", headsign in shown(bare, rtl = true))
        assertTrue(shown(set, rtl = true), headsign in shown(set, rtl = true))
    }

    @Test fun `a number in a right-to-left sentence is set apart too`() {
        assertEquals("\u20684317\u2069", bidiIsolated("4317", rtl = true))
    }

    @Test fun `an Arabic stop name keeps the time after it in a left-to-right sentence`() {
        val bare = "Next %s · %s".format("محطة الحرية", "7:18 AM")
        val set = "Next %s · %s".format(bidiIsolated("محطة الحرية", rtl = false), "7:18 AM")

        val bareShown = shown(bare, rtl = false)
        assertFalse("without it, it shows as $bareShown", bareShown.endsWith("· 7:18 AM"))
        assertTrue(shown(set, rtl = false), shown(set, rtl = false).endsWith("· 7:18 AM"))
    }
}
