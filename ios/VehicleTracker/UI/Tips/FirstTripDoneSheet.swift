import SwiftUI

/// First trip done (walkthrough spec §8): a sheet at its smallest height,
/// on the pickers after the driver's first confirmed End trip, with what
/// the trip sent. The check bounces once and the phone taps success once
/// the sheet is up; with Reduce Motion nothing bounces.
struct FirstTripDoneSheet: View {
    let sent: Int

    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @ScaledMetric(relativeTo: .largeTitle) private var countSize = 44.0
    /// The content's height, which the sheet's one detent follows.
    @State private var height: CGFloat = 320
    /// The sheet has risen.
    @State private var isUp = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.title)
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(Color.onAccent, Color.accent)
                        .symbolEffect(.bounce, options: .nonRepeating, value: isUp)
                        .symbolEffectsRemoved(reduceMotion)
                        .accessibilityHidden(true)
                    Text(TipText.firstTripDone)
                        .font(.title2.bold())
                        .accessibilityAddTraits(.isHeader)
                }
                Text(count)
                    .padding(.horizontal, 18)
                    .padding(.vertical, 12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                Text(TipText.ridersCouldSeeYou)
                    .foregroundStyle(.secondary)
                WideButton(title: TipText.ok) { dismiss() }
                    .padding(.top, 4)
            }
            .padding(.horizontal, 20)
            .padding(.top, 24)
            .padding(.bottom, 8)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        }
        .scrollBounceBehavior(.basedOnSize)
        .presentationDetents([.height(height)])
        .sensoryFeedback(.success, trigger: isUp) { _, up in up }
        .task {
            // Once the sheet has risen, so the check's bounce is seen.
            do {
                try await Task.sleep(for: .milliseconds(400))
            } catch {
                return
            }
            isUp = true
        }
    }

    /// "142 reports sent" in one string, as the language puts it, with the
    /// number drawn large in the accent colour.
    private var count: AttributedString {
        var text = AttributedString(TipText.reportsSent(sent))
        text.font = .body
        text.foregroundColor = .secondary
        if let number = Self.countRange(sent, in: text) {
            text[number].font = .system(size: countSize, weight: .semibold).monospacedDigit()
            text[number].foregroundColor = .accent
        }
        return text
    }

    /// Where the count stands in the sentence, in the digits the sentence
    /// wrote it in: Egypt's Arabic writes 142 as ١٤٢ and Persian as ۱۴۲.
    nonisolated static func countRange(_ count: Int, in text: AttributedString, locale: Locale = .current) -> Range<AttributedString.Index>? {
        text.range(of: String(format: "%lld", locale: locale, count))
    }
}

/// On the pickers: puts First trip done up once they have faded in after
/// the trip, and tells Tracking's tip when they have gone. What it waits on
/// is read here, so the picker screens are not redrawn.
struct PickersTipHost: ViewModifier {
    @Environment(TripSession.self) private var session
    @State private var presented: DriverTips.FinishedTrip?

    func body(content: Content) -> some View {
        content
            .task(id: DriverTips.shared.finishedTrip) {
                guard let trip = DriverTips.shared.finishedTrip else { return }
                // RootView fades the pickers in over 0.35 s; the sheet rises
                // over a screen that has stopped moving.
                do {
                    try await Task.sleep(for: .milliseconds(500))
                } catch {
                    return
                }
                DriverTips.shared.finishedTripShown(settings: session.settings)
                presented = trip
            }
            .sheet(item: $presented, onDismiss: { DriverTips.shared.finishedTripClosed() }) { trip in
                FirstTripDoneSheet(sent: trip.sent)
            }
            .onAppear { DriverTips.shared.pickers(onScreen: true) }
            .onDisappear { DriverTips.shared.pickers(onScreen: false) }
    }
}
