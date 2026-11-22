import UIKit

final class RowContextMenus: NSObject, UIContextMenuInteractionDelegate {
    private struct Command {
        let id: String
        let title: String
        let symbol: String?
        let attributes: UIMenuElement.Attributes

        init?(_ data: [String: Any]) {
            guard let id = data["id"] as? String, let title = data["title"] as? String else { return nil }
            self.id = id
            self.title = title
            symbol = data["symbol"] as? String
            var attributes: UIMenuElement.Attributes = []
            if data["destructive"] as? Bool == true { attributes.insert(.destructive) }
            if data["disabled"] as? Bool == true { attributes.insert(.disabled) }
            self.attributes = attributes
        }
    }

    private struct Row {
        let key: String
        let rect: CGRect
        let commands: [Command]
        let preview: Bool

        init?(_ data: [String: Any]) {
            guard let key = data["key"] as? String,
                  let rect = data["rect"] as? [Double], rect.count == 4 else { return nil }
            self.key = key
            self.rect = CGRect(x: rect[0], y: rect[1], width: rect[2], height: rect[3])
            commands = (data["commands"] as? [[String: Any]] ?? []).compactMap(Command.init)
            preview = data["preview"] as? Bool ?? false
        }
    }

    private let window: Int
    private var sequence = 0
    private var rows: [Row] = []
    private var active: Row?
    private var picked: (key: String, action: String)?
    private var conversationPreview: ConversationContextPreview?

    init(view: UIView, window: Int) {
        self.window = window
        super.init()
        view.addInteraction(UIContextMenuInteraction(delegate: self))
    }

    func update(sequence: Int, rows: [[String: Any]]) {
        guard sequence > self.sequence else { return }
        self.sequence = sequence
        self.rows = rows.compactMap(Row.init)
    }

    func mountPreview(_ child: UIViewController, request: String) {
        guard let preview = conversationPreview, preview.request == request else { return }
        preview.mount(child)
    }

    func fitPreview(height: CGFloat, request: String) {
        guard let preview = conversationPreview, preview.request == request else { return }
        preview.fit(height: height)
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                configurationForMenuAtLocation location: CGPoint) -> UIContextMenuConfiguration? {
        guard let view = interaction.view, view.safeAreaLayoutGuide.layoutFrame.contains(location),
              let row = rows.first(where: { $0.rect.contains(location) }) else { return nil }
        active = row
        picked = nil
        let provider: UIContextMenuContentPreviewProvider? = row.preview ? { [weak self, weak view] in
            guard let self, let view else { return nil }
            let viewport = view.window?.bounds ?? view.bounds
            let preview = ConversationContextPreview(size: CGSize(
                width: min(370, viewport.width - 32), height: viewport.height * 2 / 3))
            self.conversationPreview = preview
            self.emit(key: row.key, action: "preview", request: preview.request)
            return preview
        } : nil
        return UIContextMenuConfiguration(identifier: row.key as NSString, previewProvider: provider) { [weak self] _ in
            UIMenu(children: row.commands.map { command in
                UIAction(title: command.title, image: command.symbol.flatMap { UIImage(systemName: $0) },
                         attributes: command.attributes) { [weak self] _ in
                    self?.picked = (row.key, command.id)
                }
            })
        }
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                previewForHighlightingMenuWithConfiguration configuration: UIContextMenuConfiguration) -> UITargetedPreview? {
        preview(in: interaction.view)
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                previewForDismissingMenuWithConfiguration configuration: UIContextMenuConfiguration) -> UITargetedPreview? {
        preview(in: interaction.view)
    }

    private func preview(in view: UIView?) -> UITargetedPreview? {
        guard let view, let rect = active?.rect.intersection(view.bounds), !rect.isEmpty,
              let snapshot = view.resizableSnapshotView(from: rect, afterScreenUpdates: false, withCapInsets: .zero) else { return nil }
        let parameters = UIPreviewParameters()
        parameters.backgroundColor = .clear
        parameters.visiblePath = UIBezierPath(roundedRect: snapshot.bounds, cornerRadius: 10)
        let target = UIPreviewTarget(container: view, center: CGPoint(x: rect.midX, y: rect.midY))
        return UITargetedPreview(view: snapshot, parameters: parameters, target: target)
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                willPerformPreviewActionForMenuWith configuration: UIContextMenuConfiguration,
                                animator: any UIContextMenuInteractionCommitAnimating) {
        if let row = active, row.preview {
            animator.preferredCommitStyle = .dismiss
            picked = (row.key, "open")
        }
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction, willEndFor configuration: UIContextMenuConfiguration,
                                animator: (any UIContextMenuInteractionAnimating)?) {
        let complete = { [weak self] in
            guard let self else { return }
            self.active = nil
            if let preview = self.conversationPreview {
                self.conversationPreview = nil
                self.emit(key: "", action: "closePreview", request: preview.request)
            }
            if let picked = self.picked {
                self.picked = nil
                self.emit(key: picked.key, action: picked.action)
            }
        }
        if let animator {
            animator.addCompletion(complete)
        } else {
            DispatchQueue.main.async(execute: complete)
        }
    }

    private func emit(key: String, action: String, request: String = "") {
        PluginHost.emit("shiojiUIKit", "rowContextAction", [
            "window": window, "key": key, "action": action, "request": request,
        ])
    }
}

private final class ConversationContextPreview: UIViewController {
    let request = UUID().uuidString
    private let maximumSize: CGSize
    private var contentHeight: CGFloat?
    private var needsFit = false

    init(size: CGSize) {
        maximumSize = size
        super.init(nibName: nil, bundle: nil)
        preferredContentSize = size
    }

    required init?(coder: NSCoder) { fatalError() }

    func fit(height: CGFloat) {
        contentHeight = min(maximumSize.height, ceil(height))
        fitToLayout()
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        fitToLayout()
    }

    private func fitToLayout() {
        guard contentHeight != nil, !needsFit else { return }
        needsFit = true
        DispatchQueue.main.async { [weak self] in
            guard let self, let contentHeight = self.contentHeight else { return }
            self.needsFit = false
            guard let actualHeight = self.viewIfLoaded?.bounds.height, actualHeight > 0 else { return }
            // UIKit can lay out the context card below its preferred size, particularly on iPad.
            let current = self.preferredContentSize.height
            let delta = contentHeight - actualHeight
            let fitted = min(self.maximumSize.height, max(contentHeight, ceil(current + delta)))
            if abs(delta) > 1 && abs(fitted - current) > 1 {
                self.preferredContentSize = CGSize(width: self.maximumSize.width, height: fitted)
            }
        }
    }

    override func loadView() {
        let container = UIView()
        container.backgroundColor = .systemBackground
        container.accessibilityIdentifier = "archiveConversationPreview"
        let loading = UILabel()
        loading.text = "正在读取会话…"
        loading.font = .preferredFont(forTextStyle: .subheadline)
        loading.textColor = .secondaryLabel
        loading.adjustsFontForContentSizeCategory = true
        loading.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(loading)
        NSLayoutConstraint.activate([
            loading.centerXAnchor.constraint(equalTo: container.centerXAnchor),
            loading.centerYAnchor.constraint(equalTo: container.centerYAnchor),
        ])
        view = container
    }

    func mount(_ child: UIViewController) {
        let container = view!
        for subview in container.subviews {
            subview.removeFromSuperview()
        }
        addChild(child)
        let content = child.view!
        content.frame = container.bounds
        content.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        content.isUserInteractionEnabled = false
        container.addSubview(content)
        child.didMove(toParent: self)
    }
}
