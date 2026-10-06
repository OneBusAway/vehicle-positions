package org.onebusaway.vehicletracker.ui.map

import android.content.Context
import kotlinx.serialization.json.JsonPrimitive
import java.io.File

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
