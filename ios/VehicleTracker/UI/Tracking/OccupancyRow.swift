import SwiftUI

/// How full the vehicle is, as four buttons at the top of the pulled-up trip
/// sheet, big enough to hit at a stop. Tapping the selected one clears it.
/// VoiceOver reads the row as one choice, adjusted by swiping up and down.
struct OccupancyRow: View {
    let trip: TripGeometry
    var choice = OccupancyChoice.shared

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        let selected = choice.occupancy(tripID: trip.id, serviceDate: trip.serviceDate)
        VStack(alignment: .leading, spacing: 8) {
            Text(OccupancyText.title)
                .font(.subheadline)
                .foregroundStyle(.secondary)
            // Two by two at accessibility sizes, where four in a row would
            // not fit their words.
            Grid(horizontalSpacing: 8, verticalSpacing: 8) {
                ForEach(rows, id: \.self) { row in
                    GridRow {
                        ForEach(row, id: \.self) { value in
                            OccupancyButton(occupancy: value, isSelected: value == selected) {
                                choice.choose(value, tripID: trip.id, serviceDate: trip.serviceDate)
                            }
                        }
                    }
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 4)
        .padding(.bottom, 8)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(OccupancyText.title)
        .accessibilityValue(OccupancyText.value(selected))
        .accessibilityAdjustableAction { direction in
            choice.set(Occupancy.stepped(selected, up: direction == .increment), tripID: trip.id, serviceDate: trip.serviceDate)
        }
        .sensoryFeedback(.selection, trigger: selected)
    }

    private var rows: [[Occupancy]] {
        let all = Occupancy.allCases
        return dynamicTypeSize.isAccessibilitySize ? [Array(all[0..<2]), Array(all[2...])] : [all]
    }
}

/// One choice: its symbol over its words, on glass, or on the accent colour
/// while selected. The system glass button styles pad their label too much
/// for "Standing" to fit a quarter of the row without breaking the word.
private struct OccupancyButton: View {
    let occupancy: Occupancy
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: occupancy.symbol)
                    .font(.title3)
                Text(OccupancyText.name(occupancy))
                    .font(.subheadline.weight(.semibold))
                    .multilineTextAlignment(.center)
                    .lineLimit(2)
            }
            .padding(.horizontal, 4)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .frame(minHeight: 56)
            .foregroundStyle(isSelected ? Color.onAccent : Color.primary)
            .background(isSelected ? Color.accentColor : Color.clear, in: Self.shape)
            .glassEffect(isSelected ? .identity : .regular.interactive(), in: Self.shape)
            .contentShape(Self.shape)
        }
        .buttonStyle(.plain)
    }

    private static let shape = RoundedRectangle(cornerRadius: 16, style: .continuous)
}

extension Occupancy {
    /// More people the fuller it is.
    var symbol: String {
        switch self {
        case .manySeatsAvailable: "person.fill"
        case .fewSeatsAvailable: "person.2.fill"
        case .standingRoomOnly: "person.3.fill"
        case .full: "person.3.sequence.fill"
        }
    }
}

/// The occupancy row's words.
enum OccupancyText {
    static var title: String {
        String(localized: "How full is the vehicle?", comment: "Trip sheet: the heading over the occupancy buttons, and the row's VoiceOver label.")
    }

    static var notSet: String {
        String(localized: "Not set", comment: "VoiceOver value of the occupancy row when the driver has not chosen one.")
    }

    static func value(_ occupancy: Occupancy?) -> String {
        occupancy.map(name) ?? notSet
    }

    static func name(_ occupancy: Occupancy) -> String {
        switch occupancy {
        case .manySeatsAvailable:
            String(localized: "Seats free", comment: "Occupancy button: many seats are free.")
        case .fewSeatsAvailable:
            String(localized: "Few seats", comment: "Occupancy button: only a few seats are free.")
        case .standingRoomOnly:
            String(localized: "Standing", comment: "Occupancy button: no seats, standing room only.")
        case .full:
            String(localized: "Full", comment: "Occupancy button: the vehicle is full.")
        }
    }
}
