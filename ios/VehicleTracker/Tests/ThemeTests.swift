import SwiftUI
import Testing
import UIKit
@testable import VehicleTracker

@Suite struct ThemeTests {
    /// A colour set and the value Android's `Color.kt` gives it; `dark` is nil
    /// for a colour that is the same in both themes.
    nonisolated struct AndroidColor: Sendable, CustomTestStringConvertible {
        let name: String
        let light: String
        let dark: String?

        var testDescription: String { name }
    }

    /// Copied from `android/.../ui/theme/Color.kt`, Android's name after each.
    nonisolated static let palette: [AndroidColor] = [
        AndroidColor(name: "AccentColor", light: "3B6939", dark: "A1D39A"), // primary
        AndroidColor(name: "OnAccent", light: "FFFFFF", dark: "0A390F"), // onPrimary
        AndroidColor(name: "AccentContainer", light: "BCF0B4", dark: "235024"), // primaryContainer
        AndroidColor(name: "OnAccentContainer", light: "235024", dark: "BCF0B4"), // onPrimaryContainer
        AndroidColor(name: "StatusOnTimeFill", light: "006600", dark: nil), // onTime.fill
        AndroidColor(name: "StatusLateFill", light: "0055B3", dark: nil), // late.fill
        AndroidColor(name: "StatusEarlyFill", light: "A51D17", dark: nil), // early.fill
        AndroidColor(name: "StatusNoDataFill", light: "58585C", dark: nil), // neutral.fill
        AndroidColor(name: "OnStatusFill", light: "FFFFFF", dark: nil), // onFill
        AndroidColor(name: "StatusOnTimeText", light: "0E7400", dark: "30D158"), // onTime.text
        AndroidColor(name: "StatusLateText", light: "0060C9", dark: "0A8BFF"), // late.text
        AndroidColor(name: "StatusEarlyText", light: "BA2B23", dark: "FF453A"), // early.text
        AndroidColor(name: "StatusNoDataText", light: "636367", dark: "8E8E93"), // neutral.text
        AndroidColor(name: "ProblemContainer", light: "303134", dark: "3C3D40"), // ProblemColors.container
        AndroidColor(name: "ProblemContent", light: "F1F3F4", dark: nil), // content
        AndroidColor(name: "Amber", light: "FDD663", dark: nil), // icon
        AndroidColor(name: "AmberOnSurface", light: "B06000", dark: "FDD663"), // iconOnSurface
        AndroidColor(name: "SignedOutContainer", light: "1F1F1F", dark: "FDD663"), // signedOut.container
        AndroidColor(name: "SignedOutContent", light: "F1F3F4", dark: "1F1F1F"), // signedOut.content
        AndroidColor(name: "SignedOutAccent", light: "FDD663", dark: "1F1F1F"), // signedOut.accent
        AndroidColor(name: "SignedOutOnAccent", light: "1F1F1F", dark: "FDD663"), // signedOut.onAccent
        AndroidColor(name: "AttentionContainer", light: "FEF7E0", dark: "3A3220"), // attentionContainer
        AndroidColor(name: "ErrorText", light: "BA1A1A", dark: "FFB4AB"), // error
    ]

    nonisolated static let styles: [UIUserInterfaceStyle] = [.light, .dark]

    @Test(arguments: palette) func matchesAndroid(_ color: AndroidColor) throws {
        #expect(try Self.named(color.name, .light).hex == color.light)
        #expect(try Self.named(color.name, .dark).hex == color.dark ?? color.light)
    }

    @Test(arguments: styles) func whiteOnEveryStatusFillIsSevenToOne(_ style: UIUserInterfaceStyle) throws {
        for fill in ["StatusOnTimeFill", "StatusLateFill", "StatusEarlyFill", "StatusNoDataFill"] {
            #expect(try Self.contrast("OnStatusFill", on: fill, style) >= 7, "\(fill)")
        }
    }

    /// Words in a status colour, an error, or the accent as a link, on every
    /// surface a list or form draws behind them.
    @Test(arguments: styles) func textReadsOnEverySurface(_ style: UIUserInterfaceStyle) throws {
        let traits = UITraitCollection(userInterfaceStyle: style)
        let surfaces: [UIColor] = [.systemBackground, .systemGroupedBackground, .secondarySystemGroupedBackground]
        for name in ["StatusOnTimeText", "StatusLateText", "StatusEarlyText", "StatusNoDataText", "ErrorText", "AccentColor"] {
            let text = try Self.named(name, style)
            for surface in surfaces.map({ $0.resolvedColor(with: traits) }) {
                #expect(Self.contrast(text, surface) >= 4.5, "\(name) on \(surface.hex)")
            }
        }
    }

    /// Content on the theme's own fills. The accent's label is held to 4.5:1,
    /// not 7:1: Android's light pair is 6.4:1, and Android wins.
    @Test(arguments: styles) func contentOnEveryFillReads(_ style: UIUserInterfaceStyle) throws {
        let pairs = [
            ("OnAccentContainer", "AccentContainer"),
            ("ProblemContent", "ProblemContainer"),
            ("SignedOutContent", "SignedOutContainer"),
            ("SignedOutOnAccent", "SignedOutAccent"),
        ]
        for (content, container) in pairs {
            #expect(try Self.contrast(content, on: container, style) >= 7, "\(content) on \(container)")
        }
        #expect(try Self.contrast("OnAccent", on: "AccentColor", style) >= 4.5)
        // An icon, so WCAG's 3:1 for graphics.
        #expect(try Self.contrast("Amber", on: "ProblemContainer", style) >= 3)
    }

    @Test(arguments: styles) func statusTonesDrawTheirOwnColourSets(_ style: UIUserInterfaceStyle) throws {
        let sets: [(StatusTone, fill: String, text: String)] = [
            (.onTime, "StatusOnTimeFill", "StatusOnTimeText"),
            (.late, "StatusLateFill", "StatusLateText"),
            (.early, "StatusEarlyFill", "StatusEarlyText"),
            (.neutral, "StatusNoDataFill", "StatusNoDataText"),
        ]
        for (tone, fill, text) in sets {
            let wantFill = try Self.named(fill, style).hex, wantText = try Self.named(text, style).hex
            #expect(Self.hex(tone.fill, style) == wantFill, "\(tone)")
            #expect(Self.hex(tone.text, style) == wantText, "\(tone)")
        }
    }

    @Test(arguments: styles) func bannerTonesDrawTheirOwnColourSets(_ style: UIUserInterfaceStyle) throws {
        let sets: [(BannerTone, background: String, foreground: String)] = [
            (.reporting, "StatusOnTimeFill", "OnStatusFill"),
            (.neutral, "StatusNoDataFill", "OnStatusFill"),
            (.problem, "ProblemContainer", "ProblemContent"),
            (.signedOut, "SignedOutContainer", "SignedOutContent"),
        ]
        for (tone, background, foreground) in sets {
            let wantBackground = try Self.named(background, style).hex, wantForeground = try Self.named(foreground, style).hex
            #expect(Self.hex(tone.background, style) == wantBackground, "\(tone)")
            #expect(Self.hex(tone.foreground, style) == wantForeground, "\(tone)")
        }
    }

    /// A colour set from the app's catalog, resolved for `style`. A missing
    /// or misspelt set fails here.
    static func named(_ name: String, _ style: UIUserInterfaceStyle) throws -> UIColor {
        let traits = UITraitCollection(userInterfaceStyle: style)
        let color = try #require(UIColor(named: name, in: Bundle(for: TripSession.self), compatibleWith: traits), "no colour set \(name)")
        return color.resolvedColor(with: traits)
    }

    static func hex(_ color: Color, _ style: UIUserInterfaceStyle) -> String {
        var environment = EnvironmentValues()
        environment.colorScheme = style == .dark ? .dark : .light
        let resolved = color.resolve(in: environment)
        return UIColor(red: CGFloat(resolved.red), green: CGFloat(resolved.green), blue: CGFloat(resolved.blue), alpha: 1).hex
    }

    static func contrast(_ content: String, on container: String, _ style: UIUserInterfaceStyle) throws -> Double {
        contrast(try named(content, style), try named(container, style))
    }

    /// WCAG 2's contrast ratio between two opaque colours.
    static func contrast(_ a: UIColor, _ b: UIColor) -> Double {
        let la = a.relativeLuminance, lb = b.relativeLuminance
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }
}

extension UIColor {
    var srgb: (red: Double, green: Double, blue: Double) {
        var red: CGFloat = 0, green: CGFloat = 0, blue: CGFloat = 0, alpha: CGFloat = 0
        getRed(&red, green: &green, blue: &blue, alpha: &alpha)
        return (Double(red), Double(green), Double(blue))
    }

    /// Six hex digits of the 8-bit sRGB components, as `Color.kt` writes them.
    var hex: String {
        let (red, green, blue) = srgb
        return String(format: "%02X%02X%02X", Int((red * 255).rounded()), Int((green * 255).rounded()), Int((blue * 255).rounded()))
    }

    var relativeLuminance: Double {
        func linear(_ value: Double) -> Double {
            value <= 0.04045 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        let (red, green, blue) = srgb
        return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue)
    }
}
