package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Comment
import org.w3c.dom.Element
import org.w3c.dom.Node
import org.w3c.dom.Text
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The English strings as committed, read from `src/main/res`: translators see one string at a time, so what a value in
 * it stands for has to be written beside it, and a sentence naming a button has to take the button's own words.
 */
class StringsXmlTest {
    private class Entry(val element: Element, val note: String?) {
        val name: String get() = element.getAttribute("name")
        val text: String get() = element.textContent
    }

    /**
     * The translatable strings and plurals, each with its note: the comment just before it with nothing but space
     * between, as `cmd/l10n` reads it.
     */
    private val entries: List<Entry> by lazy {
        val resources = DocumentBuilderFactory.newInstance().newDocumentBuilder()
            .parse(File("src/main/res/values/strings.xml"))
            .documentElement
        val out = mutableListOf<Entry>()
        var note: String? = null
        var node: Node? = resources.firstChild
        while (node != null) {
            when (node) {
                is Comment -> note = node.data.trim()
                is Text -> if (node.data.isNotBlank()) note = null
                is Element -> {
                    val translatable = node.getAttribute("translatable") != "false"
                    if (translatable && (node.tagName == "string" || node.tagName == "plurals")) out += Entry(node, note)
                    note = null
                }
            }
            node = node.nextSibling
        }
        out
    }

    private fun entry(name: String) = entries.single { it.name == name }

    // A format placeholder such as %1$s, %d or %1$,d. A doubled %% is a percent sign.
    private val placeholder = Regex("""%(\d+\$)?[-#+ 0,(]*\d*(\.\d+)?[a-zA-Z]""")

    private fun takesValue(entry: Entry) =
        entry.element.tagName == "plurals" || placeholder.containsMatchIn(entry.text.replace("%%", ""))

    @Test fun `every string that takes a value, and every plural, says what it is in a note`() {
        val read = entries.filter(::takesValue)
        assertTrue("the file has strings that take values", read.size > 20)

        assertEquals("strings with no note just before them", emptyList<String>(), read.filter { it.note.isNullOrEmpty() }.map { it.name })
    }

    @Test fun `the run board's tip takes the start button's words rather than spelling them`() {
        val tip = entry("tip_run_board_text")

        assertEquals("one value, the button's name", listOf("%1\$s"), placeholder.findAll(tip.text).map { it.value }.toList())
        assertFalse("it names no button itself", tip.text.contains(entry("runs_start_confirm").text))
        assertTrue("its note says which string fills it", tip.note.orEmpty().contains("runs_start_confirm"))
    }
}
