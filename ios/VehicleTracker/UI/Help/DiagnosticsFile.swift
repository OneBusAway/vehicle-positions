import CoreLocation
import CoreTransferable
import OSLog
import UniformTypeIdentifiers

/// What Share diagnostics hands the share sheet: one small text file, read
/// and written when the app the driver picks asks for it, never while Help
/// draws. The app sends it nowhere itself.
nonisolated struct DiagnosticsFile: Transferable {
    /// The server's host, as Help's About row shows it.
    let host: String

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(exportedContentType: .utf8PlainText) { file in
            let grants = await LocationGrants.read()
            let url = try await file.write(grants: grants)
            return SentTransferredFile(url)
        }
    }

    /// Off the main thread: Apple warns that asking whether Location
    /// Services are on can block, and the log is read from the start of the
    /// app's run.
    @concurrent private func write(grants: LocationGrants) async throws -> URL {
        let device = DeviceFacts(
            version: AccountMenu.versionNumber(Bundle.main.infoDictionary),
            system: Self.systemVersion(),
            model: Self.modelIdentifier(),
            location: grants.location,
            precise: grants.location == .allowed ? grants.precise : nil,
            locationServicesOn: CLLocationManager.locationServicesEnabled()
        )
        let text = DiagnosticsReport.text(made: .now, timeZone: .current, device: device, host: host,
                                          errors: Self.recentErrors())
        let url = URL.temporaryDirectory.appending(path: DiagnosticsReport.fileName)
        try Data(text.utf8).write(to: url, options: .atomic)
        return url
    }

    /// "26.4", or "26.4.1" with a patch.
    private static func systemVersion() -> String {
        let os = ProcessInfo.processInfo.operatingSystemVersion
        return os.patchVersion == 0 ? "\(os.majorVersion).\(os.minorVersion)"
            : "\(os.majorVersion).\(os.minorVersion).\(os.patchVersion)"
    }

    /// "iPhone17,3". The simulator reports the Mac's processor, and names the
    /// iPhone it plays in its environment.
    private static func modelIdentifier() -> String {
        if let simulated = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] {
            return "\(simulated) (Simulator)"
        }
        var system = utsname()
        uname(&system)
        return withUnsafeBytes(of: &system.machine) { bytes in
            String(decoding: bytes.prefix { $0 != 0 }, as: UTF8.self)
        }
    }

    /// The app's newest error and fault lines since it opened, oldest first:
    /// iOS lets an app read only its own process's log. Nil when the log
    /// cannot be read. The store hands back its oldest line first even when
    /// asked for the newest, so all are read and the last kept.
    static func recentErrors() -> [LoggedError]? {
        do {
            let store = try OSLogStore(scope: .currentProcessIdentifier)
            let entries = try store.getEntries(matching: NSPredicate(format: "subsystem == %@", DiagnosticsReport.subsystem))
            var errors: [LoggedError] = []
            for case let entry as OSLogEntryLog in entries where entry.level == .error || entry.level == .fault {
                errors.append(LoggedError(entry))
            }
            return Array(errors.suffix(DiagnosticsReport.errorLimit))
        } catch {
            return nil
        }
    }
}

/// What iOS lets the app do with location, read on the main thread, where
/// the app's location manager lives.
nonisolated struct LocationGrants: Sendable {
    var location: DeviceFacts.LocationGrant
    var precise: Bool

    @MainActor static func read() -> LocationGrants {
        let manager = CLLocationManager()
        let location: DeviceFacts.LocationGrant = switch manager.authorizationStatus {
        case .authorizedWhenInUse, .authorizedAlways: .allowed
        case .denied: .notAllowed
        case .restricted: .restricted
        case .notDetermined: .notAskedYet
        @unknown default: .notAllowed
        }
        return LocationGrants(location: location, precise: manager.accuracyAuthorization == .fullAccuracy)
    }
}

nonisolated extension LoggedError {
    /// The line as the code wrote it: its words, and of its arguments only
    /// the strings it marked public.
    init(_ entry: OSLogEntryLog) {
        var pieces: [Piece] = []
        for component in entry.components {
            if !component.formatSubstring.isEmpty {
                pieces.append(.words(component.formatSubstring))
            }
            guard !component.placeholder.isEmpty else { continue }
            if component.placeholder.contains("public"), component.argumentCategory == .string,
               let argument = component.argumentStringValue {
                pieces.append(.shown(argument))
            } else {
                pieces.append(.hidden)
            }
        }
        self.init(date: entry.date, category: entry.category, pieces: pieces)
    }
}
