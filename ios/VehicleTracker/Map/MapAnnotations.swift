import MapKit

/// A stop on the trip; the next one is drawn larger and labelled, and the
/// last one, the end of the line, in an ink ring.
nonisolated final class StopAnnotation: NSObject, MKAnnotation {
    let stop: TripStop
    let isLast: Bool
    @objc dynamic var coordinate: CLLocationCoordinate2D
    var isNext = false

    init(stop: TripStop, isLast: Bool) {
        self.stop = stop
        self.isLast = isLast
        coordinate = CLLocationCoordinate2D(latitude: stop.lat, longitude: stop.lon)
    }
}

/// The vehicle, pointed along its course.
nonisolated final class VehicleAnnotation: NSObject, MKAnnotation {
    @objc dynamic var coordinate = CLLocationCoordinate2D(latitude: 0, longitude: 0)
    var course = 0.0
}

/// Where on the shape the vehicle is taken to be while it is off route.
nonisolated final class SnappedAnnotation: NSObject, MKAnnotation {
    @objc dynamic var coordinate = CLLocationCoordinate2D(latitude: 0, longitude: 0)
}

/// Images for the annotation views, in Android's sizes and colours
/// (`MapStops.kt`, `RouteMap.kt`, `ic_map_vehicle_puck` and
/// `ic_map_vehicle_ring`), drawn once per input and kept.
enum MapGlyphs {
    enum Stop: Equatable {
        case ordinary
        case next(label: String)
        case last
    }

    /// Across, ring included.
    static let stopDiameter: CGFloat = 12
    static let lastStopDiameter: CGFloat = 17
    static let nextStopDiameter: CGFloat = 23
    static let nextStopHaloDiameter: CGFloat = 25
    static let vehicleSize: CGFloat = 52

    private static let stopRingWidth: CGFloat = 2
    private static let markedStopRingWidth: CGFloat = 3
    private static let labelGap: CGFloat = 6
    private static let labelHalo: CGFloat = 1.5
    private static let labelMaxWidth: CGFloat = 200

    /// `traits` resolves the dynamic colours: an image is drawn once and
    /// kept, so `UIColor.label` has to be pinned to the trait collection the
    /// view will show it in rather than to whatever is current while drawing.
    static func stop(_ kind: Stop, traits: UITraitCollection) -> UIImage {
        switch kind {
        case .ordinary:
            discs(across: stopDiameter, [(stopDiameter / 2, .mapStopRing), (stopDiameter / 2 - stopRingWidth, .white)])
        case .last:
            discs(across: lastStopDiameter, [(lastStopDiameter / 2, .mapInk), (lastStopDiameter / 2 - markedStopRingWidth, .white)])
        case .next(let label):
            nextStop(label: label, traits: traits)
        }
    }

    /// The vehicle on its status ring: Android's puck, an ink disc with a
    /// white arrow pointing north so the view can turn it to the course,
    /// inside a thin edge that keeps the ring clear of the map. Light and
    /// dark swap the disc, the arrow and the edge.
    static func vehicle(ring: UIColor, traits: UITraitCollection) -> UIImage {
        let centre = CGPoint(x: vehicleSize / 2, y: vehicleSize / 2)
        return UIGraphicsImageRenderer(size: CGSize(width: vehicleSize, height: vehicleSize)).image { _ in
            fill([(24, ring)], at: centre)
            let edge = UIBezierPath(ovalIn: CGRect(x: 1, y: 1, width: vehicleSize - 2, height: vehicleSize - 2))
            edge.lineWidth = 2
            UIColor.mapVehicleEdge.resolvedColor(with: traits).setStroke()
            edge.stroke()
            fill([(17, UIColor.mapVehicleDisc.resolvedColor(with: traits))], at: centre)
            let arrow = UIBezierPath()
            arrow.move(to: CGPoint(x: 26, y: 15))
            arrow.addLine(to: CGPoint(x: 34.5, y: 35))
            arrow.addLine(to: CGPoint(x: 26, y: 30.5))
            arrow.addLine(to: CGPoint(x: 17.5, y: 35))
            arrow.close()
            arrow.lineWidth = 1.5
            arrow.lineJoinStyle = .round
            let arrowColor = UIColor.mapVehicleArrow.resolvedColor(with: traits)
            arrowColor.setFill()
            arrow.fill()
            arrowColor.setStroke()
            arrow.stroke()
        }
    }

    /// The next stop in its white halo, with its name beside it. The name is
    /// drawn by UIKit, not as map text, so its halo and its truncation are
    /// ours; Android draws its label the same way.
    private static func nextStop(label: String, traits: UITraitCollection) -> UIImage {
        let font = UIFontMetrics(forTextStyle: .caption1).scaledFont(for: .systemFont(ofSize: 13, weight: .semibold), compatibleWith: traits)
        let textWidth = min(labelMaxWidth, ceil((label as NSString).size(withAttributes: [.font: font]).width))
        let textHeight = ceil(font.lineHeight)
        let halo = nextStopHaloDiameter
        let size = CGSize(width: halo + (label.isEmpty ? 0 : labelGap + textWidth + 2 * labelHalo),
                          height: max(halo, textHeight + 2 * labelHalo))
        return UIGraphicsImageRenderer(size: size).image { _ in
            fill([(halo / 2, .white), (nextStopDiameter / 2, .mapInk), (nextStopDiameter / 2 - markedStopRingWidth, .mapNextStop)],
                 at: CGPoint(x: halo / 2, y: size.height / 2))
            guard !label.isEmpty else { return }
            let paragraph = NSMutableParagraphStyle()
            paragraph.lineBreakMode = .byTruncatingTail
            let rect = CGRect(x: halo + labelGap + labelHalo, y: (size.height - textHeight) / 2, width: textWidth, height: textHeight)
            // The outline first, then the text over it: a positive stroke
            // width draws the outline alone, half of it outside the glyphs.
            (label as NSString).draw(in: rect, withAttributes: [
                .font: font, .paragraphStyle: paragraph,
                .strokeColor: UIColor.systemBackground.resolvedColor(with: traits),
                .strokeWidth: 2 * labelHalo / font.pointSize * 100,
            ])
            (label as NSString).draw(in: rect, withAttributes: [
                .font: font, .paragraphStyle: paragraph,
                .foregroundColor: UIColor.label.resolvedColor(with: traits),
            ])
        }
    }

    private static func discs(across: CGFloat, _ layers: [(radius: CGFloat, color: UIColor)]) -> UIImage {
        UIGraphicsImageRenderer(size: CGSize(width: across, height: across)).image { _ in
            fill(layers, at: CGPoint(x: across / 2, y: across / 2))
        }
    }

    /// Discs about `centre`, largest first, so each ring shows round the one
    /// inside it.
    private static func fill(_ layers: [(radius: CGFloat, color: UIColor)], at centre: CGPoint) {
        for (radius, color) in layers {
            color.setFill()
            UIBezierPath(ovalIn: CGRect(x: centre.x - radius, y: centre.y - radius, width: 2 * radius, height: 2 * radius)).fill()
        }
    }
}
