import Foundation

/// What the agency's paperwork calls a run (Android's `RunLabel`): its run or
/// train `number` (GTFS's `trip_short_name`, "1503") and its `block`
/// (`block_id`, "599-20"), each "" when the feed gives none. On a frequent
/// route they tell apart runs whose times and headsigns look alike.
nonisolated struct RunLabel: Equatable {
    let number: String
    let block: String

    /// The line under a run on the board, "Run 1503 · Block 599-20": an id the
    /// feed leaves empty is left out, and a run with neither gets no line.
    var text: String? {
        let parts = [number.isEmpty ? nil : PickerText.runNumber(number),
                     block.isEmpty ? nil : PickerText.runBlock(block)].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

extension PickerText {
    nonisolated static func runNumber(_ number: String) -> String {
        String(localized: "Run \(number)", comment: "A run's number on the run list (Android's runs_row_run); the argument is the feed's trip_short_name, such as 1503.")
    }

    nonisolated static func runBlock(_ block: String) -> String {
        String(localized: "Block \(block)", comment: "A run's block on the run list (Android's runs_row_block); the argument is the feed's block_id, such as 599-20.")
    }
}
