package org.onebusaway.vehicletracker.ui

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.MAP_FILE_PLACEHOLDER
import java.io.File

/**
 * The street map assets as committed, read from `src/main/assets/map`: a style that names a font,
 * a glyph range or a sprite the app does not ship draws a blank map or blank labels, and only the
 * emulator would show it.
 */
class MapAssetsTest {
    private val assets = File("src/main/assets/map")
    private val font = "Noto Sans Regular"

    /** Basic Latin to letterlike symbols: every range a label in Latin letters can need. */
    private val latinRanges = listOf(0, 256, 512, 768, 7680, 8192, 8448)

    private val latinLabel = Json.parseToJsonElement(
        """["case",["has","script"],["get","name:en"],["coalesce",["get","name:en"],["get","name"]]]""",
    )

    private fun style(flavor: String): Pair<String, JsonObject> {
        val text = File(assets, "style-$flavor.json").readText()
        return text to Json.parseToJsonElement(text).jsonObject
    }

    private fun JsonObject.layers() = getValue("layers").jsonArray.map { it.jsonObject }

    private fun JsonObject.labelLayers() = layers().mapNotNull { it["layout"]?.jsonObject?.takeIf { layout -> "text-field" in layout } }

    @Test fun `both styles read the map file through the placeholder, once`() {
        for (flavor in listOf("light", "dark")) {
            val (text, style) = style(flavor)
            val source = style.getValue("sources").jsonObject.getValue("protomaps").jsonObject
            assertEquals(flavor, "vector", source.getValue("type").jsonPrimitive.content)
            assertEquals(flavor, "pmtiles://file://$MAP_FILE_PLACEHOLDER", source.getValue("url").jsonPrimitive.content)
            assertEquals(flavor, 1, text.split(MAP_FILE_PLACEHOLDER).size - 1)
        }
    }

    @Test fun `both styles credit OpenStreetMap`() {
        for (flavor in listOf("light", "dark")) {
            val source = style(flavor).second.getValue("sources").jsonObject.getValue("protomaps").jsonObject
            assertTrue(flavor, source.getValue("attribution").jsonPrimitive.content.contains("OpenStreetMap contributors"))
        }
    }

    @Test fun `every label is drawn in the one font the app ships`() {
        for (flavor in listOf("light", "dark")) {
            val labels = style(flavor).second.labelLayers()
            assertTrue(flavor, labels.isNotEmpty())
            for (layout in labels) assertEquals(flavor, JsonArray(listOf(Json.parseToJsonElement("\"$font\""))), layout.getValue("text-font"))
        }
    }

    @Test fun `names are drawn in Latin letters only`() {
        for (flavor in listOf("light", "dark")) {
            val (text, style) = style(flavor)
            assertFalse(flavor, text.contains("pgf:"))
            val nameLabels = style.labelLayers().map { it.getValue("text-field") }.filter { it.toString().contains("\"name") }
            assertTrue(flavor, nameLabels.isNotEmpty())
            for (field in nameLabels) assertEquals(flavor, latinLabel, field)
        }
    }

    @Test fun `glyphs and sprites are read from the app's own assets`() {
        for (flavor in listOf("light", "dark")) {
            val style = style(flavor).second
            assertEquals("asset://map/fonts/{fontstack}/{range}.pbf", style.getValue("glyphs").jsonPrimitive.content)
            assertEquals("asset://map/sprites/$flavor", style.getValue("sprite").jsonPrimitive.content)
        }
    }

    @Test fun `every glyph range a Latin label needs is there`() {
        for (start in latinRanges) {
            val range = File(assets, "fonts/$font/$start-${start + 255}.pbf")
            assertTrue("missing $range", range.length() > 0)
        }
    }

    @Test fun `both sprite sheets are there, at both densities`() {
        for (sheet in listOf("light", "light@2x", "dark", "dark@2x")) {
            for (extension in listOf("json", "png")) {
                assertTrue("missing $sheet.$extension", File(assets, "sprites/$sheet.$extension").length() > 0)
            }
        }
    }

    @Test fun `the licences ship beside what they cover`() {
        assertTrue(File(assets, "LICENSE.md").readText().contains("BSD 3-Clause"))
        assertTrue(File(assets, "fonts/OFL.txt").readText().contains("SIL OPEN FONT LICENSE"))
    }
}
