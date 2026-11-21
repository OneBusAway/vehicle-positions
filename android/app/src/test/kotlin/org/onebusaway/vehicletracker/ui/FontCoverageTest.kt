package org.onebusaway.vehicletracker.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.theme.systemFontLanguages
import org.w3c.dom.Element
import java.awt.Font
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Every letter, mark and digit of each language's strings is in the bundled font, unless the language is set in the
 * system font ([systemFontLanguages]), so a line never mixes the two. A symbol the font has no glyph for, such as the
 * run list's →, comes from the system font on its own, as it does in English.
 */
class FontCoverageTest {
    private val res = File("src/main/res")

    private val font: Font = run {
        System.setProperty("java.awt.headless", "true")
        Font.createFont(Font.TRUETYPE_FONT, File(res, "font/google_sans_flex.ttf"))
    }

    /** Each values folder with strings, by the language its strings are in: "values" is English. */
    private val languages: Map<String, File> by lazy {
        res.listFiles { dir -> dir.name.startsWith("values") && File(dir, "strings.xml").isFile }.orEmpty()
            .associate { language(it.name) + " (" + it.name + ")" to File(it, "strings.xml") }
    }

    /** A values folder's language: "values-pt-rBR" is pt, "values-b+zh+Hans" zh. */
    private fun language(folder: String): String =
        if (folder == "values") "en" else folder.removePrefix("values-").removePrefix("b+").split('-', '+').first()

    private val escape = Regex("""\\u([0-9a-fA-F]{4})""")

    /** The text of every string and plural form to translate in [file], with its \u escapes undone. */
    private fun texts(file: File): List<String> {
        val nodes = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file).documentElement.childNodes
        val elements = (0 until nodes.length).mapNotNull { nodes.item(it) as? Element }
        return elements.filter { it.getAttribute("translatable") != "false" }
            .flatMap { element ->
                val items = element.getElementsByTagName("item")
                if (element.tagName == "plurals") (0 until items.length).map { items.item(it).textContent } else listOf(element.textContent)
            }
            .map { text -> escape.replace(text) { it.groupValues[1].toInt(16).toChar().toString() } }
    }

    private val markTypes = setOf(Character.NON_SPACING_MARK, Character.COMBINING_SPACING_MARK, Character.ENCLOSING_MARK)
        .map(Byte::toInt)

    /** Whether [codePoint] is part of a word or number, which must come from the same font as the rest of its line. */
    private fun inWords(codePoint: Int) = Character.isLetterOrDigit(codePoint) || Character.getType(codePoint) in markTypes

    @Test fun `the app's languages are found`() {
        assertTrue("found: ${languages.keys}", languages.keys.any { it.startsWith("en ") } && languages.size > 1)
    }

    @Test fun `every language drawn in the bundled font has all its letters in it`() {
        val lacking = mutableListOf<String>()
        for ((name, file) in languages.toSortedMap()) {
            if (name.substringBefore(' ') in systemFontLanguages) continue
            val missing = texts(file).flatMap { it.codePoints().toArray().asList() }
                .filter { inWords(it) && !font.canDisplay(it) }
                .toSortedSet()
            if (missing.isNotEmpty()) {
                lacking += "$name: " + missing.joinToString(" ") { String(Character.toChars(it)) + " U+%04X".format(it) }
            }
        }
        assertEquals("languages whose letters the bundled font lacks, and not set in the system font", emptyList<String>(), lacking)
    }
}
