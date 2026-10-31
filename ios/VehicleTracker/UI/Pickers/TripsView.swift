import SwiftUI

/// Choose a run (DESIGN-iOS §4.4): the route's runs today as a departure
/// board, in Android's words and order (`RunsScreen.kt`): big start times, a
/// switch between the route's two directions, a line at now, and the run
/// under way or next tagged. The header stays put; only the board scrolls,
/// and it opens at the run just gone, so the driver sees where the day is
/// without scrolling. A tap asks before the run starts, and a refused start
/// says what happened under the header, with the one action that fixes it.
struct TripsView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.openURL) private var openURL
    @Environment(\.dismiss) private var dismiss
    let vehicle: Vehicle
    let route: RouteInfo
    /// Change vehicle empties it, to land on the vehicle screen.
    @Binding var path: [PickerRoute]
    @State private var load: PickerLoad<RouteTripsPage> = .loading
    /// Bumped by Try again and Reload, so the load task runs again.
    @State private var loadRequest = 0
    /// The board shown, by index: the highlighted run's direction once the
    /// runs load, then whatever the switch is set to.
    @State private var direction = 0
    /// The boards that have scrolled to their opening run. A board is kept
    /// invisible until then, so it never shows its first run and then jumps.
    @State private var landed: Set<Int> = []
    /// The run waiting for the driver's confirmation.
    @State private var confirming: BoardRun?
    /// The run the driver confirmed last: the one a start is in flight for,
    /// and the one Try again starts without asking again.
    @State private var confirmed: BoardRun?
    @State private var refusal: StartRefusal?

    /// A start is in flight: this board's own, or one from the car, which
    /// must disable this board just the same.
    private var starting: Bool { session.phase.isStarting }

    var body: some View {
        content
            .navigationTitle(PickerText.runsTitle)
            .navigationSubtitle(PickerText.runsSubtitle(vehicle: vehicle.id, route: PickerText.name(of: route)))
            .navigationBarBackButtonHidden(starting)
            .toolbar { AccountMenu() }
            // One load per visit: Back finds the board where it was.
            .task(id: loadRequest) {
                guard case .loading = load else { return }
                await load()
            }
            .sensoryFeedback(.error, trigger: refusal) { old, new in new != nil && old != new }
    }

    @ViewBuilder private var content: some View {
        switch load {
        case .loading:
            DelayedProgress()
        case .failed:
            EmptyState(symbol: "icloud.slash", title: PickerText.runsLoadFailed, detail: PickerText.checkConnection,
                       action: PickerText.tryAgain, onAction: loadAgain)
        case .loaded(let page):
            if page.trips.isEmpty {
                EmptyState(symbol: "calendar.badge.exclamationmark", title: noRunsTitle, detail: PickerText.noRunsDetail,
                           action: PickerText.chooseAnotherRoute) { dismiss() }
            } else {
                // Runs go by while the driver looks at the board: each new
                // minute moves the line, the dimming and the Now or Next tag.
                TimelineView(.everyMinute) { context in
                    board(RunBoards.of(page, now: context.date), page: page)
                }
            }
        }
    }

    private var noRunsTitle: String {
        let number = route.shortName.trimmingCharacters(in: .whitespaces)
        return number.isEmpty ? PickerText.noRunsUnnamed : PickerText.noRuns(route: number)
    }

    /// The header stays put over the board as a top inset, so the rows scroll
    /// under it and the board's opening run lands right under it, while the
    /// title still collapses as the board scrolls. At accessibility sizes the
    /// header scrolls with the rows instead, so it cannot take the whole
    /// screen. Both directions stay alive, each with its own scroll
    /// position; the one not shown is hidden and cannot be hit.
    private func board(_ boards: RunBoards, page: RouteTripsPage) -> some View {
        // A reload can split the runs into fewer boards than the switch was set to.
        let selected = min(direction, boards.boards.count - 1)
        return ZStack {
            ForEach(boards.boards.indices, id: \.self) { index in
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            if dynamicTypeSize.isAccessibilitySize {
                                header(boards, selected: selected, page: page)
                            }
                            rows(boards.boards[index], page: page)
                        }
                    }
                    // After the update that made the rows, so they exist to
                    // scroll to: a scroll in that same update does nothing.
                    .task {
                        await Task.yield()
                        if let id = boards.boards[index].openAt { proxy.scrollTo(id, anchor: .top) }
                        landed.insert(index)
                    }
                }
                .opacity(index == selected && landed.contains(index) ? 1 : 0)
                .allowsHitTesting(index == selected)
                .accessibilityHidden(index != selected)
            }
        }
        .animation(reduceMotion ? nil : .easeInOut(duration: 0.2), value: selected)
        .safeAreaBar(edge: .top, spacing: 0) {
            if !dynamicTypeSize.isAccessibilitySize {
                header(boards, selected: selected, page: page)
            }
        }
    }

    @ViewBuilder private func rows(_ board: Board, page: RouteTripsPage) -> some View {
        ForEach(Array(board.runs.enumerated()), id: \.element.id) { index, run in
            if index == board.nowAt {
                NowLine()
            }
            RunRow(run: run, timezone: page.timezone, starting: starting && confirmed?.id == run.id,
                   dimmed: starting && confirmed?.id != run.id) { confirming = run }
                .equatable()
                .id(run.id)
                // On the row, so the dialog anchors to it on iPhone, where a
                // tap outside cancels and iOS shows no Cancel button.
                .confirmationDialog(PickerText.startTitle(time: Formatters.clock(run.startsAt, timezone: page.timezone)),
                                    isPresented: isConfirming(run), titleVisibility: .visible, presenting: run) { run in
                    Button(PickerText.startTrip) { start(run) }
                    Button(PickerText.cancel, role: .cancel) {}
                } message: { run in
                    Text(PickerText.startMessage(route: PickerText.name(of: route), headsign: run.headsign, vehicle: vehicle.id))
                }
        }
        if board.nowAt == board.runs.count {
            NowLine()
        }
    }

    private func isConfirming(_ run: BoardRun) -> Binding<Bool> {
        Binding(get: { confirming?.id == run.id }, set: { if !$0, confirming?.id == run.id { confirming = nil } })
    }

    private func header(_ boards: RunBoards, selected: Int, page: RouteTripsPage) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if let refusal {
                AttentionBanner(symbol: refusal.symbol, text: refusal.text, action: refusal.action.map(actionTitle)) {
                    act(on: refusal.action)
                }
                .padding(.bottom, 8)
            }
            if boards.boards.count > 1 {
                directionSwitch(boards)
            }
            Text(Formatters.serviceDay(page.serviceDate, timezone: page.timezone))
                .font(.subheadline.weight(.semibold))
                .accessibilityAddTraits(.isHeader)
            if let direction = boards.boards[selected].direction {
                Text(PickerText.stops(direction.firstStop, direction.lastStop))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Segments at the default sizes; a menu at accessibility sizes, where
    /// segments truncate and do not scale.
    @ViewBuilder private func directionSwitch(_ boards: RunBoards) -> some View {
        let picker = Picker(PickerText.directionTitle, selection: $direction) {
            ForEach(boards.boards.indices, id: \.self) { index in
                Text(PickerText.direction(boards.boards[index].direction?.headsign ?? "")).tag(index)
            }
        }
        if dynamicTypeSize.isAccessibilitySize {
            picker.pickerStyle(.menu)
        } else {
            picker.pickerStyle(.segmented)
        }
    }

    private func actionTitle(_ action: StartRefusal.Action) -> String {
        switch action {
        case .changeVehicle: PickerText.changeVehicle
        case .tryAgain: PickerText.tryAgain
        case .reload: PickerText.reload
        case .openSettings: PickerText.openSettings
        }
    }

    private func act(on action: StartRefusal.Action?) {
        switch action {
        case .changeVehicle:
            path = []
        case .tryAgain:
            // The driver confirmed this run already; it is not asked again.
            if let confirmed { start(confirmed) }
        case .reload:
            loadAgain()
        case .openSettings:
            openURL(.appSettings)
        case nil:
            return
        }
    }

    private func loadAgain() {
        load = .loading
        loadRequest += 1
    }

    private func load() async {
        do {
            let page = try await session.trips(routeID: route.id)
            direction = RunBoards.of(page, now: Date()).start
            landed = []
            // Whatever refused a start against the old list, such as a run
            // gone out of service, is not true of the new one.
            refusal = nil
            load = .loaded(page)
        } catch {
            // The view went away mid-load; the next visit loads afresh.
            guard !Task.isCancelled else { return }
            guard !session.handleIfUnauthorized(error) else { return }
            load = .failed(PickerLoadFailure(error))
        }
    }

    private func start(_ run: BoardRun) {
        confirmed = run
        refusal = nil
        // Not tied to this view on purpose: the session owns the start, and
        // cancelling it halfway could leave a trip open on the server.
        Task {
            do {
                try await session.start(vehicle: vehicle, tripID: run.id)
            } catch {
                guard !session.handleIfUnauthorized(error) else { return }
                let refusal = StartRefusal(error, vehicleID: vehicle.id)
                self.refusal = refusal
                // Read out when it appears, as Android's banner is.
                AccessibilityNotification.Announcement(refusal.text).post()
            }
        }
    }
}
