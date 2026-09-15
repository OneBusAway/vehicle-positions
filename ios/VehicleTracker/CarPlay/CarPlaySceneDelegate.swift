import CarPlay
import UIKit

/// Named in Info.plist's scene manifest; CarPlay instantiates it when the car
/// connects. Navigation apps get the window as well as the interface
/// controller, and hand both to the controller for the life of the session.
final class CarPlaySceneDelegate: UIResponder, CPTemplateApplicationSceneDelegate {
    private var controller: CarPlayController?

    func templateApplicationScene(_ scene: CPTemplateApplicationScene, didConnect interfaceController: CPInterfaceController, to window: CPWindow) {
        let controller = CarPlayController(session: AppContainer.shared.session, interface: interfaceController, window: window)
        self.controller = controller
        controller.start()
        controller.contentStyleDidChange(scene.contentStyle)
    }

    func templateApplicationScene(_ scene: CPTemplateApplicationScene, didDisconnect interfaceController: CPInterfaceController, from window: CPWindow) {
        controller?.stop()
        controller = nil
    }

    func contentStyleDidChange(_ contentStyle: UIUserInterfaceStyle) {
        controller?.contentStyleDidChange(contentStyle)
    }
}
