import SwiftUI
import UIKit

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

    /// `text` for UIKit, where the map draws the ring round the vehicle.
    var uiText: UIColor {
        switch self {
        case .onTime: .statusOnTimeText
        case .late: .statusLateText
        case .early: .statusEarlyText
        case .neutral: .statusNoDataText
        }
    }
}
