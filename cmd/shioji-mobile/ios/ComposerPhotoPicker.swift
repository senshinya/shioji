import PhotosUI
import UIKit
import UniformTypeIdentifiers

final class ComposerPhotoPicker: NSObject, PHPickerViewControllerDelegate, UIAdaptivePresentationControllerDelegate {
    private struct Photo {
        let file: URL
        let name: String
    }

    private var active: (picker: PHPickerViewController, reply: PluginReply)?

    func present(from page: UIViewController, limit: Int, reply: @escaping PluginReply) {
        guard limit > 0 else {
            reply(.failure(PluginFailure("PhotoLimit", "每条消息最多添加 8 个附件")))
            return
        }
        guard active == nil, page.viewIfLoaded?.window != nil, page.presentedViewController == nil else {
            reply(.success(nil))
            return
        }
        var configuration = PHPickerConfiguration()
        configuration.filter = .images
        configuration.selectionLimit = limit
        configuration.selection = .ordered
        configuration.preferredAssetRepresentationMode = .compatible
        let picker = PHPickerViewController(configuration: configuration)
        picker.delegate = self
        active = (picker, reply)
        page.present(picker, animated: true)
        picker.presentationController?.delegate = self
    }

    func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        guard let active, active.picker === picker else { return }
        self.active = nil
        picker.dismiss(animated: true) {
            guard !results.isEmpty else {
                active.reply(.success(nil))
                return
            }
            Task { @MainActor in
                var photos: [Photo] = []
                for (index, selection) in results.enumerated() {
                    let name = results.count == 1 ? "相册图片" : "相册图片 \(index + 1)"
                    switch await Self.load(selection.itemProvider, name: name) {
                    case .success(let photo):
                        photos.append(photo)
                    case .failure(let error):
                        for photo in photos { try? FileManager.default.removeItem(at: photo.file) }
                        active.reply(.failure(error))
                        return
                    }
                }
                active.reply(.success(["action": "photos", "photos": photos.map { ["path": $0.file.path, "name": $0.name] }]))
            }
        }
    }

    func presentationControllerDidDismiss(_ controller: UIPresentationController) {
        guard let active, controller.presentedViewController === active.picker else { return }
        self.active = nil
        active.reply(.success(nil))
    }

    private static func load(_ provider: NSItemProvider, name: String) async -> Result<Photo, PluginFailure> {
        let png = provider.hasItemConformingToTypeIdentifier(UTType.png.identifier)
        return await withCheckedContinuation { continuation in
            _ = provider.loadObject(ofClass: UIImage.self) { object, _ in
                guard let image = object as? UIImage, let pixels = image.cgImage,
                      Int64(pixels.width) * Int64(pixels.height) <= 40_000_000,
                      let data = png ? image.pngData() : image.jpegData(compressionQuality: 0.9), data.count <= 8 << 20 else {
                    continuation.resume(returning: .failure(PluginFailure("PhotoFailed", "无法读取\(name)，或图片超过 8 MiB 或 4000 万像素")))
                    return
                }
                let file = FileManager.default.temporaryDirectory.appendingPathComponent("shioji-photo-" + UUID().uuidString)
                do {
                    try data.write(to: file, options: [.atomic, .completeFileProtection])
                    let suffix = png ? ".png" : ".jpg"
                    continuation.resume(returning: .success(Photo(file: file, name: name + suffix)))
                } catch {
                    continuation.resume(returning: .failure(PluginFailure("PhotoFailed", "无法暂存\(name)")))
                }
            }
        }
    }
}
