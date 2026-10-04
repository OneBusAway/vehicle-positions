package org.onebusaway.vehicletracker.ui.map

private const val OPENGL_ES_3_0 = 0x30000

/**
 * Whether this phone can draw the map. MapLibre needs OpenGL ES 3.0. [glEsVersion] is
 * `ConfigurationInfo.reqGlEsVersion`: the major version in the high 16 bits, the minor in the low.
 */
fun supportsMap(glEsVersion: Int): Boolean = glEsVersion >= OPENGL_ES_3_0
