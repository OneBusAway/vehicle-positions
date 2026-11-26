import SwiftUI

/// A drawn map's colours, taken from the Tracking design: the welcome's first
/// picture and the two in During a trip. They are the only colours there that
/// are not the app's own.
nonisolated struct MapColors {
    let land: Color
    let road: Color
    let block: Color
    let warmBlock: Color
    let phoneBody: Color

    init(dark: Bool) {
        land = Self.hex(dark ? 0x3B4D63 : 0xF4F3EF)
        road = Self.hex(dark ? 0x2F3D50 : 0xC3CBCF)
        block = Self.hex(dark ? 0x4E6888 : 0xE6E6E4)
        warmBlock = Self.hex(dark ? 0x566E8C : 0xF2ECDD)
        phoneBody = Self.hex(dark ? 0x2C2C2E : 0xFFFFFF)
    }

    private static func hex(_ value: UInt32) -> Color {
        Color(red: Double((value >> 16) & 0xFF) / 255, green: Double((value >> 8) & 0xFF) / 255, blue: Double(value & 0xFF) / 255)
    }
}
