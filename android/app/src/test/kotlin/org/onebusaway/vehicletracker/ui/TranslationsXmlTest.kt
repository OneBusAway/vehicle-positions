package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Each translation in `src/main/res/values-<language>` against the English in `values/`: the app fills a string's
 * values in by position, so a translation that drops one, adds one or changes its type shows the wrong thing or crashes.
 * Lint's StringFormatMatches catches some of this; this holds every string and every plural form to it.
 */
class TranslationsXmlTest {
    private val res = File("src/main/res")

    /** A string's text, or a plural's forms by quantity, keyed by name, leaving out what isn't translated. */
    private fun read(file: File): Map<String, Map<String, String>> {
        val resources = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file).documentElement
        val children = resources.childNodes
        return buildMap {
            for (i in 0 until children.length) {
                val node = children.item(i) as? Element ?: continue
                if (node.getAttribute("translatable") == "false") continue
                when (node.tagName) {
                    "string" -> put(node.getAttribute("name"), mapOf("" to node.textContent))
                    "plurals" -> {
                        val items = node.getElementsByTagName("item")
                        put(node.getAttribute("name"), (0 until items.length).associate { j ->
                            val item = items.item(j) as Element
                            item.getAttribute("quantity") to item.textContent
                        })
                    }
                }
            }
        }
    }

    private val english by lazy { read(File(res, "values/strings.xml")) }

    private val translations by lazy {
        res.listFiles { dir -> dir.name.startsWith("values-") && File(dir, "strings.xml").isFile }.orEmpty()
            .sortedBy { it.name }
            .associate { it.name.removePrefix("values-") to read(File(it, "strings.xml")) }
    }

    // A format placeholder such as %1$s, %d or %1$,d. A doubled %% is a percent sign.
    private val placeholder = Regex("""%(\d+\$)?[-#+ 0,(]*\d*(\.\d+)?[a-zA-Z]""")

    private fun placeholders(text: String) = placeholder.findAll(text.replace("%%", "")).map { it.value }.sorted().toList()

    // An integer placeholder, %d, %1$d or %1$,d. In a plural it is the count, the number getQuantityString picks the
    // form by: every plural in the app has exactly one, and one with two would fail below until it said which counts.
    private val integer = Regex("""%(\d+\$)?[-#+ 0,(]*\d*d""")

    /**
     * Whether a translated form at [quantity] takes the right values of its English, [wanted]: a string or a plural's
     * "other" all of them, and a plural's other forms all of them or all but the count, as in "one report".
     */
    private fun takesRightValues(quantity: String, have: List<String>, wanted: List<String>): Boolean {
        if (have == wanted) return true
        if (quantity == "" || quantity == "other") return false
        val counts = wanted.filter(integer::matches).distinct()
        check(counts.size <= 1) { "a plural with more than one integer value, so no count to leave out: $wanted" }
        return counts.isNotEmpty() && have == wanted - counts.single()
    }

    @Test fun `the app has translations to check`() {
        assertTrue("languages found in src/main/res: ${translations.keys}", "hi" in translations)
    }

    @Test fun `every translation takes the same values as its English`() {
        val wrong = mutableListOf<String>()
        for ((language, strings) in translations) {
            for ((name, forms) in strings) {
                val source = english[name] ?: continue
                // A plural's English "other" takes every value.
                val wanted = placeholders(source[""] ?: source.getValue("other"))
                for ((quantity, text) in forms) {
                    val have = placeholders(text)
                    if (!takesRightValues(quantity, have, wanted)) {
                        wrong += "$language $name${if (quantity.isEmpty()) "" else " ($quantity)"}: $have, English $wanted"
                    }
                }
            }
        }

        assertEquals("translations whose values differ from the English", emptyList<String>(), wrong)
    }

    @Test fun `a plural form may leave out its count, and nothing else`() {
        val english = placeholders(
            "Reports stop, and route %1\$s's %2\$s run is marked complete. %3\$d reports were sent.",
        )

        assertTrue(
            "the count left out of a \"one\" form",
            takesRightValues("one", placeholders("Route %1\$s's %2\$s run is complete. One report was sent."), english),
        )
        assertFalse(
            "the route left out of a \"one\" form",
            takesRightValues("one", placeholders("The %2\$s run is complete. %3\$d report was sent."), english),
        )
        assertFalse(
            "the count left out of \"other\"",
            takesRightValues("other", placeholders("Route %1\$s's %2\$s run is complete."), english),
        )
        assertFalse(
            "a value the English lacks",
            takesRightValues("one", placeholders("Route %1\$s's %2\$s run, %4\$s. %3\$d report was sent."), english),
        )
    }
}
