import SwiftUI

/// The trips the driver has ended, newest first under their days, a page at a
/// time as the list ends (the design's sheet 10). Nothing opens from a row.
/// Android's `MyTripsScreen`.
struct MyTripsView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var list = MyTripsList()
    /// Bumped by Try again, so the load task runs again.
    @State private var loadRequest = 0
    @State private var refresh = TileRefresh()

    var body: some View {
        content
            .background(Color(.systemGroupedBackground))
            .navigationTitle(MyTripsText.title)
            .navigationBarTitleDisplayMode(typeSize.isAccessibilitySize ? .inline : .large)
            // One load per visit; pull to refresh and Try again load again on purpose.
            .task(id: loadRequest) {
                guard list.load == .loading else { return }
                await loadFirst()
            }
    }

    @ViewBuilder private var content: some View {
        switch list.load {
        case .loading:
            DelayedProgress()
        case .failed:
            EmptyState(symbol: "icloud.slash", title: MyTripsText.loadFailed, detail: PickerText.checkConnection,
                       action: PickerText.tryAgain) {
                list.reload()
                loadRequest += 1
            }
        case .loaded:
            if list.trips.isEmpty && !list.hasMore {
                // A pull still works here: a trip may have ended since.
                ScrollView {
                    VStack(spacing: 0) {
                        if list.refreshFailed { refreshBanner }
                        EmptyState(symbol: "clock.arrow.circlepath", title: MyTripsText.noTrips, detail: MyTripsText.noTripsDetail)
                    }
                    .containerRelativeFrame(.vertical, alignment: .top)
                }
                .refreshable { await refresh.run(loadFirst) }
            } else {
                trips
            }
        }
    }

    private var trips: some View {
        // Today becomes Yesterday on a page left open past midnight. Around
        // the whole list, so its rows stay lazy.
        TimelineView(EachMidnight()) { context in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if list.refreshFailed { refreshBanner }
                    ForEach(Array(MyTripsDay.of(list.trips, now: context.date).enumerated()), id: \.element.id) { index, day in
                        SettingsGroupTitle(title: day.title)
                            .padding(.top, index == 0 ? 8 : 28)
                        VStack(spacing: 0) {
                            ForEach(Array(day.trips.enumerated()), id: \.element.id) { index, trip in
                                if index > 0 { Divider().padding(.leading, 16) }
                                TripRow(row: MyTripRow(trip))
                            }
                        }
                        .settingsCard()
                    }
                    end
                }
                .padding(EdgeInsets(top: 0, leading: 16, bottom: 32, trailing: 16))
            }
            .refreshable { await refresh.run(loadFirst) }
        }
    }

    /// The pickers' spinner while another page loads, as the list reaches it.
    @ViewBuilder private var end: some View {
        if let page = list.nextPage {
            ProgressView()
                .frame(maxWidth: .infinity)
                .padding(.vertical, 20)
                .task(id: page) { await loadMore(page) }
        } else if list.moreFailed {
            AttentionBanner(symbol: "icloud.slash", text: MyTripsText.loadFailed, action: PickerText.tryAgain) {
                list.retryMore()
            }
            .padding(.top, 16)
        }
    }

    private var refreshBanner: some View {
        AttentionBanner(symbol: "icloud.slash", text: MyTripsText.loadFailed, action: PickerText.tryAgain,
                        busyLabel: refresh.isRunning ? PickerText.loading : nil) {
            Task { await refresh.run(loadFirst) }
        }
        .padding(.top, 8)
    }

    private func loadFirst() async {
        do {
            list.receivedFirst(try await session.myTrips(limit: MyTripsList.pageSize, offset: 0))
        } catch {
            // The view went away mid-load; the next visit loads afresh.
            guard !Task.isCancelled else { return }
            guard !session.handleIfUnauthorized(error) else { return }
            list.firstFailed()
        }
    }

    private func loadMore(_ page: MyTripsList.PageRequest) async {
        do {
            list.received(try await session.myTrips(limit: MyTripsList.pageSize, offset: page.offset), for: page)
        } catch {
            guard !Task.isCancelled else { return }
            guard !session.handleIfUnauthorized(error) else { return }
            list.failed(page)
        }
    }
}

/// When My trips' day titles are worked out: as the list shows, then at the
/// start of each day after, in the phone's time zone.
nonisolated struct EachMidnight: TimelineSchedule {
    var calendar: Calendar = .current

    func entries(from startDate: Date, mode: TimelineScheduleMode) -> UnfoldFirstSequence<Date> {
        // A day on, then back to that day's start, which is 1:00 on a day
        // whose clocks skip midnight.
        sequence(first: startDate) { [calendar] date in
            calendar.date(byAdding: .day, value: 1, to: date).map { calendar.startOfDay(for: $0) }
        }
    }
}

/// The route's badge, where the trip went, and when and on which vehicle.
private struct TripRow: View {
    let row: MyTripRow

    var body: some View {
        HStack(spacing: 14) {
            RouteBadge(shortName: row.badge, color: row.color, textColor: row.textColor, size: .row)
            VStack(alignment: .leading, spacing: 2) {
                if let title = row.title {
                    Text(title)
                }
                Text(row.detail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .frame(minHeight: 52)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(row.spoken)
    }
}
