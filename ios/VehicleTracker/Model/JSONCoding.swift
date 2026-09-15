import Foundation

/// The one decoder and encoder the app uses for the server's JSON. Dates are
/// RFC 3339 with an offset, which is what the server writes; every type spells
/// its own snake_case keys, so no key strategy is applied.
nonisolated enum JSONCoding {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()
}
