import SwiftUI
import UIKit

/// Shows the phone's window in the theme chosen in Settings, and with it all
/// that comes up over it: sheets, alerts, menus, the keyboard, the share
/// sheet and the map. SwiftUI's preferredColorScheme would leave out what
/// UIKit puts up. The car's window is in a scene of its own and keeps the
/// car's.
struct WindowTheme: UIViewRepresentable {
    let choice: ThemeChoice

    func makeUIView(context: Context) -> WindowThemeView {
        WindowThemeView(style: choice.style)
    }

    func updateUIView(_ view: WindowThemeView, context: Context) {
        view.style = choice.style
    }
}

/// As it joins its window, before the first frame, and as the choice changes.
final class WindowThemeView: UIView {
    var style: UIUserInterfaceStyle {
        didSet { window?.overrideUserInterfaceStyle = style }
    }

    init(style: UIUserInterfaceStyle) {
        self.style = style
        super.init(frame: .zero)
        isUserInteractionEnabled = false
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("made in code only")
    }

    override func willMove(toWindow window: UIWindow?) {
        super.willMove(toWindow: window)
        window?.overrideUserInterfaceStyle = style
    }
}

extension ThemeChoice {
    /// Unspecified leaves it to the phone.
    nonisolated var style: UIUserInterfaceStyle {
        switch self {
        case .system: .unspecified
        case .light: .light
        case .dark: .dark
        }
    }
}
