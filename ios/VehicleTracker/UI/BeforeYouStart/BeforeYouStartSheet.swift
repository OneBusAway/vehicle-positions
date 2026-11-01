import SwiftUI

/// Before you start (DESIGN-iOS §4.6): after the start confirmation, only
/// when location access is missing. It says what the trip needs and why
/// before iOS asks; Continue walks iOS's prompts while the sheet stays under
/// them, and once everything is allowed the sheet closes by itself and the
/// confirmed run starts.
struct BeforeYouStartSheet: View {
    let vehicleID: String
    let access: any LocationAccess
    let onReady: () -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @State private var checklist: LocationChecklist
    /// A prompt is up: Continue and the rows wait for its answer.
    @State private var asking = false

    init(vehicleID: String, access: any LocationAccess, onReady: @escaping () -> Void) {
        self.vehicleID = vehicleID
        self.access = access
        self.onReady = onReady
        _checklist = State(initialValue: LocationChecklist(access.current()))
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text(ChecklistText.message(vehicle: vehicleID))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 24)
                        .padding(.bottom, 20)
                    ForEach(LocationChecklist.Row.allCases, id: \.self) { row in
                        ChecklistRow(row: row, state: checklist.state(of: row), onAction: act)
                    }
                }
            }
            .navigationTitle(ChecklistText.title)
            .toolbar {
                if checklist.canCancel {
                    ToolbarItem(placement: .cancellationAction) {
                        Button(ChecklistText.cancel) { dismiss() }
                    }
                }
            }
            .safeAreaInset(edge: .bottom) {
                WideButton(title: ChecklistText.continue, action: askNext)
                    .disabled(checklist.continueAsk == nil || asking)
                    .padding(24)
            }
        }
        .interactiveDismissDisabled(!checklist.canCancel)
        // A driver back from Settings may have changed the answer there.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active && !asking { read() }
        }
        .onChange(of: checklist) { old, new in
            if new.isReady {
                onReady()
                return
            }
            for row in LocationChecklist.Row.allCases where old.state(of: row) != new.state(of: row) {
                AccessibilityNotification.Announcement(ChecklistText.spoken(row, new.state(of: row))).post()
            }
        }
    }

    private func askNext() {
        switch checklist.continueAsk {
        case .location: ask { checklist.read(await access.request()) }
        case .precise: ask { checklist.answeredPrecise(await access.requestPrecise()) }
        case nil: return
        }
    }

    private func act(_ action: LocationChecklist.RowAction) {
        switch action {
        case .openSettings: openURL(.appSettings)
        case .askPrecise: ask { checklist.answeredPrecise(await access.requestPrecise()) }
        case .readAgain: read()
        }
    }

    private func read() {
        checklist.read(access.current())
    }

    /// One prompt at a time; the sheet can't be left while it is up.
    private func ask(_ prompt: @escaping () async -> Void) {
        guard !asking else { return }
        asking = true
        Task {
            await prompt()
            asking = false
        }
    }
}
