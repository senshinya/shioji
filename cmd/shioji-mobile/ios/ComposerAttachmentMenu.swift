import UIKit
import UniformTypeIdentifiers

final class ComposerAttachmentMenu: UIViewController, UIPopoverPresentationControllerDelegate {
    let surfaceColor: UIColor
    private var reply: PluginReply?
    private var loading = false

    init(appearance: UITraitCollection, reply: @escaping PluginReply) {
        surfaceColor = UIColor.secondarySystemBackground.resolvedColor(with: appearance)
        self.reply = reply
        super.init(nibName: nil, bundle: nil)
        modalPresentationStyle = .popover
        preferredContentSize = CGSize(width: 224, height: 146)
        pasteConfiguration = UIPasteConfiguration(acceptableTypeIdentifiers: [UTType.plainText.identifier, UTType.image.identifier])
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = surfaceColor
        let stack = UIStackView()
        stack.axis = .vertical
        stack.spacing = 2
        stack.translatesAutoresizingMaskIntoConstraints = false
        for (title, image, action) in [
            ("文件与 Skill 引用", "doc.text", "reference"),
            ("从文件选择附件", "folder", "file")
        ] {
            var style = UIButton.Configuration.plain()
            style.title = title
            if let symbol = UIImage(systemName: image, withConfiguration: UIImage.SymbolConfiguration(pointSize: 14, weight: .regular)) {
                let size = CGSize(width: 16, height: 16)
                let scale = min(size.width / symbol.size.width, size.height / symbol.size.height)
                let width = symbol.size.width * scale
                let height = symbol.size.height * scale
                style.image = UIGraphicsImageRenderer(size: size).image { _ in
                    symbol.draw(in: CGRect(x: (size.width - width) / 2, y: (size.height - height) / 2, width: width, height: height))
                }.withRenderingMode(.alwaysTemplate)
            }
            style.imagePadding = 4
            style.contentInsets = NSDirectionalEdgeInsets(top: 0, leading: 13, bottom: 0, trailing: 8)
            style.baseForegroundColor = .label
            let row = UIButton(configuration: style, primaryAction: UIAction { [weak self] _ in
                self?.finish(.success(["action": action]))
            })
            row.contentHorizontalAlignment = .leading
            row.heightAnchor.constraint(equalToConstant: 44).isActive = true
            stack.addArrangedSubview(row)
        }
        let configuration = UIPasteControl.Configuration()
        configuration.displayMode = .iconAndLabel
        configuration.imagePlacement = .leading
        configuration.baseForegroundColor = .label
        configuration.baseBackgroundColor = surfaceColor
        let pasteButton = UIPasteControl(configuration: configuration)
        pasteButton.accessibilityIdentifier = "composerClipboardPaste"
        pasteButton.target = self
        pasteButton.setContentHuggingPriority(.required, for: .horizontal)
        pasteButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        pasteButton.heightAnchor.constraint(equalToConstant: 44).isActive = true
        let pasteRow = UIStackView(arrangedSubviews: [pasteButton, UIView()])
        stack.addArrangedSubview(pasteRow)
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 5),
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 8),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -8)
        ])
    }

    func adaptivePresentationStyle(for controller: UIPresentationController) -> UIModalPresentationStyle { .none }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        let completion = reply
        reply = nil
        completion?(.success(nil))
    }

    override func paste(itemProviders: [NSItemProvider]) {
        guard !loading, reply != nil else { return }
        loading = true
        if let provider = itemProviders.first(where: { $0.canLoadObject(ofClass: NSString.self) }) {
            _ = provider.loadObject(ofClass: NSString.self) { [weak self] value, _ in
                DispatchQueue.main.async {
                    if let text = value as? String, !text.isEmpty {
                        self?.finish(.success(["action": "text", "text": text]))
                    } else {
                        self?.finish(.failure(PluginFailure("PasteFailed", "无法读取剪贴板文字")))
                    }
                }
            }
        } else if let provider = itemProviders.first(where: { $0.canLoadObject(ofClass: UIImage.self) }) {
            _ = provider.loadObject(ofClass: UIImage.self) { [weak self] value, _ in
                guard let image = value as? UIImage, let png = image.pngData(), png.count <= 8 * 1024 * 1024 else {
                    DispatchQueue.main.async {
                        self?.finish(.failure(PluginFailure("PasteFailed", "无法读取图片，或图片超过 8 MiB")))
                    }
                    return
                }
                let file = FileManager.default.temporaryDirectory.appendingPathComponent("shioji-paste-" + UUID().uuidString)
                do {
                    try png.write(to: file, options: [.atomic, .completeFileProtection])
                } catch {
                    DispatchQueue.main.async {
                        self?.finish(.failure(PluginFailure("PasteFailed", "无法暂存粘贴的图片")))
                    }
                    return
                }
                DispatchQueue.main.async {
                    guard let self, self.reply != nil else {
                        try? FileManager.default.removeItem(at: file)
                        return
                    }
                    self.finish(.success(["action": "image", "path": file.path]))
                }
            }
        } else {
            finish(.success(nil))
        }
    }

    private func finish(_ result: Result<Any?, PluginFailure>) {
        guard let completion = reply else { return }
        reply = nil
        dismiss(animated: true) { completion(result) }
    }
}
