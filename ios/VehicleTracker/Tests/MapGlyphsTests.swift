import Testing
import UIKit
@testable import VehicleTracker

/// The marks on the map, in Android's sizes and colours: each image is
/// measured, and read at the points where one layer shows through another.
@Suite struct MapGlyphsTests {
    let light = UITraitCollection(userInterfaceStyle: .light)
    let dark = UITraitCollection(userInterfaceStyle: .dark)

    @Test func theVehicleIsAndroidsPuckOnTheStatusRing() throws {
        let day = MapGlyphs.vehicle(ring: .red, traits: light)
        #expect(day.size == CGSize(width: 52, height: 52))
        #expect(try pixel(day, 26, 4) == "FF0000", "the ring, 24 across from the centre")
        #expect(try pixel(day, 26, 1) == "FFFFFF", "a white edge by day")
        #expect(try pixel(day, 26, 12) == "1F1F1F", "an ink disc by day")
        #expect(try pixel(day, 26, 26) == "FFFFFF", "a white arrow by day")

        let night = MapGlyphs.vehicle(ring: .red, traits: dark)
        #expect(try pixel(night, 26, 4) == "FF0000", "the ring keeps the status colour at night")
        #expect(try pixel(night, 26, 1) == "0B0B0C", "a near-black edge at night")
        #expect(try pixel(night, 26, 12) == "E8EAED", "a light disc at night")
        #expect(try pixel(night, 26, 26) == "1F1F1F", "an ink arrow at night")
    }

    @Test func theStopsAreWhiteInAGreyRingTheNextOrangeInInkOnAHaloAndTheLastInInk() throws {
        let stop = MapGlyphs.stop(.ordinary, traits: light)
        #expect(stop.size == CGSize(width: 12, height: 12))
        #expect(try pixel(stop, 6, 6) == "FFFFFF")
        #expect(try pixel(stop, 1, 6) == "6B6B6B")

        let last = MapGlyphs.stop(.last, traits: light)
        #expect(last.size == CGSize(width: 17, height: 17))
        #expect(try pixel(last, 8, 8) == "FFFFFF")
        #expect(try pixel(last, 1, 8) == "1F1F1F")

        let next = MapGlyphs.stop(.next(label: ""), traits: light)
        #expect(next.size == CGSize(width: 25, height: 25), "the next stop sits in a 25 pt halo")
        #expect(try pixel(next, 12, 12) == "FFA726")
        #expect(try pixel(next, 2, 12) == "1F1F1F")
        // The stops look the same at night.
        #expect(try pixel(MapGlyphs.stop(.next(label: ""), traits: dark), 12, 12) == "FFA726")
        #expect(try pixel(MapGlyphs.stop(.ordinary, traits: dark), 1, 6) == "6B6B6B")
    }

    @Test func theNextStopsNameSitsBesideItsHaloAndIsCutAt200Points() {
        let named = MapGlyphs.stop(.next(label: "3rd Ave & Seneca St"), traits: light)
        #expect(named.size.width > 25 + 6 + 40)
        #expect(named.size.height >= 25)
        let long = MapGlyphs.stop(.next(label: String(repeating: "Rainier Ave S & Mount Baker ", count: 8)), traits: light)
        #expect(long.size.width <= 25 + 6 + 200 + 3)
    }

    /// The six hex digits of the pixel at (`x`, `y`) points, top-left origin.
    private func pixel(_ image: UIImage, _ x: CGFloat, _ y: CGFloat) throws -> String {
        let cgImage = try #require(image.cgImage)
        let scale = image.scale
        let width = Int(image.size.width * scale), height = Int(image.size.height * scale)
        var bytes = [UInt8](repeating: 0, count: width * height * 4)
        let context = try #require(CGContext(data: &bytes, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                                             space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        context.draw(cgImage, in: CGRect(x: 0, y: 0, width: width, height: height))
        let column = Int(x * scale), row = height - 1 - Int(y * scale)
        let index = (row * width + column) * 4
        return String(format: "%02X%02X%02X", bytes[index], bytes[index + 1], bytes[index + 2])
    }
}
