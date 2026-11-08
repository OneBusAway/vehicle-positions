import SwiftUI
import UIKit

/// An opaque sRGB colour as the feed writes it, six hex digits, kept as
/// numbers: never read back out of a `UIColor`, whose `getRed` fails for a
/// colour in another space.
struct RGB: Equatable {
    /// 0 to 1.
    var red: Double
    var green: Double
    var blue: Double

    init(red: Double, green: Double, blue: Double) {
        self.red = red
        self.green = green
        self.blue = blue
    }

    /// `0xRRGGBB`.
    init(_ value: UInt32) {
        self.init(red: Double((value >> 16) & 0xFF) / 255, green: Double((value >> 8) & 0xFF) / 255, blue: Double(value & 0xFF) / 255)
    }

    /// Six hex digits, with or without a leading '#'; nil for anything else,
    /// which GTFS allows and the server passes through as "".
    init?(hex: String) {
        var digits = hex.trimmingCharacters(in: .whitespaces)
        if digits.hasPrefix("#") { digits.removeFirst() }
        guard digits.count == 6, let value = UInt32(digits, radix: 16) else { return nil }
        self.init(value)
    }

    /// "RRGGBB", as `Color.kt` and routes.txt write it.
    var hex: String {
        String(format: "%02X%02X%02X", Self.channel(red), Self.channel(green), Self.channel(blue))
    }

    var uiColor: UIColor {
        UIColor(red: red, green: green, blue: blue, alpha: 1)
    }

    private static func channel(_ value: Double) -> Int {
        min(255, max(0, Int((value * 255).rounded())))
    }
}

extension UIColor {
    /// A GTFS route colour: six hex digits, with or without a leading '#'.
    convenience init?(hex: String) {
        guard let rgb = RGB(hex: hex) else { return nil }
        self.init(red: rgb.red, green: rgb.green, blue: rgb.blue, alpha: 1)
    }
}

extension Color {
    /// A GTFS route colour: six hex digits, with or without a leading '#'.
    /// One parser, in `UIColor`, so the badge and the map cannot disagree
    /// about what a feed's colour means.
    init?(hex: String) {
        guard let ui = UIColor(hex: hex) else { return nil }
        self.init(uiColor: ui)
    }
}
