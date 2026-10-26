import SwiftUI

/// A schedule status in Android's colours (`StatusTone` and `StatusColors` in
/// its `Color.kt`): `fill` behind `OnStatusFill` on a status card, `text` for
/// the status in words on an ordinary surface.
enum StatusTone {
    case onTime, late, early, neutral

    var fill: Color {
        switch self {
        case .onTime: .statusOnTimeFill
        case .late: .statusLateFill
        case .early: .statusEarlyFill
        case .neutral: .statusNoDataFill
        }
    }

    var text: Color {
        switch self {
        case .onTime: .statusOnTimeText
        case .late: .statusLateText
        case .early: .statusEarlyText
        case .neutral: .statusNoDataText
        }
    }
}

/// The colours of the banner across the top of the tracking screen. A
/// problem is ink, not red, because red already means early.
enum BannerTone {
    case reporting, neutral, problem, signedOut

    var background: Color {
        switch self {
        case .reporting: .statusOnTimeFill
        case .neutral: .statusNoDataFill
        case .problem: .problemContainer
        case .signedOut: .signedOutContainer
        }
    }

    var foreground: Color {
        switch self {
        case .reporting, .neutral: .onStatusFill
        case .problem: .problemContent
        case .signedOut: .signedOutContent
        }
    }
}
