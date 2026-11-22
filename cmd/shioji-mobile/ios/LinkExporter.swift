import UIKit

final class LinkExporter: NSObject, UIDocumentPickerDelegate, UIAdaptivePresentationControllerDelegate {
    private var pending: (id: String, progress: UIAlertController?, page: UIViewController)?
    private var export: (picker: UIDocumentPickerViewController, reply: PluginReply)?

    func begin(id: String, from page: UIViewController, reply: @escaping PluginReply) {
        guard pending == nil, export == nil, availableNavigation(for: page) != nil else {
            reply(.failure(PluginFailure("ExportUnavailable", "无法读取此链接，请重试")))
            return
        }
        pending = (id, nil, page)
        reply(.success(nil))
        // Quick transfers go straight to Files without a flash of progress UI.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { [weak self] in
            self?.showProgress(id: id)
        }
    }

    private func showProgress(id: String) {
        guard let current = pending, current.id == id, let nav = availableNavigation(for: current.page) else { return }
        let progress = UIAlertController(title: "正在读取链接…", message: nil, preferredStyle: .alert)
        progress.addAction(UIAlertAction(title: "取消", style: .cancel) { [weak self] _ in
            guard self?.pending?.id == id else { return }
            self?.pending = nil
            PluginHost.emit("shiojiUIKit", "linkExportCanceled", id)
        })
        pending = (id, progress, current.page)
        nav.present(progress, animated: true)
    }

    func finish(id: String, path: String?, problem: String?, reply: @escaping PluginReply) {
        guard let current = pending, current.id == id else {
            reply(.success(nil))
            return
        }
        pending = nil
        let complete = {
            guard let nav = self.availableNavigation(for: current.page) else {
                reply(.success(nil))
                return
            }
            if let path {
                let file = URL(fileURLWithPath: path)
                let picker = UIDocumentPickerViewController(forExporting: [file], asCopy: true)
                picker.delegate = self
                self.export = (picker, reply)
                nav.present(picker, animated: true)
                picker.presentationController?.delegate = self
            } else {
                if let problem {
                    let alert = UIAlertController(title: "无法保存链接", message: problem, preferredStyle: .alert)
                    alert.addAction(UIAlertAction(title: "好", style: .default))
                    nav.present(alert, animated: true)
                }
                reply(.success(nil))
            }
        }
        if let progress = current.progress {
            if progress.isBeingPresented, let transition = progress.transitionCoordinator,
               transition.animate(alongsideTransition: nil, completion: { _ in
                   progress.dismiss(animated: true, completion: complete)
               }) {
                return
            }
            progress.dismiss(animated: true, completion: complete)
        } else {
            complete()
        }
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        dismissPicker(controller)
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        dismissPicker(controller)
    }

    func presentationControllerDidDismiss(_ controller: UIPresentationController) {
        if let picker = controller.presentedViewController as? UIDocumentPickerViewController {
            dismissPicker(picker)
        }
    }

    private func dismissPicker(_ picker: UIDocumentPickerViewController) {
        guard let current = export, current.picker === picker else { return }
        export = nil
        current.reply(.success(nil))
    }

    private func availableNavigation(for page: UIViewController) -> UINavigationController? {
        guard let nav = page.navigationController, nav.topViewController === page,
              page.viewIfLoaded?.window != nil, nav.presentedViewController == nil else { return nil }
        return nav
    }
}
