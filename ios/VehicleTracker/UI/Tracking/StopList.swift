import SwiftUI

/// The run's stops down a strip of the route, as the map draws them and in
/// Android `TripPanel.kt`'s numbers: the vehicle between the stop it last
/// passed and the next, the next stop orange, the last stop in an ink ring,
/// and the passed stops folded into one row.
struct StopList: View {
    let trip: TripGeometry
    let rows: [StopRows.Row]
    let vehicleName: String
    /// The exact deviation, nil before the first fix.
    let vehicleStatus: String?
    let vehicleTone: StatusTone
    let strip: RouteStripColors
    @Binding var unfolded: Bool

    var body: some View {
        LazyVStack(spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
                let strip = RouteStrip(colors: strip, endsAtMiddle: index == rows.count - 1)
                switch row {
                case .stop(let stop, let kind):
                    StopRow(stop: stop, kind: kind, time: Formatters.clock(stop.arrivalAt, timezone: trip.timezone), strip: strip)
                case .folded(let count):
                    FoldedRow(count: count, unfolded: $unfolded, strip: strip)
                case .vehicle:
                    VehicleRow(name: vehicleName, status: vehicleStatus, tone: vehicleTone, strip: strip)
                }
            }
        }
        .padding(.horizontal, 20)
    }
}

/// The route line's colour and its casing, as the map draws them.
struct RouteStripColors: Equatable {
    let line: Color
    let casing: Color
}

/// The route down the list's left edge: a 12 pt strip, the line in its colour
/// between two 2 pt edges of casing, 8 pt from the leading edge. It ends at
/// the row's middle on the last row, where the line ends.
private struct RouteStrip: Equatable {
    let colors: RouteStripColors
    let endsAtMiddle: Bool

    func draw(in context: inout GraphicsContext, size: CGSize) {
        let bottom = endsAtMiddle ? size.height / 2 : size.height
        context.fill(Path(CGRect(x: 8, y: 0, width: 12, height: bottom)), with: .color(colors.casing))
        context.fill(Path(CGRect(x: 10, y: 0, width: 8, height: bottom)), with: .color(colors.line))
    }
}

/// What sits on the strip in one row.
private enum MarkerStyle: Equatable {
    case stop(StopRows.Kind)
    case vehicle(StatusTone)
    /// A short column of dots, for the stops the folded row stands in for.
    case folded
}

/// The strip through the row and the mark on it, 28 pt wide with the mark's
/// centre 14 pt in.
private struct Marker: View {
    let style: MarkerStyle
    let strip: RouteStrip

    var body: some View {
        Canvas { context, size in
            strip.draw(in: &context, size: size)
            let centre = CGPoint(x: 14, y: size.height / 2)
            switch style {
            case .stop(.next):
                circles(&context, at: centre, [(11, .white), (9, .mapInk), (6, .mapNextStop)])
            case .stop(.last):
                circles(&context, at: centre, [(7, .mapInk), (4, .white)])
            case .stop(.passed), .stop(.upcoming):
                circles(&context, at: centre, [(6, .mapStopRing), (4, .white)])
            case .vehicle(let tone):
                circles(&context, at: centre, [(14, Color(.systemBackground)), (12, tone.text), (8, Color(.label))])
            case .folded:
                for offset in [-6.0, 0, 6] {
                    circles(&context, at: CGPoint(x: centre.x, y: centre.y + offset), [(1.5, Color(.systemBackground))])
                }
            }
        }
        .frame(width: 28)
        .accessibilityHidden(true)
    }

    /// Discs about `centre`, largest first, so each ring shows round the one
    /// inside it.
    private func circles(_ context: inout GraphicsContext, at centre: CGPoint, _ layers: [(radius: CGFloat, color: Color)]) {
        for (radius, color) in layers {
            let rect = CGRect(x: centre.x - radius, y: centre.y - radius, width: 2 * radius, height: 2 * radius)
            context.fill(Path(ellipseIn: rect), with: .color(color))
        }
    }
}

private struct StopRow: View, Equatable {
    let stop: TripStop
    let kind: StopRows.Kind
    let time: String
    let strip: RouteStrip

    var body: some View {
        let weight: Font.Weight = kind == .next ? .semibold : .regular
        HStack(spacing: 16) {
            Marker(style: .stop(kind), strip: strip)
            Text(stop.name)
                .font(.body.weight(weight))
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(time)
                .font(.body.weight(weight))
                .monospacedDigit()
                .foregroundStyle(kind == .next ? .primary : .secondary)
        }
        .frame(height: 46)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(TripStatusText.spokenStopRow(name: stop.name, time: time, kind: kind))
    }
}

/// The vehicle's row: its name, and how it is keeping to the schedule in the
/// status colour, exact minutes even inside the on-time window (P33).
private struct VehicleRow: View, Equatable {
    let name: String
    let status: String?
    let tone: StatusTone
    let strip: RouteStrip

    var body: some View {
        HStack(spacing: 16) {
            Marker(style: .vehicle(tone), strip: strip)
            Text(name)
                .font(.body.weight(.semibold))
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            if let status {
                Text(status)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(tone.text)
            }
        }
        .frame(height: 40)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(TripStatusText.spokenVehicleRow(name: name, status: status))
    }
}

/// Stands in for the passed stops above the last one passed; tapping it
/// shows them.
private struct FoldedRow: View {
    let count: Int
    @Binding var unfolded: Bool
    let strip: RouteStrip

    var body: some View {
        Button {
            withAnimation(.smooth(duration: 0.25)) { unfolded.toggle() }
        } label: {
            HStack(spacing: 16) {
                Marker(style: .folded, strip: strip)
                Text(TripStatusText.stopsPassed(count))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Image(systemName: "chevron.down")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .rotationEffect(.degrees(unfolded ? 180 : 0))
            }
            .frame(height: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(TripStatusText.stopsPassed(count))
        .accessibilityValue(unfolded ? TripStatusText.expanded : TripStatusText.collapsed)
    }
}
