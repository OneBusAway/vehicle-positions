package org.onebusaway.vehicletracker.ui.map

import androidx.compose.ui.graphics.Color

/** The land and streets of a drawn map. */
internal class StreetColors(val land: Color, val road: Color, val park: Color)

// The day roads are grey rather than the Tracking design's white, which can't be seen on this land.
private val DayStreets = StreetColors(land = Color(0xFFEDEFF1), road = Color(0xFFCED2D6), park = Color(0xFFD5E8D4))
private val NightStreets = StreetColors(land = Color(0xFF1F2023), road = Color(0xFF36383C), park = Color(0xFF1C2A21))

internal fun streetColors(dark: Boolean): StreetColors = if (dark) NightStreets else DayStreets
