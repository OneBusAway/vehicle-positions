import MapKit

/// A stop on the trip; the next one is drawn larger and labelled.
nonisolated final class StopAnnotation: NSObject, MKAnnotation {
    let stop: TripStop
    @objc dynamic var coordinate: CLLocationCoordinate2D
    var isNext = false

    init(stop: TripStop) {
        self.stop = stop
        coordinate = CLLocationCoordinate2D(latitude: stop.lat, longitude: stop.lon)
    }
}

/// The vehicle, pointed along its course; grey when off route.
nonisolated final class VehicleAnnotation: NSObject, MKAnnotation {
    @objc dynamic var coordinate = CLLocationCoordinate2D(latitude: 0, longitude: 0)
    var course = 0.0
    var isOnRoute = true
}

/// Where on the shape the vehicle is taken to be while it is off route.
nonisolated final class SnappedAnnotation: NSObject, MKAnnotation {
    @objc dynamic var coordinate = CLLocationCoordinate2D(latitude: 0, longitude: 0)
}

extension UIColor {
    /// A GTFS route colour: six hex digits, with or without a leading '#'.
    convenience init?(hex: String) {
        var s = hex.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("#") { s.removeFirst() }
        guard s.count == 6, let v = UInt32(s, radix: 16) else { return nil }
        self.init(red: CGFloat((v >> 16) & 0xFF) / 255, green: CGFloat((v >> 8) & 0xFF) / 255, blue: CGFloat(v & 0xFF) / 255, alpha: 1)
    }
}

/// Images for the annotation views, drawn once per size and colour.
enum MapGlyphs {
    static func stop(diameter: CGFloat, fill: UIColor, label: String?) -> UIImage {
        let font = UIFont.systemFont(ofSize: 13, weight: .semibold)
        let textSize = label.map { ($0 as NSString).size(withAttributes: [.font: font]) } ?? .zero
        let size = CGSize(width: diameter + (label == nil ? 0 : textSize.width + 8), height: max(diameter, textSize.height))
        return UIGraphicsImageRenderer(size: size).image { ctx in
            let circle = CGRect(x: 0, y: (size.height - diameter) / 2, width: diameter, height: diameter)
            fill.setFill()
            UIBezierPath(ovalIn: circle).fill()
            UIColor.white.setStroke()
            let ring = UIBezierPath(ovalIn: circle.insetBy(dx: 1, dy: 1))
            ring.lineWidth = 2
            ring.stroke()
            if let label {
                let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: UIColor.label,
                                                            .strokeColor: UIColor.systemBackground, .strokeWidth: -3]
                (label as NSString).draw(at: CGPoint(x: diameter + 6, y: (size.height - textSize.height) / 2), withAttributes: attrs)
            }
        }
    }

    static func vehicle(fill: UIColor) -> UIImage {
        let size = CGSize(width: 28, height: 28)
        return UIGraphicsImageRenderer(size: size).image { _ in
            let path = UIBezierPath()
            path.move(to: CGPoint(x: 14, y: 2))
            path.addLine(to: CGPoint(x: 25, y: 26))
            path.addLine(to: CGPoint(x: 14, y: 20))
            path.addLine(to: CGPoint(x: 3, y: 26))
            path.close()
            fill.setFill()
            path.fill()
            UIColor.white.setStroke()
            path.lineWidth = 2
            path.stroke()
        }
    }

    static func dot() -> UIImage {
        UIGraphicsImageRenderer(size: CGSize(width: 10, height: 10)).image { _ in
            UIColor.systemBackground.setFill()
            UIBezierPath(ovalIn: CGRect(x: 0, y: 0, width: 10, height: 10)).fill()
            UIColor.label.setFill()
            UIBezierPath(ovalIn: CGRect(x: 2, y: 2, width: 6, height: 6)).fill()
        }
    }
}
