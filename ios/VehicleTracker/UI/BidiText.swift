import Foundation

extension String {
    /// This text set apart from the sentence beside it, between Unicode's
    /// first strong isolate and pop directional isolate, so it keeps its own
    /// order: after Arabic, "12 Mile Rd" would otherwise show as "Mile Rd 12".
    /// String(localized:) does this itself for each value it puts in a
    /// sentence; this is for a name a view puts beside one, as Android's
    /// `isolated` is. The isolates take no space, so a name in the sentence's
    /// own direction looks the same.
    nonisolated var bidiIsolated: String {
        "\u{2068}\(self)\u{2069}"
    }
}
