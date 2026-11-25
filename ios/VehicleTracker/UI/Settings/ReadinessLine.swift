import SwiftUI

/// Before you start's rows as stops on a route that ends at a rider, with the
/// Tracking map's vehicle where it can get to: solid behind it, dashed ahead.
/// It is the lower edge of the header, which says the same in words. Its
/// offsets and its rotation are from the leading edge, so right to left
/// SwiftUI turns the whole line round.
struct ReadinessLine: View {
    let readiness: Readiness
    let geometry: LineGeometry
    let position: Double
    let arrivals: Int
    let content: Color
    let showsSymbols: Bool
    let fold: HeaderFold

    private static let thickness: CGFloat = 6
    private static let vehicleSize: CGFloat = 46
    private static let riderHeight: CGFloat = 28
    private static let symbolSize: CGFloat = 22
    private static let symbolAboveLine: CGFloat = 35
    /// How far up from the bottom edge the middle of the line is.
    private static let middle: CGFloat = thickness / 2

    private var vehicleSide: CGFloat { geometry.isNarrow ? Self.vehicleSize * 0.82 : Self.vehicleSize }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            track
            ForEach(Array(readiness.stops.enumerated()), id: \.offset) { index, stop in
                let x = geometry.x(at: Double(index + 1))
                StopMark(stop: stop)
                    .offset(x: x - StopMark.side / 2, y: StopMark.side / 2 - Self.middle)
                symbol(LocationChecklist.Row.allCases[index].symbol, for: stop)
                    .offset(x: x - Self.symbolSize / 2, y: -(Self.middle + Self.symbolAboveLine - Self.symbolSize / 2))
            }
            rider
            vehicle
        }
        .frame(maxWidth: .infinity, alignment: .bottomLeading)
        // The line moves as one, whatever the vehicle is doing on it.
        .geometryGroup()
    }

    private var track: some View {
        ZStack {
            LineAhead(geometry: geometry, position: position, arrivesAt: arrivesAt)
                .stroke(Color.secondary, style: StrokeStyle(lineWidth: 4, dash: [8, 6]))
            LineBehind(geometry: geometry, position: position, arrivesAt: arrivesAt)
                .fill(.accent)
        }
        .frame(height: Self.thickness)
    }

    /// Where the vehicle is by the riders: nowhere, while it can't get there.
    private var arrivesAt: Double {
        readiness.canReport ? Double(readiness.vehicleAt) : .infinity
    }

    private func symbol(_ name: String, for stop: Readiness.Stop) -> some View {
        Image(systemName: name)
            .resizable()
            .scaledToFit()
            .fontWeight(.semibold)
            .foregroundStyle(stop == .missing ? .amberOnSurface : stop == .beyond ? Color.secondary : content)
            .frame(width: Self.symbolSize, height: Self.symbolSize)
            .visualEffect { [fold] effect, proxy in effect.opacity(fold.symbolsOpacity(proxy.headerScroll)) }
            .opacity(showsSymbols ? 1 : 0)
    }

    /// Their feet on the line.
    private var rider: some View {
        Image(systemName: "figure.wave")
            .resizable()
            .scaledToFit()
            .foregroundStyle(content)
            .opacity(readiness.canReport ? 1 : 0.5)
            .frame(width: Self.riderHeight, height: Self.riderHeight, alignment: .bottom)
            .phaseAnimator([0, ReadinessMotion.lift], trigger: arrivals) { rider, lift in
                rider.offset(y: lift)
            } animation: { lift in
                lift == 0 ? ReadinessMotion.settling : ReadinessMotion.lifting
            }
            .offset(x: geometry.rider - Self.riderHeight / 2, y: -Self.thickness)
    }

    /// The Tracking map's marker, pointing along the line.
    private var vehicle: some View {
        VehicleMark(ring: (readiness.canReport ? StatusTone.onTime : .neutral).text, side: vehicleSide)
            .rotationEffect(.degrees(90))
            .visualEffect { [fold] effect, proxy in effect.scaleEffect(fold.vehicleScale(proxy.headerScroll)) }
            .offset(x: geometry.x(at: position) - vehicleSide / 2, y: vehicleSide / 2 - Self.middle)
    }
}

/// Solid, from the edge to the vehicle, and past it to the other edge once
/// the vehicle is by the riders.
nonisolated private struct LineBehind: Shape {
    let geometry: LineGeometry
    var position: Double
    let arrivesAt: Double

    var animatableData: Double {
        get { position }
        set { position = newValue }
    }

    func path(in rect: CGRect) -> Path {
        let end = position.hasArrived(at: arrivesAt) ? rect.width : geometry.x(at: position)
        return Path(CGRect(x: rect.minX, y: rect.minY, width: max(end, 0), height: rect.height))
    }
}

/// From the vehicle to the far edge, until the vehicle is by the riders.
nonisolated private struct LineAhead: Shape {
    let geometry: LineGeometry
    var position: Double
    let arrivesAt: Double

    var animatableData: Double {
        get { position }
        set { position = newValue }
    }

    func path(in rect: CGRect) -> Path {
        var path = Path()
        guard !position.hasArrived(at: arrivesAt) else { return path }
        path.move(to: CGPoint(x: rect.minX + geometry.x(at: position), y: rect.midY))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.midY))
        return path
    }
}

nonisolated private extension Double {
    /// A little before its spring's last settling, so the line's end does not
    /// wait for it.
    func hasArrived(at stop: Double) -> Bool { self > stop - 0.02 }
}

/// A stop, in a box of one size whatever its state.
private struct StopMark: View {
    let stop: Readiness.Stop

    static let side: CGFloat = 20

    var body: some View {
        // All three, two of them clear: one that came and went would fade
        // out where it was as the line moves.
        ZStack {
            Circle().fill(.accent)
                .frame(width: 14, height: 14)
                .opacity(stop == .allowed ? 1 : 0)
            ZStack {
                Circle().fill(Color(.systemGroupedBackground))
                Circle().strokeBorder(.amberOnSurface, lineWidth: 4)
                // Barred as well as amber, so it does not rest on the colour
                // alone.
                Capsule().fill(.amberOnSurface).frame(width: 8, height: 3)
            }
            .opacity(stop == .missing ? 1 : 0)
            Circle().fill(Color(.systemGroupedBackground)).strokeBorder(.statusNoDataText, lineWidth: 2)
                .frame(width: 14, height: 14)
                .opacity(stop == .beyond ? 1 : 0)
        }
        .frame(width: Self.side, height: Self.side)
    }
}

/// The Tracking map's vehicle marker, as `MapGlyphs.vehicle` draws it in its
/// 52 pt box, pointing up.
struct VehicleMark: View {
    let ring: Color
    let side: CGFloat

    private func scaled(_ points: CGFloat) -> CGFloat { points / MapGlyphs.vehicleSize * side }

    var body: some View {
        ZStack {
            Circle().fill(ring).frame(width: scaled(48), height: scaled(48))
            Circle().strokeBorder(.mapVehicleEdge, lineWidth: scaled(2))
            Circle().fill(.mapVehicleDisc).frame(width: scaled(34), height: scaled(34))
            VehicleArrow().fill(.mapVehicleArrow).frame(width: scaled(34), height: scaled(34))
        }
        .frame(width: side, height: side)
    }
}
