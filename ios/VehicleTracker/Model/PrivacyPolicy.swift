import Foundation

nonisolated enum PrivacyPolicy {
    /// `string` as a link the browser opens as it is: https, with a host. Nil
    /// for anything else.
    static func httpsLink(_ string: String) -> URL? {
        guard let url = URL(string: string), url.scheme?.lowercased() == "https", let host = url.host(), !host.isEmpty else { return nil }
        return url
    }
}
