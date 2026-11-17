import Foundation

// What Share diagnostics sends: the app, the iPhone, the server, what the app
// may do and its recent errors, for whoever looks after the app. It stays in
// English whatever the phone's language, so that person can read it, as
// Android's does.

/// What the iPhone says about itself and this build. Nothing in it names the
/// driver, the vehicle or where it is.
nonisolated struct DeviceFacts: Equatable, Sendable {
    enum LocationGrant: Equatable, Sendable {
        case allowed, notAllowed, notAskedYet, restricted
    }

    /// "1.0.0 (1)", as Help's About row shows it.
    var version: String
    /// "26.4".
    var system: String
    /// The model's identifier, "iPhone17,3".
    var model: String
    var location: LocationGrant
    /// Nil until location is allowed: iOS says nothing about precision
    /// before then that a trip could use.
    var precise: Bool?
    var locationServicesOn: Bool
}

/// One error line the app logged, as Share diagnostics may send it: the
/// words written in the code, each argument the app marked public (#252), and
/// nothing of the rest.
nonisolated struct LoggedError: Equatable, Sendable {
    enum Piece: Equatable, Sendable {
        /// Words from the code's format string.
        case words(String)
        /// An argument logged with `privacy: .public`: an error's description
        /// or the server's host.
        case shown(String)
        /// Any other argument: a vehicle, route or trip id, or a number. Left
        /// out, as the phone leaves it out of a log read off it.
        case hidden
    }

    var date: Date
    /// The logger's category, "trip-session".
    var category: String
    var pieces: [Piece]
}

nonisolated enum DiagnosticsReport {
    static let title = "Vehicle Tracker diagnostics"
    static let fileName = "\(title).txt"
    /// The app logs under this subsystem, in every file that logs.
    static let subsystem = "org.onebusaway.vehicletracker"
    static let errorLimit = 50
    /// One line each, cut here: a system error's description can run to
    /// several hundred characters of keys and addresses.
    static let lineLimit = 300

    /// The file's text. `errors` is nil when the log could not be read, and
    /// holds the newest `errorLimit` at most, oldest first.
    static func text(made: Date, timeZone: TimeZone, device: DeviceFacts, host: String, errors: [LoggedError]?) -> String {
        var lines = [
            title,
            "Made: \(stamp(made, timeZone: timeZone, seconds: false))",
            "App: \(device.version)",
            "iOS: \(device.system)",
            "iPhone: \(device.model)",
            "Server: \(host.isEmpty ? "none" : host)",
        ]
        // The rows of "Before you start", under its names.
        lines += LocationChecklist.Row.allCases.map { row in
            switch row {
            case .location: "Location: \(grant(device.location))"
            case .precise: "Precise location: \(device.precise.map { $0 ? "allowed" : "not allowed" } ?? "n/a")"
            case .locationServices: "Location services: \(device.locationServicesOn ? "on" : "off")"
            }
        }
        guard let errors else {
            lines.append("Errors: the app's log could not be read")
            return lines.joined(separator: "\n") + "\n"
        }
        let recent = errors.suffix(errorLimit)
        lines.append("Errors since the app opened: \(recent.isEmpty ? "none" : String(recent.count))")
        lines += recent.map { line($0, timeZone: timeZone) }
        return lines.joined(separator: "\n") + "\n"
    }

    /// "2026-11-17 01:02:03 +05:30 trip-session: could not load the vehicles: …"
    static func line(_ error: LoggedError, timeZone: TimeZone) -> String {
        let message = error.pieces.map { piece in
            switch piece {
            case .words(let words): words
            case .shown(let argument): scrub(argument)
            case .hidden: "<private>"
            }
        }.joined()
        let flat = "\(stamp(error.date, timeZone: timeZone, seconds: true)) \(error.category): \(message)"
            .split(whereSeparator: \.isNewline).joined(separator: " ")
        return flat.count > lineLimit ? String(flat.prefix(lineLimit - 1)) + "…" : flat
    }

    /// What a public argument can still carry that the file must not: a
    /// URL's path and query (a trip or vehicle id), an email, a token, or a
    /// position. A URL keeps only its scheme and host: the host is the
    /// server's, already in the file.
    static func scrub(_ text: String) -> String {
        text
            .replacing(#/[A-Za-z][A-Za-z0-9+.\-]*://[^\s"'<>(){}\[\],;]+/#) { match in
                let url = URLComponents(string: String(match.output))
                guard let scheme = url?.scheme, let host = url?.host, !host.isEmpty else { return "<url>" }
                return "\(scheme)://\(host)"
            }
            .replacing(#/[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}/#, with: "<email>")
            .replacing(#/(?i:bearer)\s+[A-Za-z0-9._~+\/=\-]+/#, with: "Bearer <token>")
            .replacing(#/eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+(\.[A-Za-z0-9_\-]*)?/#, with: "<token>")
            // A long run of letters and digits mixed: a refresh token, a key.
            // Error descriptions' own words have no digits in them.
            .replacing(#/[A-Za-z0-9+/_=\-]{20,}/#) { match in
                let run = match.output
                return run.contains(where: \.isNumber) && run.contains(where: \.isLetter) ? "<token>" : String(run)
            }
            // A latitude or a longitude: degrees with three or more decimals.
            // A run with more dots is an address or a version, and stays.
            .replacing(#/-?\d+(?:\.\d+)+/#) { match in
                let parts = match.output.split(separator: ".")
                let isDegrees = parts.count == 2 && parts[0].count(where: \.isNumber) <= 3 && parts[1].count >= 3
                return isDegrees ? "<number>" : String(match.output)
            }
    }

    private static func grant(_ grant: DeviceFacts.LocationGrant) -> String {
        switch grant {
        case .allowed: "allowed"
        case .notAllowed: "not allowed"
        case .notAskedYet: "not asked yet"
        case .restricted: "restricted"
        }
    }

    private static func stamp(_ date: Date, timeZone: TimeZone, seconds: Bool) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timeZone
        formatter.dateFormat = seconds ? "yyyy-MM-dd HH:mm:ss xxx" : "yyyy-MM-dd HH:mm xxx"
        return formatter.string(from: date)
    }
}
