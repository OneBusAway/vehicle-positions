import CoreText
import Foundation
import Testing
@testable import VehicleTracker

/// The status card's "Next" line, laid out by Core Text as the screen lays it
/// out: how far from the left each part of it starts.
@Suite struct BidiTextTests {
    func left(of part: String, in line: String, rightToLeft: Bool) -> CGFloat {
        var direction: CTWritingDirection = rightToLeft ? .rightToLeft : .leftToRight
        let style = withUnsafeBytes(of: &direction) { bytes in
            var setting = CTParagraphStyleSetting(spec: .baseWritingDirection, valueSize: bytes.count, value: bytes.baseAddress!)
            return CTParagraphStyleCreate(&setting, 1)
        }
        let text = NSAttributedString(string: line, attributes: [NSAttributedString.Key(kCTParagraphStyleAttributeName as String): style])
        let index = (line as NSString).range(of: part).location
        return CTLineGetOffsetForStringIndex(CTLineCreateWithAttributedString(text), index, nil)
    }

    @Test func withoutTheIsolatesANumberLeavesTheNameItStarts() {
        let line = "التالية " + "12 Mile Rd"
        #expect(left(of: "Mile", in: line, rightToLeft: true) < left(of: "12", in: line, rightToLeft: true), "shows as Mile Rd 12")
    }

    @Test func anIsolatedNameKeepsItsOrderInArabic() {
        let line = "التالية " + "12 Mile Rd".bidiIsolated
        #expect(left(of: "12", in: line, rightToLeft: true) < left(of: "Mile", in: line, rightToLeft: true))
    }

    /// The run board's "first stop → last stop" and CarPlay's "time →
    /// headsign" point from the first to the second the way each language
    /// reads. English comes from the code, not from a translation.
    @Test func everyLanguagesArrowsPointTheWayItReads() throws {
        let languages = Bundle.main.localizations.filter { $0 != "Base" && $0 != Bundle.main.developmentLocalization }
        #expect(!languages.isEmpty)
        for language in languages {
            let bundle = try #require(Bundle.main.path(forResource: language, ofType: "lproj").flatMap(Bundle.init(path:)))
            let arrow = Locale.Language(identifier: language).characterDirection == .rightToLeft ? "←" : "→"
            for key in ["%@ → %@", "runs.departure"] {
                #expect(bundle.localizedString(forKey: key, value: nil, table: nil) == "%1$@ \(arrow) %2$@", "\(language): \(key)")
            }
        }
    }

    /// Sign-in's https line in Arabic. Written in the sentence, as it was,
    /// the slashes of https:// land on the far side of "https" and .local's
    /// dot after "local"; put in as values, each keeps its order.
    @Test func theHTTPSLineKeepsItsAddressesInOrderInArabic() throws {
        let written = "استخدم https://. لا يُسمح بـ http:// العادي إلا مع localhost أو 127.0.0.1 أو مضيف .local."
        #expect(left(of: "//", in: written, rightToLeft: true) < left(of: "https", in: written, rightToLeft: true))
        #expect(left(of: "local.", in: written, rightToLeft: true) < left(of: ".local", in: written, rightToLeft: true))

        let arabic = try #require(Bundle.main.path(forResource: "ar", ofType: "lproj").flatMap(Bundle.init(path:)))
        let line = SignInText.useHTTPS(in: arabic)
        #expect(line != SignInText.useHTTPS, "the Arabic line, not the English")
        #expect(left(of: "https", in: line, rightToLeft: true) < left(of: "//", in: line, rightToLeft: true))
        #expect(left(of: ".local", in: line, rightToLeft: true) < left(of: "local\u{2069}", in: line, rightToLeft: true))
    }

    @Test func theHTTPSLineReadsAsItDidInEnglish() {
        #expect(SignInText.useHTTPS == "Use https://. Plain http:// is allowed only for localhost, 127.0.0.1 or a .local host.")
    }

    @Test func anIsolatedNameLooksTheSameInEnglish() {
        let plain = "Next " + "12 Mile Rd"
        let isolated = "Next " + "12 Mile Rd".bidiIsolated
        for part in ["12", "Mile", "Rd"] {
            #expect(left(of: part, in: isolated, rightToLeft: false) == left(of: part, in: plain, rightToLeft: false), "\(part)")
        }
    }
}
