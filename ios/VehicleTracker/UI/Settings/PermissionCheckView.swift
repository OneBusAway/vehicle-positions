import SwiftUI

/// Each thing the app needs to report a trip, whether iOS allows it, and what
/// fixes one it doesn't: the line of Settings' header, upright. It starts no
/// trip, and asks for nothing until its row is tapped. Android's
/// `PermissionCheckScreen`.
struct PermissionCheckView: View {
    let access: any LocationAccess

    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var check: PermissionCheck
    /// The row the vehicle is over, or on its way to.
    @State private var over: Int
    /// iOS's prompt is up: the row waits for its answer.
    @State private var asking = false

    init(access: any LocationAccess) {
        self.access = access
        let readiness = Readiness(access: access.current(), waiting: 0)
        _check = State(initialValue: PermissionCheck(readiness))
        _over = State(initialValue: PermissionCheck.waitsOver(readiness))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text(PermissionCheckText.intro)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 4)
                CheckCard(readiness: check.readiness, over: over) {
                    ForEach(LocationChecklist.Row.allCases, id: \.self) { row in
                        CheckRow(row: row, readiness: check.readiness, ticked: check.ticked.contains(row), asking: asking, onFix: fix)
                    }
                }
            }
            .padding(EdgeInsets(top: 4, leading: 16, bottom: 32, trailing: 16))
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(PermissionCheckText.title)
        // A large title is cut short at the largest sizes.
        .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
        // A driver back from the Settings app, or from iOS's own prompt, may
        // have changed the answer there.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active && !asking { read() }
        }
        // In an update of its own, or the rows' change would take the
        // drive's spring.
        .task(id: check.readiness) {
            let next = PermissionCheck.waitsOver(check.readiness)
            guard next != over else { return }
            do { try await Task.sleep(for: ReadinessMotion.waitToDrive) } catch { return }
            withAnimation(reduceMotion ? nil : ReadinessMotion.drive) { over = next }
        }
    }

    private func read() {
        let before = check.readiness
        let now = Readiness(access: access.current(), waiting: 0)
        guard now != before else { return }
        withAnimation(ChecklistMotion.change(reduceMotion: reduceMotion)) { check.read(now) }
        if let announcement = PermissionCheckText.announcement(from: before, to: now) {
            AccessibilityNotification.Announcement(announcement).post()
        }
    }

    private func fix(_ fix: Readiness.Fix) {
        switch fix {
        case .ask:
            guard !asking else { return }
            asking = true
            Task {
                _ = await access.request()
                asking = false
                read()
            }
        case .openSettings: openURL(.appSettings)
        case .readAgain: read()
        }
    }
}

/// The rows on a card, with the line through their markers: solid to the
/// vehicle, dashed after it, and solid all the way once the vehicle is by the
/// riders. The rows say the same, so the line is not read out.
private struct CheckCard<Rows: View>: View {
    let readiness: Readiness
    let over: Int
    @ViewBuilder let rows: Rows

    private static var vehicleSide: CGFloat { 34 }
    private static var lineWidth: CGFloat { 6 }
    /// The middle of the markers, from the card's leading edge.
    private static var middle: CGFloat { CheckRow.inset + ChecklistMarker.side / 2 }

    var body: some View {
        VStack(spacing: 0) {
            rows
            RidersRow(canReport: readiness.canReport)
        }
        .padding(.top, CheckPlaces.home)
        .padding(.bottom, 8)
        .backgroundPreferenceValue(CheckRowBounds.self) { bounds in
            GeometryReader { proxy in
                let places = CheckPlaces(rows: bounds.map { proxy[$0] })
                let vehicle = places.vehicle(over: over)
                let arrivesAt = readiness.canReport ? places.vehicle(over: readiness.stops.count) : .infinity
                ZStack {
                    CheckLineAhead(vehicle: vehicle, end: places.end, arrivesAt: arrivesAt)
                        .stroke(Color.secondary, style: StrokeStyle(lineWidth: 4, dash: [8, 6]))
                    CheckLineBehind(vehicle: vehicle, end: places.end, arrivesAt: arrivesAt)
                        .fill(.accent)
                }
                .frame(width: Self.lineWidth)
                .offset(x: Self.middle - Self.lineWidth / 2)
            }
        }
        .overlayPreferenceValue(CheckRowBounds.self) { bounds in
            GeometryReader { proxy in
                let places = CheckPlaces(rows: bounds.map { proxy[$0] })
                VehicleMark(ring: (readiness.canReport ? StatusTone.onTime : .neutral).text, side: Self.vehicleSide)
                    .rotationEffect(.degrees(180))
                    .offset(x: Self.middle - Self.vehicleSide / 2, y: places.vehicle(over: over) - Self.vehicleSide / 2)
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
        .background(Color(.secondarySystemGroupedBackground), in: .rect(cornerRadius: 26))
    }
}

/// Where the rows are laid out, top to bottom.
private struct CheckRowBounds: PreferenceKey {
    static var defaultValue: [Anchor<CGRect>] { [] }

    static func reduce(value: inout [Anchor<CGRect>], nextValue: () -> [Anchor<CGRect>]) {
        value += nextValue()
    }
}

/// Solid, from the top to the vehicle, and on to the riders once the vehicle
/// is by them.
nonisolated private struct CheckLineBehind: Shape {
    var vehicle: CGFloat
    let end: CGFloat
    let arrivesAt: CGFloat

    var animatableData: CGFloat {
        get { vehicle }
        set { vehicle = newValue }
    }

    func path(in rect: CGRect) -> Path {
        Path(CGRect(x: rect.minX, y: rect.minY, width: rect.width, height: max(vehicle.hasArrived(at: arrivesAt) ? end : vehicle, 0)))
    }
}

/// From the vehicle to the riders, until the vehicle is by them.
nonisolated private struct CheckLineAhead: Shape {
    var vehicle: CGFloat
    let end: CGFloat
    let arrivesAt: CGFloat

    var animatableData: CGFloat {
        get { vehicle }
        set { vehicle = newValue }
    }

    func path(in rect: CGRect) -> Path {
        var path = Path()
        guard !vehicle.hasArrived(at: arrivesAt), end > vehicle else { return path }
        path.move(to: CGPoint(x: rect.midX, y: rect.minY + vehicle))
        path.addLine(to: CGPoint(x: rect.midX, y: rect.minY + end))
        return path
    }
}

nonisolated private extension CGFloat {
    /// A little before its spring's last settling, so the line's end does not
    /// wait for it.
    func hasArrived(at place: CGFloat) -> Bool { self > place - 1 }
}

/// A row's marker and words, and its state or what fixes it, which goes under
/// the words where they would have less than `leastWords`. A row seen to
/// become allowed ticks, as on Before you start.
private struct CheckRow: View {
    let row: LocationChecklist.Row
    let readiness: Readiness
    let ticked: Bool
    let asking: Bool
    let onFix: (Readiness.Fix) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    static let inset: CGFloat = 16
    static let leastHeight: CGFloat = 96
    private static let leastWords: CGFloat = 170

    private var state: LocationChecklist.RowState { readiness.checklistState(of: row) }

    var body: some View {
        HStack(spacing: 16) {
            ChecklistMarker(look: ChecklistMarker.Look(stop: readiness.stop(for: row)), symbol: row.symbol)
                .animation(ChecklistMotion.tick(reduceMotion: reduceMotion), value: ticked)
                .symbolEffectsRemoved(reduceMotion)
                // Over the line, which its rings would let through.
                .background(Color(.secondarySystemGroupedBackground), in: .circle)
                .geometryGroup()
                // The words say it.
                .accessibilityHidden(true)
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 12) {
                    words.frame(minWidth: Self.leastWords, idealWidth: Self.leastWords, maxWidth: .infinity, alignment: .leading)
                    end
                }
                VStack(alignment: .leading, spacing: 8) {
                    words
                    end
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, Self.inset)
        .padding(.vertical, 8)
        .frame(minHeight: Self.leastHeight)
        .anchorPreference(key: CheckRowBounds.self, value: .bounds) { [$0] }
        .geometryGroup()
        .phaseAnimator([0, ChecklistMotion.dip(reduceMotion: reduceMotion)], trigger: ticked) { row, dip in
            row.offset(y: dip)
        } animation: { dip in
            dip == 0 ? ChecklistMotion.settle : ChecklistMotion.push
        }
    }

    private var words: some View {
        CheckWords(title: ChecklistText.title(of: row), detail: ChecklistText.reason(of: row, state))
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(ChecklistText.spoken(row, state))
    }

    @ViewBuilder private var end: some View {
        if let fix = readiness.fix(for: row) {
            Button(PermissionCheckText.name(of: fix)) { onFix(fix) }
                .buttonStyle(CheckButtonStyle())
                .accessibilityLabel(PermissionCheckText.spoken(fix, for: row))
                // Voice Control goes by what the driver sees.
                .accessibilityInputLabels([PermissionCheckText.name(of: fix)])
                .disabled(asking)
                .transition(.opacity)
        } else if state == .allowed {
            Text(ChecklistText.allowed)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                // The words say it.
                .accessibilityHidden(true)
                .transition(.opacity)
        }
    }
}

/// Where the line ends: whether riders can see the vehicle.
private struct RidersRow: View {
    let canReport: Bool

    var body: some View {
        HStack(spacing: 16) {
            ZStack {
                Circle().fill(.accentContainer).opacity(canReport ? 1 : 0)
                Circle().strokeBorder(Color(.separator), style: StrokeStyle(lineWidth: 2, dash: [4, 4])).opacity(canReport ? 0 : 1)
                Image(systemName: "figure.wave")
                    .resizable()
                    .scaledToFit()
                    .frame(width: 24, height: 24)
                    .foregroundStyle(canReport ? Color.onAccentContainer : .secondary)
            }
            .frame(width: ChecklistMarker.side, height: ChecklistMarker.side)
            .background(Color(.secondarySystemGroupedBackground), in: .circle)
            // The words say it.
            .accessibilityHidden(true)
            CheckWords(title: PermissionCheckText.riders,
                       detail: canReport ? PermissionCheckText.ridersSee : PermissionCheckText.ridersDoNotSee)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, CheckRow.inset)
        .padding(.vertical, 8)
        .frame(minHeight: CheckRow.leastHeight)
        .anchorPreference(key: CheckRowBounds.self, value: .bounds) { [$0] }
        .accessibilityElement(children: .combine)
    }
}

private struct CheckWords: View {
    let title: String
    let detail: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(.headline)
            Text(detail)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .fixedSize(horizontal: false, vertical: true)
    }
}

private struct CheckButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(.onAccentContainer)
            .padding(.horizontal, 18)
            .padding(.vertical, 8)
            .frame(minHeight: 44)
            .background(.accentContainer, in: .capsule)
            .opacity(configuration.isPressed || !isEnabled ? 0.6 : 1)
    }
}
