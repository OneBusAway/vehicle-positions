package org.onebusaway.vehicletracker.ui.map

import android.content.Context
import kotlinx.serialization.json.JsonPrimitive
import java.io.File

/** What the route is drawn on until the street map is on the phone: the design's map grey, light and dark. */
const val PLAIN_BACKGROUND_LIGHT = 0xFFE3E5E8.toInt()
const val PLAIN_BACKGROUND_DARK = 0xFF26272A.toInt()

/**
 * Where the street style names the map file. `android/map-style/generate.mjs` writes it into the
 * styles under `assets/map/`, once each.
 */
const val MAP_FILE_PLACEHOLDER = "__MAP_FILE__"

/** The street style for [dark] or light, from the app's assets, reading [file]. */
fun streetStyle(context: Context, dark: Boolean, file: File): String {
    val template = context.assets.open(if (dark) "map/style-dark.json" else "map/style-light.json")
        .bufferedReader()
        .use { it.readText() }
    return streetStyleJson(template, file)
}

/**
 * [template] reading its vector tiles from [file]. The path lands inside a JSON string, so it is
 * escaped as one.
 */
fun streetStyleJson(template: String, file: File): String {
    require(template.contains(MAP_FILE_PLACEHOLDER)) { "the street style does not name a map file" }
    val escaped = JsonPrimitive(file.absolutePath).toString().removeSurrounding("\"")
    return template.replace(MAP_FILE_PLACEHOLDER, escaped)
}
