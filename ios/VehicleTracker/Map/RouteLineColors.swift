import Foundation

/// The route line's colour and its casing, as Android's `RouteLine.kt` picks
/// them, so the same feed colour draws the same line on both apps.
enum RouteLineColors {
    /// iOS's `systemBlue`, the colour the line falls back to when the feed
    /// gives none.
    static let fallback = RGB(0x007AFF)
    static let ink = RGB(0x1F1F1F)
    static let nightInk = RGB(0x0A0A0A)
    static let white = RGB(0xFFFFFF)
    /// Android's plain dark map background: a route has to stand 3:1 from it.
    static let nightBackground = RGB(0x26272A)

    /// The design's cut-offs: a route brighter than these, by WCAG luminance,
    /// gets a dark casing, where a white one would melt into the line. Darker
    /// routes get white.
    static let lightMapDarkCasingAbove = 0.35
    static let darkMapDarkCasingAbove = 0.18

    /// How far a route line stands from the dark map: a line, like any
    /// graphic, needs 3:1.
    static let nightContrast = 3.0
    static let lightnessStep = 0.01

    /// The feed's `route_color`, or the fallback when it has none. On the
    /// `dark` map a route too dark to see is lifted until it reads.
    static func line(hex: String, dark: Bool) -> RGB {
        let color = RGB(hex: hex) ?? fallback
        return dark ? liftedForNight(color) : color
    }

    /// The casing drawn wider under the line, so the line reads over the
    /// streets whatever its colour: ink under a bright route, white under a
    /// dark one, with the design's cut-off for each map.
    static func casing(line: RGB, dark: Bool) -> RGB {
        let luminance = luminance(line)
        if dark {
            return luminance > darkMapDarkCasingAbove ? nightInk : white
        }
        return luminance > lightMapDarkCasingAbove ? ink : white
    }

    /// `color` lighter, with its hue and saturation kept, until it stands
    /// 3:1 from the dark map: a navy, maroon or black route would be lost on
    /// it. A route that already reads comes back as it was.
    static func liftedForNight(_ color: RGB) -> RGB {
        let (hue, saturation, start) = hsl(color)
        var lightness = start
        var lifted = color
        while contrast(lifted, nightBackground) < nightContrast && lightness < 1 {
            lightness = min(lightness + lightnessStep, 1)
            lifted = fromHSL(hue: hue, saturation: saturation, lightness: lightness)
        }
        return lifted
    }

    /// WCAG relative luminance, with the cut-off Android uses.
    static func luminance(_ color: RGB) -> Double {
        func linear(_ channel: Double) -> Double {
            channel <= 0.03928 ? channel / 12.92 : pow((channel + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * linear(color.red) + 0.7152 * linear(color.green) + 0.0722 * linear(color.blue)
    }

    /// WCAG contrast ratio, 1 to 21.
    static func contrast(_ a: RGB, _ b: RGB) -> Double {
        let la = luminance(a), lb = luminance(b)
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }

    private static func hsl(_ color: RGB) -> (hue: Double, saturation: Double, lightness: Double) {
        let high = max(color.red, color.green, color.blue)
        let low = min(color.red, color.green, color.blue)
        let lightness = (high + low) / 2
        let delta = high - low
        if delta == 0 { return (0, 0, lightness) }
        let saturation = delta / (1 - abs(2 * lightness - 1))
        let hue: Double
        switch high {
        case color.red: hue = 60 * flooredRemainder((color.green - color.blue) / delta, 6)
        case color.green: hue = 60 * ((color.blue - color.red) / delta + 2)
        default: hue = 60 * ((color.red - color.green) / delta + 4)
        }
        return (hue, saturation, lightness)
    }

    /// Back to 8-bit channels, as Android's `fromHsl` rounds them.
    private static func fromHSL(hue: Double, saturation: Double, lightness: Double) -> RGB {
        let chroma = (1 - abs(2 * lightness - 1)) * saturation
        let x = chroma * (1 - abs(flooredRemainder(hue / 60, 2) - 1))
        let m = lightness - chroma / 2
        let (r, g, b): (Double, Double, Double)
        switch Int(hue / 60) {
        case 0: (r, g, b) = (chroma, x, 0)
        case 1: (r, g, b) = (x, chroma, 0)
        case 2: (r, g, b) = (0, chroma, x)
        case 3: (r, g, b) = (0, x, chroma)
        case 4: (r, g, b) = (x, 0, chroma)
        default: (r, g, b) = (chroma, 0, x)
        }
        func channel(_ value: Double) -> Double {
            min(255, max(0, ((value + m) * 255).rounded())) / 255
        }
        return RGB(red: channel(r), green: channel(g), blue: channel(b))
    }

    /// Kotlin's `mod`: never negative, unlike `truncatingRemainder`.
    private static func flooredRemainder(_ value: Double, _ divisor: Double) -> Double {
        let remainder = value.truncatingRemainder(dividingBy: divisor)
        return remainder < 0 ? remainder + divisor : remainder
    }
}
