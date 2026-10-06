package org.onebusaway.vehicletracker.ui

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.MAP_FILE_PLACEHOLDER
import org.onebusaway.vehicletracker.ui.map.streetStyleJson
import java.io.File

class MapStyleTest {
    private val template = """{"version":8,"sources":{"protomaps":{"type":"vector","url":"pmtiles://file://$MAP_FILE_PLACEHOLDER"}},"layers":[]}"""

    private fun sourceUrl(style: String): String =
        Json.parseToJsonElement(style).jsonObject["sources"]!!.jsonObject["protomaps"]!!.jsonObject["url"]!!.jsonPrimitive.content

    @Test fun `the file's path goes into the source`() {
        val file = File("/data/user/0/org.onebusaway.vehicletracker/no_backup/map/map-1.pmtiles")

        assertEquals("pmtiles://file://${file.absolutePath}", sourceUrl(streetStyleJson(template, file)))
    }

    @Test fun `a path JSON has to escape still makes valid JSON with the path intact`() {
        val file = File("/tmp/a \"quoted\" \\ name/map.pmtiles")

        assertEquals("pmtiles://file://${file.absolutePath}", sourceUrl(streetStyleJson(template, file)))
    }

    @Test fun `a style that names no file is refused`() {
        assertThrows(IllegalArgumentException::class.java) { streetStyleJson("""{"version":8}""", File("/map.pmtiles")) }
    }
}
