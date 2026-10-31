import SwiftUI

/// A picker's loading spinner, shown only once a load has taken 200 ms, so a
/// fast load goes straight to its content instead of flashing a spinner on the
/// way (Android's `DelayedSpinner`).
struct DelayedProgress: View {
    static let delay: Duration = .milliseconds(200)

    @State private var shown = false

    var body: some View {
        ProgressView()
            .opacity(shown ? 1 : 0)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .task {
                do {
                    try await Task.sleep(for: Self.delay)
                    shown = true
                } catch {
                    // Cancelled: the load finished inside the delay and this
                    // view is going away, so there is nothing to show.
                }
            }
    }
}
