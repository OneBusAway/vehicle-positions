package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import java.text.Bidi
import javax.xml.parsers.DocumentBuilderFactory

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

    /** A string in `values-[language]` (`values` for none) as the app reads it, its \u escapes and quotes undone. */
    private fun resource(language: String, name: String): String {
        val file = File("src/main/res/${if (language.isEmpty()) "values" else "values-$language"}/strings.xml")
        val strings = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file).getElementsByTagName("string")
        val text = (0 until strings.length).map { strings.item(it) as Element }.first { it.getAttribute("name") == name }.textContent
        return Regex("""\\u([0-9a-fA-F]{4})|\\(.)""").replace(text) { m ->
            m.groups[1]?.let { it.value.toInt(16).toChar().toString() } ?: m.groupValues[2]
        }
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

    @Test fun `a name that starts in Latin and ends in Arabic keeps the time after it in a left-to-right sentence`() {
        val name = "Metro محطة"
        val bare = "Next %s · %s".format(name, "7:18 AM")
        val set = "Next %s · %s".format(bidiIsolated(name, rtl = false), "7:18 AM")

        val bareShown = shown(bare, rtl = false)
        assertFalse("without it, it shows as $bareShown", bareShown.endsWith("· 7:18 AM"))
        assertTrue(shown(set, rtl = false), shown(set, rtl = false).endsWith("· 7:18 AM"))
    }

    @Test fun `a name that starts in Arabic and ends in Latin is set apart in a right-to-left sentence`() {
        assertEquals("\u2068محطة Metro\u2069", bidiIsolated("محطة Metro", rtl = true))
        assertEquals("محطة 5", bidiIsolated("محطة 5", rtl = true))
    }

    @Test fun `the https scheme keeps its colon and slashes in a right-to-left sentence`() {
        for (language in listOf("ar", "fa")) {
            val sentence = resource(language, "login_error_https_required")
            val bare = shown(sentence.format("https://"), rtl = true)
            val set = shown(sentence.format(bidiIsolated("https://", rtl = true)), rtl = true)

            assertFalse("$language without it shows $bare", "https://" in bare)
            assertTrue("$language shows $set", "https://" in set)
        }
    }

    @Test fun `the example address and the console's scheme read in order in a right-to-left language`() {
        for (language in listOf("ar", "fa")) {
            val example = resource(language, "login_error_invalid_address").format(bidiIsolated("https://tracker.example.org", rtl = true))
            assertTrue(shown(example, rtl = true), "https://tracker.example.org" in shown(example, rtl = true))

            // The console shows this one as it is, so its isolates are written in the translation.
            val console = resource(language, "restriction_server_url_description")
            val bare = shown(console.filterNot { it in "\u2068\u2069" }, rtl = true)
            assertFalse("$language without them shows $bare", "https://" in bare)
            assertTrue(shown(console, rtl = true), "https://" in shown(console, rtl = true))
        }
    }

    @Test fun `the English with an address put in reads as it did`() {
        val example = resource("", "login_server_placeholder")
        assertEquals("https://tracker.example.org", example)
        assertEquals("Enter an address like https://tracker.example.org",
            resource("", "login_error_invalid_address").format(bidiIsolated(example, rtl = false)))
        assertEquals("Use an https:// address", resource("", "login_error_https_required").format(bidiIsolated("https://", rtl = false)))
    }
}
