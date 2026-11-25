import SwiftUI

/// What Tracking does during a trip, as the driver likes it: the display, the
/// map, and a vibration when reporting has a problem. Android's
/// `TripSettingsScreen`.
struct TripSettingsView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        @Bindable var settings = session.settings
        ScrollView {
            VStack(alignment: .leading, spacing: 26) {
                VStack(alignment: .leading, spacing: 0) {
                    SettingsGroupTitle(title: TripSettingsText.screen)
                    VStack(spacing: 0) {
                        ScreenRow(choice: .charging, chosen: $settings.screenOn)
                        Divider().padding(.leading, TripRow.inset)
                        ScreenRow(choice: .always, chosen: $settings.screenOn)
                    }
                    .settingsCard()
                }
                VStack(alignment: .leading, spacing: 0) {
                    SettingsGroupTitle(title: TripSettingsText.map)
                    MapCards(chosen: $settings.mapUp)
                }
                Toggle(isOn: $settings.vibratesOnProblem) {
                    TripRow.Words(title: TripSettingsText.vibrate, detail: TripSettingsText.vibrateDetail)
                }
                .tint(.accent)
                .modifier(TripRow())
                .settingsCard()
            }
            .padding(EdgeInsets(top: 8, leading: 16, bottom: 32, trailing: 16))
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(TripSettingsText.title)
        // A large title is cut short at the largest sizes.
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
    }
}

/// A row of the page: it has no symbol, so its words start by the group's
/// title.
private struct TripRow: ViewModifier {
    static let inset: CGFloat = 20

    func body(content: Content) -> some View {
        content
            .padding(.horizontal, Self.inset)
            .padding(.vertical, 11)
            .frame(minHeight: 60)
    }

    struct Words: View {
        let title: String
        let detail: String

        var body: some View {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                Text(detail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct ScreenRow: View {
    let choice: ScreenOn
    @Binding var chosen: ScreenOn

    private var isChosen: Bool { chosen == choice }

    var body: some View {
        Button { chosen = choice } label: {
            HStack(spacing: 14) {
                TripRow.Words(title: TripSettingsText.name(of: choice), detail: TripSettingsText.detail(of: choice))
                Image(systemName: "checkmark")
                    .fontWeight(.semibold)
                    .foregroundStyle(.accent)
                    .opacity(isChosen ? 1 : 0)
                    .accessibilityHidden(true)
            }
            .modifier(TripRow())
        }
        .buttonStyle(SettingsRowStyle())
        .accessibilityAddTraits(isChosen ? .isSelected : [])
    }
}

/// Side by side, or under each other where their words would be squeezed: at
/// 130% text and up, and on a window under 350 wide.
private struct MapCards: View {
    @Binding var chosen: MapUp

    @Environment(\.dynamicTypeSize) private var typeSize

    private static let gap: CGFloat = 12
    /// What a card has on a window 350 wide.
    private static let leastWidth: CGFloat = 153

    var body: some View {
        if typeSize >= .xxxLarge {
            stacked
        } else {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: Self.gap) {
                    ForEach(MapUp.allCases, id: \.self) { choice in
                        MapCard(choice: choice, chosen: $chosen)
                            .frame(minWidth: Self.leastWidth, idealWidth: Self.leastWidth, maxWidth: .infinity)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
                stacked
            }
        }
    }

    private var stacked: some View {
        VStack(spacing: Self.gap) {
            ForEach(MapUp.allCases, id: \.self) { MapCard(choice: $0, chosen: $chosen) }
        }
    }
}

/// Its picture says what its words do, so only the words are read out.
private struct MapCard: View {
    let choice: MapUp
    @Binding var chosen: MapUp

    private static let shape = RoundedRectangle(cornerRadius: 22, style: .continuous)

    private var isChosen: Bool { chosen == choice }

    var body: some View {
        Button { chosen = choice } label: {
            VStack(spacing: 0) {
                MapPicture(up: choice)
                    .frame(height: 112)
                    .accessibilityHidden(true)
                HStack(spacing: 10) {
                    Text(TripSettingsText.name(of: choice))
                        .multilineTextAlignment(.leading)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Image(systemName: isChosen ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 22))
                        .foregroundStyle(isChosen ? AnyShapeStyle(.accent) : AnyShapeStyle(.secondary))
                        .accessibilityHidden(true)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .frame(minHeight: 56, maxHeight: .infinity)
            }
        }
        .buttonStyle(SettingsRowStyle())
        .background(Color(.secondarySystemGroupedBackground))
        .clipShape(Self.shape)
        .overlay { Self.shape.strokeBorder(.accent, lineWidth: isChosen ? 2 : 0) }
        .accessibilityAddTraits(isChosen ? .isSelected : [])
    }
}

/// The same streets and route as the map would show them: turned to the way
/// the vehicle goes, or north up.
private struct MapPicture: View {
    let up: MapUp

    @Environment(\.colorScheme) private var colorScheme

    /// How far round the pictured route runs from north, in degrees.
    private static let routeFromNorth = 40.0
    private static let streetGap: CGFloat = 38
    private static let streetWidth: CGFloat = 6
    private static let vehicleSide: CGFloat = 34

    /// The way the vehicle points on the screen.
    private var pointing: Double { up == .north ? Self.routeFromNorth : 0 }

    private func vehicle(in size: CGSize) -> CGPoint {
        CGPoint(x: size.width / 2, y: size.height * 0.62)
    }

    var body: some View {
        let colors = MapColors(dark: colorScheme == .dark)
        Canvas { context, size in
            let reach = size.width + size.height
            context.fill(Path(CGRect(origin: .zero, size: size)), with: .color(colors.land))
            let centre = vehicle(in: size)
            context.translateBy(x: centre.x, y: centre.y)
            var streets = context
            streets.rotate(by: .degrees(pointing - Self.routeFromNorth))
            for away in stride(from: Self.streetGap / 2, to: reach, by: Self.streetGap) {
                for middle in [away, -away] {
                    let edge = middle - Self.streetWidth / 2
                    streets.fill(Path(CGRect(x: edge, y: -reach, width: Self.streetWidth, height: 2 * reach)), with: .color(colors.road))
                    streets.fill(Path(CGRect(x: -reach, y: edge, width: 2 * reach, height: Self.streetWidth)), with: .color(colors.road))
                }
            }
            // The route: the accent colour on an edge 35% darker, as the
            // welcome draws it.
            context.rotate(by: .degrees(pointing))
            let edge = Path(CGRect(x: -5.5, y: -reach, width: 11, height: 2 * reach))
            context.fill(edge, with: .color(.accent))
            context.fill(edge, with: .color(.black.opacity(0.35)))
            context.fill(Path(CGRect(x: -4, y: -reach, width: 8, height: 2 * reach)), with: .color(.accent))
        }
        .overlay {
            GeometryReader { proxy in
                VehicleMark(ring: StatusTone.onTime.text, side: Self.vehicleSide)
                    .rotationEffect(.degrees(pointing))
                    .position(vehicle(in: proxy.size))
            }
        }
        // North and east are where they are in every language.
        .environment(\.layoutDirection, .leftToRight)
    }
}
