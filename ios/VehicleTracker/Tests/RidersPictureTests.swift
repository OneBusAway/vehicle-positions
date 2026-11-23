import CoreGraphics
import SwiftUI
import Testing
@testable import VehicleTracker

/// The welcome's first picture drawn as the screen draws it, at rest, in
/// both layout directions: the vehicle sits on its route, and right to left
/// the whole picture is the mirror of left to right.
@MainActor @Suite struct RidersPictureTests {
    static let size = CGSize(width: 360, height: 500)

    /// The picture's pixels, one point to a pixel, as RGBA.
    func pixels(_ direction: LayoutDirection) throws -> (data: [UInt8], width: Int) {
        let picture = RidersPicture(beat: .rest, isCompact: false)
            .frame(width: Self.size.width, height: Self.size.height)
            .environment(\.layoutDirection, direction)
            .environment(\.colorScheme, .light)
        let renderer = ImageRenderer(content: picture)
        renderer.scale = 1
        let image = try #require(renderer.cgImage)
        let width = image.width, height = image.height
        var data = [UInt8](repeating: 0, count: width * height * 4)
        let drawn = data.withUnsafeMutableBytes { buffer in
            guard let context = CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8,
                                          bytesPerRow: width * 4, space: CGColorSpaceCreateDeviceRGB(),
                                          bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        try #require(drawn)
        return (data, width)
    }

    /// The middle of the solid green across one row: the route, and on the
    /// vehicle's row the vehicle's ring with it. The faded rings round the
    /// vehicle are left out, as they fade into a block on one side.
    func greenMiddle(_ picture: (data: [UInt8], width: Int), row: Int) -> Double? {
        let green = (0..<picture.width).filter { x in
            let i = (row * picture.width + x) * 4
            let (r, g, b) = (Int(picture.data[i]), Int(picture.data[i + 1]), Int(picture.data[i + 2]))
            return r < 120 && g > r + 30 && g > b + 30
        }
        guard let first = green.first, let last = green.last else { return nil }
        return Double(first + last) / 2
    }

    /// Along the top, above the phone and its badge, only the route is green; on the
    /// vehicle's row, halfway down, the route runs under the vehicle.
    @Test(arguments: [LayoutDirection.leftToRight, .rightToLeft])
    func theVehicleSitsOnItsRoute(_ direction: LayoutDirection) throws {
        let picture = try pixels(direction)
        let route = try #require(greenMiddle(picture, row: 2))
        let vehicleRow = try #require(greenMiddle(picture, row: Int(Self.size.height * RidersLayout.vehicleRest)))
        #expect(abs(route - vehicleRow) <= 3, "route at \(route), vehicle's row centred at \(vehicleRow)")
    }

    /// Right to left the route is on the other side, as the route line over
    /// the welcome runs the other way.
    @Test func rightToLeftTheRouteIsMirrored() throws {
        let routeX = RidersLayout(size: Self.size, isCompact: false).routeX
        let leftToRight = try #require(greenMiddle(try pixels(.leftToRight), row: 2))
        let rightToLeft = try #require(greenMiddle(try pixels(.rightToLeft), row: 2))
        #expect(abs(leftToRight - routeX) <= 3)
        #expect(abs(rightToLeft - (Self.size.width - routeX)) <= 3)
    }
}
