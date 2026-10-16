package org.onebusaway.vehicletracker.ui

import androidx.compose.material3.ColorScheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.ui.map.PLAIN_BACKGROUND_DARK
import org.onebusaway.vehicletracker.ui.map.PLAIN_BACKGROUND_LIGHT
import org.onebusaway.vehicletracker.ui.theme.DarkColors
import org.onebusaway.vehicletracker.ui.theme.DarkProblemColors
import org.onebusaway.vehicletracker.ui.theme.DarkStatusColors
import org.onebusaway.vehicletracker.ui.theme.LightColors
import org.onebusaway.vehicletracker.ui.theme.LightProblemColors
import org.onebusaway.vehicletracker.ui.theme.LightStatusColors
import org.onebusaway.vehicletracker.ui.theme.ProblemColors
import org.onebusaway.vehicletracker.ui.theme.StatusColors
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import java.io.File

/**
 * WCAG contrast for the pairs the theme promises, in both themes: 4.5:1 for text, and 7:1 for white
 * on a status fill, which the driver reads at a glance in sun.
 */
class ThemeContrastTest {
    private class Theme(val name: String, val scheme: ColorScheme, val status: StatusColors, val problems: ProblemColors)

    private val themes = listOf(
        Theme("light", LightColors, LightStatusColors, LightProblemColors),
        Theme("dark", DarkColors, DarkStatusColors, DarkProblemColors),
    )

    private fun contrast(a: Color, b: Color): Float {
        val lighter = maxOf(a.luminance(), b.luminance())
        val darker = minOf(a.luminance(), b.luminance())
        return (lighter + 0.05f) / (darker + 0.05f)
    }

    private fun assertContrast(minimum: Float, pair: String, foreground: Color, background: Color) {
        val ratio = contrast(foreground, background)
        assertTrue("$pair is ${"%.2f".format(ratio)}:1, below $minimum:1", ratio >= minimum)
    }

    @Test fun `ink reads on the surface`() {
        for (theme in themes) {
            val s = theme.scheme
            assertContrast(4.5f, "${theme.name} onSurface", s.onSurface, s.surface)
            assertContrast(4.5f, "${theme.name} onSurfaceVariant", s.onSurfaceVariant, s.surface)
        }
    }

    @Test fun `every on colour reads on its colour and its container`() {
        for (theme in themes) {
            val s = theme.scheme
            assertContrast(4.5f, "${theme.name} onPrimary", s.onPrimary, s.primary)
            assertContrast(4.5f, "${theme.name} onSecondary", s.onSecondary, s.secondary)
            assertContrast(4.5f, "${theme.name} onTertiary", s.onTertiary, s.tertiary)
            assertContrast(4.5f, "${theme.name} onError", s.onError, s.error)
            assertContrast(4.5f, "${theme.name} onPrimaryContainer", s.onPrimaryContainer, s.primaryContainer)
            assertContrast(4.5f, "${theme.name} onSecondaryContainer", s.onSecondaryContainer, s.secondaryContainer)
            assertContrast(4.5f, "${theme.name} onTertiaryContainer", s.onTertiaryContainer, s.tertiaryContainer)
            assertContrast(4.5f, "${theme.name} onErrorContainer", s.onErrorContainer, s.errorContainer)
        }
    }

    @Test fun `status text reads on the surface and on a container`() {
        for (theme in themes) {
            for (tone in StatusTone.entries) {
                val text = theme.status[tone].text
                assertContrast(4.5f, "${theme.name} $tone text on surface", text, theme.scheme.surface)
                assertContrast(4.5f, "${theme.name} $tone text on surfaceContainer", text, theme.scheme.surfaceContainer)
                // The trip panel's colour, where the vehicle's row says how it is keeping to the schedule.
                assertContrast(4.5f, "${theme.name} $tone text on surfaceContainerLow", text, theme.scheme.surfaceContainerLow)
            }
        }
    }

    @Test fun `white on a status fill reads at a glance`() {
        for (theme in themes) {
            for (tone in StatusTone.entries) {
                val status = theme.status[tone]
                assertContrast(7f, "${theme.name} $tone onFill", status.onFill, status.fill)
            }
        }
    }

    /**
     * The ring round the vehicle is the status text colour, on the street map's land and its
     * background, read from the committed styles, and on the plain map before the streets arrive.
     * A ring is a graphic, so 3:1.
     */
    @Test fun `the vehicle's ring stands out on the map`() {
        for (theme in themes) {
            val plain = Color(if (theme.name == "dark") PLAIN_BACKGROUND_DARK else PLAIN_BACKGROUND_LIGHT)
            for (tone in StatusTone.entries) {
                assertContrast(3f, "${theme.name} $tone ring on the plain map", theme.status[tone].text, plain)
            }
            val style = Json.parseToJsonElement(File("src/main/assets/map/style-${theme.name}.json").readText()).jsonObject
            val paint = style.getValue("layers").jsonArray.map { it.jsonObject }.associate { layer ->
                layer.getValue("id").jsonPrimitive.content to layer["paint"]?.jsonObject
            }
            val land = listOf(
                "background" to paint.getValue("background")?.getValue("background-color"),
                "earth" to paint.getValue("earth")?.getValue("fill-color"),
            )
            for ((layer, value) in land) {
                val hex = requireNotNull(value).jsonPrimitive.content
                require(hex.matches(Regex("#[0-9a-fA-F]{6}"))) { "the $layer layer is no longer a plain colour: $hex" }
                val map = Color(0xFF000000 or hex.removePrefix("#").toLong(16))
                for (tone in StatusTone.entries) {
                    assertContrast(3f, "${theme.name} $tone ring on the map's $layer", theme.status[tone].text, map)
                }
            }
        }
    }

    @Test fun `a problem reads on its container`() {
        for (theme in themes) {
            assertContrast(4.5f, "${theme.name} problem content", theme.problems.content, theme.problems.container)
            // An icon is a graphic, which WCAG holds to 3:1.
            assertContrast(3f, "${theme.name} problem icon", theme.problems.icon, theme.problems.container)
        }
    }

    @Test fun `a problem icon reads on the trip bar and in a dialog`() {
        for (theme in themes) {
            val icon = theme.problems.iconOnSurface
            assertContrast(3f, "${theme.name} problem icon on surfaceContainerLow", icon, theme.scheme.surfaceContainerLow)
            // AlertDialog's container colour.
            assertContrast(3f, "${theme.name} problem icon on surfaceContainerHigh", icon, theme.scheme.surfaceContainerHigh)
        }
    }

    @Test fun `the signed out card reads, and its button stands out`() {
        for (theme in themes) {
            val card = theme.problems.signedOut
            assertContrast(4.5f, "${theme.name} signed out content", card.content, card.container)
            assertContrast(4.5f, "${theme.name} signed out onAccent", card.onAccent, card.accent)
            assertContrast(3f, "${theme.name} signed out accent on the card", card.accent, card.container)
        }
    }
}
