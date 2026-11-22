import UIKit

struct PopupMenuItem: Decodable {
    struct MenuChildren: Decodable { let Items: [PopupMenuItem] }
    struct MenuImage: Decodable {
        let PNG: Data?
        let Template: Bool
    }

    let ID: Int
    let `Type`: Int
    let Label: String
    let Subtitle: String
    let Enabled: Bool
    let Visible: Bool
    let Checked: Bool
    let Destructive: Bool
    let Submenu: MenuChildren?
    let Icon: MenuImage?

    static func elements(_ items: [Self], choose: @escaping (Int) -> Void) -> [UIMenuElement] {
        var groups: [[UIMenuElement]] = [[]]
        for item in items where item.Visible {
            if item.Type == 1 {
                if !groups[groups.count - 1].isEmpty { groups.append([]) }
                continue
            }
            if let submenu = item.Submenu {
                groups[groups.count - 1].append(UIMenu(title: item.Label, children: elements(submenu.Items, choose: choose)))
                continue
            }
            var attributes: UIMenuElement.Attributes = []
            if !item.Enabled { attributes.insert(.disabled) }
            if item.Destructive { attributes.insert(.destructive) }
            let icon = item.Icon?.PNG.flatMap { UIImage(data: $0, scale: 3) }?
                .withRenderingMode(item.Icon?.Template == true ? .alwaysTemplate : .alwaysOriginal)
            let action = UIAction(title: item.Label, subtitle: item.Subtitle.isEmpty ? nil : item.Subtitle,
                                  image: icon, attributes: attributes, state: item.Checked ? .on : .off) { _ in
                choose(item.ID)
            }
            groups[groups.count - 1].append(action)
        }
        let populated = groups.filter { !$0.isEmpty }
        if populated.count == 1 { return populated[0] }
        return populated.map { UIMenu(options: .displayInline, children: $0) }
    }
}

final class PopupMenuAnchor: UIButton {
    var finish: ((Int) -> Void)?
    private(set) var didPresent = false

    func select(_ id: Int) {
        let callback = finish
        finish = nil
        callback?(id)
    }

    override func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                         willDisplayMenuFor configuration: UIContextMenuConfiguration,
                                         animator: UIContextMenuInteractionAnimating?) {
        super.contextMenuInteraction(interaction, willDisplayMenuFor: configuration, animator: animator)
        didPresent = true
    }

    override func contextMenuInteraction(_ interaction: UIContextMenuInteraction,
                                         willEndFor configuration: UIContextMenuConfiguration,
                                         animator: UIContextMenuInteractionAnimating?) {
        super.contextMenuInteraction(interaction, willEndFor: configuration, animator: animator)
        let complete = { [weak self] in
            guard let self else { return }
            self.select(0)
            self.removeFromSuperview()
        }
        if let animator { animator.addCompletion(complete) } else { DispatchQueue.main.async(execute: complete) }
    }
}
