import SwiftUI

/// Screen 1's picture: a vehicle goes live and glides up its route, the
/// riders at the next stop turn and wave, the same vehicle shows on a
/// rider's phone, and the reports go out. The map is one `Canvas`, drawn
/// once; what moves sits above it and is moved by offset, scale and opacity
/// alone. VoiceOver reads the whole picture as one sentence.
struct RidersPicture: View {
    let beat: RidersBeat
    let isCompact: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// At a text size that leaves less room than this, the page scrolls.
    private static let minHeight: CGFloat = 320

    var body: some View {
        GeometryReader { proxy in
            let layout = RidersLayout(size: proxy.size, isCompact: isCompact)
            ZStack(alignment: .topLeading) {
                RidersMap(isCompact: isCompact)
                phoneVehicle(layout)
                riderBadge(layout)
                RidersChip(isLit: beat >= .ridersNotice, waving: beat.ridersWaving)
                    .frame(width: layout.chip.width, height: layout.chip.height)
                    .position(x: layout.chip.midX, y: layout.chip.midY)
                vehicle(layout)
                SentChip(count: beat.reportsSent)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
                    .padding(RidersLayout.inset)
            }
        }
        .frame(minHeight: Self.minHeight, maxHeight: .infinity)
        .clipShape(RoundedRectangle(cornerRadius: 28, style: .continuous))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(WelcomeText.ridersPicture)
    }

    /// The vehicle at the place it comes to rest, held down the route by an
    /// offset until it goes live. Its rings ride with it.
    private func vehicle(_ layout: RidersLayout) -> some View {
        let isLive = beat >= .live
        return ZStack {
            // A ring waits behind the marker, then grows out past it and fades.
            ForEach(0..<3, id: \.self) { ring in
                let hasLeft = beat.ringsLeft > ring
                VehicleRing()
                    .scaleEffect(hasLeft ? 2.7 : 0.55)
                    .opacity(hasLeft ? 0 : 0.55)
                    .animation(motion(.easeOut(duration: 1.5)), value: hasLeft)
            }
            ForEach(RidersLayout.stillRings, id: \.scale) { ring in
                VehicleRing()
                    .scaleEffect(ring.scale)
                    .opacity(beat >= .rest ? ring.opacity : 0)
                    .animation(motion(.smooth(duration: 0.4)), value: beat >= .rest)
            }
            VehicleMarker(isLive: isLive)
        }
        .frame(width: VehicleMarker.diameter, height: VehicleMarker.diameter)
        .geometryGroup()
        .position(x: layout.routeX, y: layout.vehicleRestY)
        .offset(y: isLive ? 0 : layout.vehicleStartY - layout.vehicleRestY)
        .animation(motion(RidersLayout.glide), value: isLive)
    }

    /// The same vehicle on the rider's phone: it appears a moment after the
    /// trip goes live and moves with the one on the route.
    private func phoneVehicle(_ layout: RidersLayout) -> some View {
        let isLive = beat >= .live
        let screen = layout.phoneScreen
        return Circle()
            .fill(.mapVehicleDisc)
            .strokeBorder(.statusOnTimeText, lineWidth: 4)
            .padding(1.5)
            .background(.mapVehicleEdge, in: Circle())
            .frame(width: 23, height: 23)
            .opacity(isLive ? 1 : 0)
            .animation(motion(WelcomeMotion.fade.delay(0.3)), value: isLive)
            .geometryGroup()
            .position(x: screen.midX, y: screen.minY + screen.height * RidersLayout.vehicleRest)
            .offset(y: isLive ? 0 : screen.height * (RidersLayout.vehicleStart - RidersLayout.vehicleRest))
            .animation(motion(RidersLayout.glide), value: isLive)
    }

    /// Whose phone it is: a rider's, on its top corner.
    private func riderBadge(_ layout: RidersLayout) -> some View {
        Image(systemName: "person.fill")
            .font(.system(size: 13, weight: .semibold))
            .foregroundStyle(.onAccentContainer)
            .frame(width: 28, height: 28)
            .background(.accentContainer, in: Circle())
            .position(x: layout.phoneScreen.minX, y: layout.phoneScreen.minY)
    }

    private func motion(_ animation: Animation) -> Animation? {
        reduceMotion ? nil : animation
    }
}

/// Where the pieces of the riders' picture sit. Everything is a share of the
/// card, so the picture stretches with the screen.
nonisolated struct RidersLayout {
    let size: CGSize
    let isCompact: Bool

    static let inset: CGFloat = 14
    /// Down the card: the next stop, the stop behind the vehicle, and where
    /// the vehicle starts and comes to rest.
    static let nextStop = 0.24
    static let passedStop = 0.68
    static let vehicleStart = 0.82
    static let vehicleRest = 0.50
    /// The vehicle's run up the route. A spring with no bounce: it pulls
    /// away at once and eases to a stop.
    static let glide = Animation.smooth(duration: 2.4)
    /// The rings that stay round the vehicle at rest, as shares of its size.
    static let stillRings: [(scale: CGFloat, opacity: Double)] = [(1.5, 0.45), (2.1, 0.28)]

    var routeX: CGFloat { size.width * 0.36 }
    var nextStopY: CGFloat { size.height * Self.nextStop }
    var passedStopY: CGFloat { size.height * Self.passedStop }
    var vehicleStartY: CGFloat { size.height * Self.vehicleStart }
    var vehicleRestY: CGFloat { size.height * Self.vehicleRest }

    /// The rider's phone, in the top trailing corner: 196 pt tall, or 128 pt
    /// on the smallest iPhone, and half as wide.
    var phone: CGRect {
        let height: CGFloat = isCompact ? 128 : 196
        return CGRect(x: size.width - Self.inset - height / 2, y: Self.inset, width: height / 2, height: height)
    }

    /// The phone's screen, inside its 5 pt body.
    var phoneScreen: CGRect { phone.insetBy(dx: 5, dy: 5) }

    /// The riders, beside the next stop: 40 pt tall, ending 22 pt short of
    /// the route, and no wider than the card leaves room for.
    var chip: CGRect {
        let trailing = routeX - 22
        let width = min(94, trailing - 4)
        return CGRect(x: trailing - width, y: nextStopY - 20, width: width, height: 40)
    }
}

/// The picture's map: land, blocks and roads, the route with its two stops,
/// and the rider's phone showing the same route. Nothing in it moves, so it
/// is drawn once and not again while the picture plays.
private struct RidersMap: View {
    let isCompact: Bool

    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        let colors = MapColors(dark: colorScheme == .dark)
        Canvas { context, size in
            let layout = RidersLayout(size: size, isCompact: isCompact)
            let routeX = layout.routeX
            // Two streets across, and one down each side of the route's.
            let upper = size.height * 0.40, lower = layout.passedStopY - 3.5
            let near = size.width * 0.09, far = size.width * 0.85
            context.fill(Path(CGRect(origin: .zero, size: size)), with: .color(colors.land))
            for (column, row, color) in [(0, 0, colors.block), (1, 0, colors.warmBlock), (0, 1, colors.block), (1, 1, colors.block)] {
                let x = column == 0 ? near + 18 : routeX + 12
                let width = column == 0 ? routeX - 16 - x : far - 6.5 - x
                let y = (row == 0 ? upper : lower) + 14
                let height = (row == 0 ? lower - 7 : size.height - 15) - y
                guard width > 0, height > 0 else { continue }
                context.fill(Path(roundedRect: CGRect(x: x, y: y, width: width, height: height), cornerRadius: 3), with: .color(color))
            }
            for y in [upper, lower] {
                context.fill(Path(CGRect(x: 0, y: y, width: size.width, height: 7)), with: .color(colors.road))
            }
            context.fill(Path(CGRect(x: far, y: 0, width: 7, height: size.height)), with: .color(colors.road))
            context.fill(Path(CGRect(x: near, y: upper, width: 7, height: size.height - upper)), with: .color(colors.road))

            // The route: the accent colour on an edge 35% darker.
            let edge = Path(CGRect(x: routeX - 5, y: 0, width: 10, height: size.height))
            context.fill(edge, with: .color(.accent))
            context.fill(edge, with: .color(.black.opacity(0.35)))
            context.fill(Path(CGRect(x: routeX - 3, y: 0, width: 6, height: size.height)), with: .color(.accent))

            // The stop behind the vehicle, then the next stop in its halo.
            Self.disc(&context, at: CGPoint(x: routeX, y: layout.passedStopY), radius: 6.5, .mapStopRing)
            Self.disc(&context, at: CGPoint(x: routeX, y: layout.passedStopY), radius: 4.5, .white)
            Self.disc(&context, at: CGPoint(x: routeX, y: layout.nextStopY), radius: 15.5, .white)
            Self.disc(&context, at: CGPoint(x: routeX, y: layout.nextStopY), radius: 13, .mapInk)
            Self.disc(&context, at: CGPoint(x: routeX, y: layout.nextStopY), radius: 10, .mapNextStop)

            // The rider's phone: its body with a soft shadow, then its map.
            let phone = layout.phone, screen = layout.phoneScreen
            context.drawLayer { layer in
                layer.addFilter(.shadow(color: .black.opacity(0.16), radius: 6, y: 2))
                layer.fill(Path(roundedRect: phone, cornerRadius: phone.width * 0.245, style: .continuous), with: .color(colors.phoneBody))
            }
            context.drawLayer { layer in
                layer.clip(to: Path(roundedRect: screen, cornerRadius: phone.width * 0.245 - 5, style: .continuous))
                layer.fill(Path(screen), with: .color(colors.land))
                layer.fill(Path(CGRect(x: screen.minX, y: screen.minY + screen.height * 0.62, width: screen.width, height: 5)), with: .color(colors.road))
                layer.fill(Path(CGRect(x: screen.midX - 2, y: screen.minY, width: 4, height: screen.height)), with: .color(.accent))
                let stop = CGPoint(x: screen.midX, y: screen.minY + screen.height * RidersLayout.nextStop)
                Self.disc(&layer, at: stop, radius: 6, .mapInk)
                Self.disc(&layer, at: stop, radius: 4, .mapNextStop)
            }
        }
    }

    private static func disc(_ context: inout GraphicsContext, at centre: CGPoint, radius: CGFloat, _ color: Color) {
        context.fill(Path(ellipseIn: CGRect(x: centre.x - radius, y: centre.y - radius, width: 2 * radius, height: 2 * radius)), with: .color(color))
    }
}

/// The picture's map, taken from the Tracking design. These are the only
/// colours in the welcome that are not the app's own.
private nonisolated struct MapColors {
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

/// A ring the size of the vehicle marker, in the colour of a trip that is
/// reporting. Scaled and faded by whoever draws it.
private struct VehicleRing: View {
    var body: some View {
        Circle()
            .strokeBorder(.statusOnTimeText, lineWidth: 2)
            .frame(width: VehicleMarker.diameter, height: VehicleMarker.diameter)
    }
}

/// The Tracking map's vehicle marker (`MapGlyphs.vehicle`): its disc and
/// arrow inside a status ring, grey until the trip is live and green after.
private struct VehicleMarker: View {
    let isLive: Bool

    static let diameter: CGFloat = 52

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ZStack {
            Circle().fill(.statusNoDataText)
            Circle().fill(.statusOnTimeText)
                .opacity(isLive ? 1 : 0)
                .animation(reduceMotion ? nil : WelcomeMotion.fade, value: isLive)
            Circle().fill(.mapVehicleDisc)
                .frame(width: 34, height: 34)
            VehicleArrow()
                .fill(.mapVehicleArrow)
                .stroke(.mapVehicleArrow, style: StrokeStyle(lineWidth: 1.5, lineJoin: .round))
                .frame(width: 34, height: 34)
        }
        .padding(2)
        .background(.mapVehicleEdge, in: Circle())
        .frame(width: Self.diameter, height: Self.diameter)
    }
}

/// Three riders at the stop. They stand in a plain chip until the vehicle is
/// on their map; then the chip lights up and each turns and waves in turn,
/// the figure changing with the symbol's own replace and lifting a little.
private struct RidersChip: View {
    let isLit: Bool
    let waving: Int

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 0) {
            ForEach(0..<3, id: \.self) { rider in
                Rider(isWaving: waving > rider)
                    .frame(maxWidth: .infinity)
            }
        }
        .foregroundStyle(isLit ? AnyShapeStyle(.onAccentContainer) : AnyShapeStyle(.secondary))
        .padding(.horizontal, 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background {
            Capsule().fill(Color(.secondarySystemGroupedBackground))
            Capsule().fill(.accentContainer)
                .opacity(isLit ? 1 : 0)
        }
        .animation(reduceMotion ? nil : WelcomeMotion.fade, value: isLit)
        .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
    }
}

private struct Rider: View {
    let isWaving: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        // Up and back down, once for each change: one small move of its own.
        PhaseAnimator([false, true], trigger: isWaving) { isLifted in
            Image(systemName: isWaving ? "figure.wave" : "figure.stand")
                .font(.system(size: 19, weight: .medium))
                .contentTransition(.symbolEffect(.replace))
                .offset(y: isLifted ? -5 : 0)
        } animation: { _ in
            reduceMotion ? nil : .bouncy(duration: 0.23)
        }
        .animation(reduceMotion ? nil : WelcomeMotion.fade, value: isWaving)
    }
}

/// The sent count, as the trip bar shows it: the reporting symbol and the
/// app's own "N sent". It rises in with the first report and counts up.
private struct SentChip: View {
    let count: Int

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        // The chip keeps "1 sent" while it is still unseen, so it never
        // counts up from nothing as it rises.
        let shown = max(count, 1)
        HStack(spacing: 6) {
            Image(systemName: "checkmark.icloud.fill")
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.secondary)
            Text(TripStatusText.sent(shown))
                .fontWeight(.semibold)
                .monospacedDigit()
                .contentTransition(.numericText(value: Double(shown)))
                .animation(reduceMotion ? nil : WelcomeMotion.count, value: shown)
        }
        .font(.subheadline)
        .padding(.horizontal, 14)
        .frame(minHeight: 44)
        .background(Color(.secondarySystemGroupedBackground), in: Capsule())
        .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
        .risesIn(count > 0, reduceMotion ? nil : WelcomeMotion.land(0.35))
        // The picture is a fixed size, so its words stop growing where they
        // would cover the route.
        .dynamicTypeSize(...DynamicTypeSize.accessibility1)
    }
}
