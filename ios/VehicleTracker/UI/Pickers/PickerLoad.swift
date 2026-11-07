import Foundation

/// Why a picker's list could not be loaded, in the two terms the screens tell
/// apart. Only Choose a route shows `noSchedule`; the other screens say
/// "Couldn't load" for either, as Android's do.
nonisolated enum PickerLoadFailure: Equatable {
    /// The server could not be reached, or answered with an error a retry
    /// may clear.
    case unreachable
    /// The server has no schedule to offer: a 503 while its index loads, or a
    /// 404 from a server whose catalog was never registered (Android's
    /// `CatalogUnavailable`). A retry will not produce one.
    case noSchedule

    init(_ error: any Error) {
        switch error {
        case APIError.status(503, _), APIError.status(404, _): self = .noSchedule
        default: self = .unreachable
        }
    }
}

/// One shape for every picker's load, in place of separate error and loaded
/// flags on each screen.
nonisolated enum PickerLoad<Value: Equatable>: Equatable {
    case loading
    case loaded(Value)
    case failed(PickerLoadFailure)
}
