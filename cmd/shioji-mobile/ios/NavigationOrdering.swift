import UIKit

final class NavigationOrdering: UITableViewController, UIAdaptivePresentationControllerDelegate {
    private struct Row {
        let index: Int
        let title: String
        let detail: String?
    }

    private var rows: [Row]
    private var reply: PluginReply?

    init(title: String, rows: [[String: Any]], reply: @escaping PluginReply) {
        self.rows = rows.enumerated().map { index, row in
            Row(index: index, title: row["title"] as? String ?? "", detail: row["detail"] as? String)
        }
        self.reply = reply
        super.init(style: .plain)
        self.title = title
        navigationItem.largeTitleDisplayMode = .never
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        tableView.accessibilityIdentifier = "navigation-ordering"
        tableView.register(UITableViewCell.self, forCellReuseIdentifier: "order")
        tableView.rowHeight = UITableView.automaticDimension
        tableView.estimatedRowHeight = 54
        tableView.allowsSelectionDuringEditing = true
        tableView.selectionFollowsFocus = true
        tableView.remembersLastFocusedIndexPath = true
        tableView.isEditing = true
        navigationItem.leftBarButtonItem = UIBarButtonItem(title: "取消", style: .plain, target: self, action: #selector(cancel))
        navigationItem.rightBarButtonItem = UIBarButtonItem(title: "完成", style: .done, target: self, action: #selector(save))
        navigationItem.rightBarButtonItem?.isEnabled = false
    }

    override var canBecomeFirstResponder: Bool { true }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        becomeFirstResponder()
    }

    override var keyCommands: [UIKeyCommand]? {
        let reorder = [(UIKeyCommand.inputUpArrow, "上移"), (UIKeyCommand.inputDownArrow, "下移")]
        return (super.keyCommands ?? []) + reorder.map { input, title in
            let command = UIKeyCommand(input: input, modifierFlags: .alternate, action: #selector(moveSelection(_:)))
            command.discoverabilityTitle = title
            command.wantsPriorityOverSystemBehavior = true
            return command
        }
    }

    override func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int { rows.count }

    override func tableView(_ tableView: UITableView, cellForRowAt path: IndexPath) -> UITableViewCell {
        let cell = tableView.dequeueReusableCell(withIdentifier: "order", for: path)
        let row = rows[path.row]
        var content = UIListContentConfiguration.subtitleCell()
        content.text = row.title
        content.secondaryText = row.detail
        content.secondaryTextProperties.color = .secondaryLabel
        cell.contentConfiguration = content
        cell.showsReorderControl = true
        return cell
    }

    override func tableView(_ tableView: UITableView, canMoveRowAt path: IndexPath) -> Bool { true }

    override func tableView(_ tableView: UITableView, editingStyleForRowAt path: IndexPath) -> UITableViewCell.EditingStyle { .none }

    override func tableView(_ tableView: UITableView, shouldIndentWhileEditingRowAt path: IndexPath) -> Bool { false }

    override func tableView(_ tableView: UITableView, moveRowAt source: IndexPath, to destination: IndexPath) {
        moveRow(from: source.row, to: destination.row)
    }

    private func moveRow(from source: Int, to destination: Int) {
        let row = rows.remove(at: source)
        rows.insert(row, at: destination)
        navigationItem.rightBarButtonItem?.isEnabled = rows.enumerated().contains { $0.offset != $0.element.index }
    }

    @objc private func moveSelection(_ command: UIKeyCommand) {
        guard let source = tableView.indexPathForSelectedRow else { return }
        let direction = command.input == UIKeyCommand.inputUpArrow ? -1 : 1
        let index = source.row + direction
        guard rows.indices.contains(index) else { return }
        let destination = IndexPath(row: index, section: source.section)
        moveRow(from: source.row, to: index)
        tableView.moveRow(at: source, to: destination)
        tableView.selectRow(at: destination, animated: false, scrollPosition: .none)
    }

    @objc private func cancel() { dismiss(result: nil) }

    @objc private func save() { dismiss(result: rows.map(\.index)) }

    private func dismiss(result: [Int]?) {
        guard let reply else { return }
        self.reply = nil
        dismiss(animated: true) { reply(.success(result)) }
    }

    func presentationControllerDidDismiss(_ presentationController: UIPresentationController) {
        reply?(.success(nil))
        reply = nil
    }
}
