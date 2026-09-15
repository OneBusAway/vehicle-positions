import SwiftUI

/// The route's short name on its GTFS colour, as riders see it.
struct RouteBadge: View {
    var shortName: String
    var color: String
    var textColor: String

    var body: some View {
        Text(shortName.isEmpty ? "—" : shortName)
            .font(.title3.bold())
            .foregroundStyle(Color(hex: textColor) ?? .white)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(Color(hex: color) ?? .accentColor, in: RoundedRectangle(cornerRadius: 6))
    }
}
