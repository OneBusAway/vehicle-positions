import OSLog
import SwiftUI

/// Resume your trip? (DESIGN-iOS §4.8): at launch, for a trip the app closed
/// during, in place of a paused Tracking screen. It asks the one question:
/// keep reporting, or end the trip. An end the server doesn't confirm is said
/// here, under the card, and the buttons then offer to try again or end it on
/// this phone only; a third goes back, for a driver who didn't mean to end it.
struct ResumeView: View {
    @Environment(TripSession.self) private var session
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// The trip shown, kept while the screen fades out after an end.
    @State private var trip: ActiveTrip?
    @State private var endNotConfirmed = false
    private let log = Logger(subsystem: "org.onebusaway.vehicletracker", category: "resume")

    private var isEnding: Bool { session.phase.isEnding }

    var body: some View {
        GeometryReader { proxy in
            // One scroll for the page: the buttons sit at the bottom, and a
            // large text size scrolls them into reach.
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    // Each part keeps its full height; the page scrolls
                    // rather than cutting the headsign short.
                    VStack(alignment: .leading, spacing: 0) {
                        Text(ResumeText.title)
                            .font(.largeTitle.bold())
                            .accessibilityAddTraits(.isHeader)
                        Text(ResumeText.message)
                            .foregroundStyle(.secondary)
                            .padding(.top, 8)
                        if let trip {
                            ResumeTripCard(card: ResumeCard(trip))
                                .padding(.top, 28)
                        }
                        if endNotConfirmed {
                            AttentionBanner(symbol: "icloud.slash.fill", text: ResumeText.endNotConfirmed)
                                .padding(.top, 20)
                                .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
                        }
                    }
                    .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 24)
                    buttons
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(24)
                .frame(minHeight: proxy.size.height, alignment: .top)
            }
            // There is no bar over the page, so the title fades under the
            // status bar as it scrolls, rather than running into the clock.
            .scrollEdgeEffectStyle(.soft, for: .top)
        }
        .background(Color(.systemGroupedBackground))
        .onChange(of: session.activeTrip, initial: true) { _, active in
            if let active { trip = active }
        }
        .sensoryFeedback(.error, trigger: endNotConfirmed) { _, failed in failed }
    }

    @ViewBuilder private var buttons: some View {
        VStack(spacing: 12) {
            if endNotConfirmed {
                WideButton(title: TripStatusText.tryAgain, action: end)
                WideButton(title: TripStatusText.endOnThisPhone, style: .plain) { session.endLocally() }
                Button {
                    animated { endNotConfirmed = false }
                } label: {
                    Text(ResumeText.dontEnd)
                        .font(.headline)
                        .frame(maxWidth: .infinity, minHeight: 48)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            } else {
                WideButton(title: ResumeText.resumeTrip) { session.resume() }
                    .disabled(isEnding)
                // No confirmation, as on Android: this screen asked already.
                WideButton(title: ResumeText.endTrip, busyTitle: isEnding ? ResumeText.ending : nil, style: .plain, action: end)
            }
        }
    }

    private func end() {
        // A second tap while the end is out does nothing.
        guard !isEnding else { return }
        animated { endNotConfirmed = false }
        // Not tied to this view: the session owns the end, which goes on
        // whether or not the screen does.
        Task {
            do {
                try await session.end()
            } catch {
                log.error("the server did not confirm the end of the trip: \(String(describing: error))")
                animated { endNotConfirmed = true }
                // Read out when it appears, as Android's banner is.
                AccessibilityNotification.Announcement(ResumeText.endNotConfirmed).post()
            }
        }
    }

    private func animated(_ change: () -> Void) {
        withAnimation(reduceMotion ? nil : .smooth(duration: 0.25), change)
    }
}
