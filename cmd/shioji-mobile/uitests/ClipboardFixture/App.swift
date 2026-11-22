import UIKit

@main
final class ClipboardFixture: UIResponder, UIApplicationDelegate {
    func application(_ application: UIApplication, configurationForConnecting session: UISceneSession,
                     options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        let config = UISceneConfiguration(name: nil, sessionRole: session.role)
        config.delegateClass = ClipboardScene.self
        return config
    }
}

final class ClipboardScene: UIResponder, UIWindowSceneDelegate {
    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession, options: UIScene.ConnectionOptions) {
        guard let scene = scene as? UIWindowScene else { return }
        let controller = UIViewController()
        controller.view.backgroundColor = .systemBackground
        let buttons = UIStackView()
        buttons.axis = .vertical
        buttons.spacing = 12
        buttons.translatesAutoresizingMaskIntoConstraints = false
        func addButton(_ title: String, copy: @escaping () -> Void) {
            let button = UIButton(configuration: .bordered(), primaryAction: UIAction(title: title) { _ in copy() })
            buttons.addArrangedSubview(button)
        }
        addButton("Copy text") { UIPasteboard.general.string = "clipboard text" }
        addButton("Copy screenshot") {
            let format = UIGraphicsImageRendererFormat()
            format.scale = 1
            let rect = CGRect(x: 0, y: 0, width: 18, height: 12)
            UIPasteboard.general.image = UIGraphicsImageRenderer(size: rect.size, format: format).image { context in
                UIColor.systemRed.setFill()
                context.fill(rect)
            }
        }
        controller.view.addSubview(buttons)
        NSLayoutConstraint.activate([
            buttons.centerXAnchor.constraint(equalTo: controller.view.centerXAnchor),
            buttons.centerYAnchor.constraint(equalTo: controller.view.centerYAnchor)
        ])
        let window = UIWindow(windowScene: scene)
        window.rootViewController = controller
        window.makeKeyAndVisible()
        self.window = window
    }
}
