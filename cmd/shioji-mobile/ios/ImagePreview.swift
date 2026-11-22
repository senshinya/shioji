import ImageIO
import UIKit

final class ImagePreviewView: UIView {
    private let photo: ImagePreviewScrollView
    private let rotate = UIButton(type: .system)

    init(path: String, name: String, reduceMotion: Bool, showsFilename: Bool = false, standalone: Bool = false) {
        photo = ImagePreviewScrollView(name: name, reduceMotion: reduceMotion)
        super.init(frame: .zero)

        let progress = UIActivityIndicatorView(style: .medium)
        progress.accessibilityLabel = "正在读取图片"
        progress.startAnimating()
        var config = UIButton.Configuration.plain()
        config.image = UIImage(systemName: "rotate.left")
        config.baseForegroundColor = .label
        rotate.configuration = config
        rotate.accessibilityLabel = "逆时针旋转 90°"
        rotate.isEnabled = false
        rotate.isHidden = standalone
        rotate.addAction(UIAction { [weak self] _ in self?.rotateImage() }, for: .touchUpInside)

        for child in [photo, rotate, progress] {
            child.translatesAutoresizingMaskIntoConstraints = false
            addSubview(child)
        }
        NSLayoutConstraint.activate([
            rotate.topAnchor.constraint(equalTo: topAnchor),
            rotate.trailingAnchor.constraint(equalTo: trailingAnchor),
            rotate.widthAnchor.constraint(equalToConstant: 44),
            rotate.heightAnchor.constraint(equalToConstant: 44),
            photo.topAnchor.constraint(equalTo: standalone ? topAnchor : rotate.bottomAnchor),
            photo.leadingAnchor.constraint(equalTo: leadingAnchor),
            photo.trailingAnchor.constraint(equalTo: trailingAnchor),
            photo.bottomAnchor.constraint(equalTo: bottomAnchor),
            progress.centerXAnchor.constraint(equalTo: photo.centerXAnchor),
            progress.centerYAnchor.constraint(equalTo: photo.centerYAnchor)
        ])
        if showsFilename {
            let filename = UILabel()
            filename.text = name
            filename.font = .preferredFont(forTextStyle: .subheadline)
            filename.adjustsFontForContentSizeCategory = true
            filename.lineBreakMode = .byTruncatingMiddle
            filename.translatesAutoresizingMaskIntoConstraints = false
            addSubview(filename)
            NSLayoutConstraint.activate([
                filename.leadingAnchor.constraint(equalTo: leadingAnchor),
                filename.trailingAnchor.constraint(lessThanOrEqualTo: rotate.leadingAnchor, constant: -8),
                filename.centerYAnchor.constraint(equalTo: rotate.centerYAnchor)
            ])
        }

        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let image = Self.readImage(at: URL(fileURLWithPath: path))
            DispatchQueue.main.async { [weak self] in
                progress.stopAnimating()
                guard let self else { return }
                if let image {
                    self.photo.show(image)
                    self.rotate.isEnabled = true
                } else {
                    self.showError()
                }
            }
        }
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func rotateImage() { photo.rotateImage() }

    private func showError() {
        let label = UILabel()
        label.text = "无法显示图片"
        label.font = .preferredFont(forTextStyle: .footnote)
        label.adjustsFontForContentSizeCategory = true
        label.textColor = .secondaryLabel
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: centerXAnchor),
            label.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    private static func readImage(at url: URL) -> UIImage? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary),
              let cgImage = CGImageSourceCreateThumbnailAtIndex(source, CGImageSourceGetPrimaryImageIndex(source), [
                  kCGImageSourceCreateThumbnailFromImageAlways: true,
                  kCGImageSourceCreateThumbnailWithTransform: true,
                  kCGImageSourceShouldCacheImmediately: true,
                  kCGImageSourceThumbnailMaxPixelSize: 4096
              ] as CFDictionary) else { return nil }
        return UIImage(cgImage: cgImage)
    }
}

private final class ImagePreviewScrollView: UIScrollView, UIScrollViewDelegate {
    private let photo = UIImageView()
    private let zoomedContent = UIView()
    private let reduceMotion: Bool
    private var turns = 0
    private var lastViewport = CGSize.zero
    private var fitting = false
    private var pinchStartScale: CGFloat = 1
    private var pinchAnchor = CGPoint.zero

    init(name: String, reduceMotion: Bool) {
        self.reduceMotion = reduceMotion
        super.init(frame: .zero)
        delegate = self
        minimumZoomScale = 1
        maximumZoomScale = 8
        bouncesZoom = true
        showsHorizontalScrollIndicator = false
        showsVerticalScrollIndicator = false
        contentInsetAdjustmentBehavior = .never
        accessibilityIdentifier = "imagePreviewScroll"

        photo.contentMode = .scaleAspectFit
        photo.accessibilityLabel = name
        photo.accessibilityTraits = .image
        photo.accessibilityCustomActions = [
            UIAccessibilityCustomAction(name: "放大图片") { [weak self] _ in
                guard let self, self.zoomScale < self.maximumZoomScale else { return false }
                self.setZoomScale(min(self.maximumZoomScale, self.zoomScale * 2), animated: self.animates)
                return true
            },
            UIAccessibilityCustomAction(name: "缩小图片") { [weak self] _ in
                guard let self, self.zoomScale > self.minimumZoomScale else { return false }
                self.setZoomScale(max(self.minimumZoomScale, self.zoomScale / 2), animated: self.animates)
                return true
            }
        ]
        zoomedContent.addSubview(photo)
        addSubview(zoomedContent)

        let doubleTap = UITapGestureRecognizer(target: self, action: #selector(toggleZoom(_:)))
        doubleTap.numberOfTapsRequired = 2
        addGestureRecognizer(doubleTap)

        // Drive the native scroll view around the touch point even when a
        // fitted portrait image leaves wide gutters in an iPad column.
        pinchGestureRecognizer?.isEnabled = false
        panGestureRecognizer.maximumNumberOfTouches = 1
        addGestureRecognizer(UIPinchGestureRecognizer(target: self, action: #selector(pinchImage(_:))))
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    private var animates: Bool { !reduceMotion && !UIAccessibility.isReduceMotionEnabled }
    private var angle: CGFloat { -CGFloat(turns) * .pi / 2 }

    func show(_ image: UIImage) {
        photo.image = image
        photo.isAccessibilityElement = true
        refit()
    }

    func rotateImage() {
        guard photo.image != nil else { return }
        let previousAngle = angle
        let previousWidth = photo.bounds.width
        turns = (turns + 1) % 4
        refit()
        guard animates, previousWidth > 0, photo.bounds.width > 0 else { return }
        let ratio = previousWidth / photo.bounds.width
        photo.transform = CGAffineTransform(rotationAngle: previousAngle).scaledBy(x: ratio, y: ratio)
        UIView.animate(withDuration: 0.22, delay: 0, options: [.curveEaseOut, .beginFromCurrentState, .allowUserInteraction]) {
            self.photo.transform = CGAffineTransform(rotationAngle: self.angle)
        }
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        if lastViewport != bounds.size {
            refit()
        }
    }

    private func refit() {
        guard let image = photo.image, bounds.width > 0, bounds.height > 0 else { return }
        lastViewport = bounds.size
        let sideways = turns % 2 != 0
        let rotated = sideways ? CGSize(width: image.size.height, height: image.size.width) : image.size
        let fit = min(bounds.width / rotated.width, bounds.height / rotated.height)
        let size = CGSize(width: rotated.width * fit, height: rotated.height * fit)
        let previousScale = zoomScale
        fitting = true
        setZoomScale(1, animated: false)
        zoomedContent.frame = CGRect(origin: .zero, size: size)
        photo.bounds = CGRect(origin: .zero, size: CGSize(width: image.size.width * fit, height: image.size.height * fit))
        photo.center = CGPoint(x: size.width / 2, y: size.height / 2)
        photo.transform = CGAffineTransform(rotationAngle: angle)
        contentSize = size
        setZoomScale(previousScale, animated: false)
        fitting = false
        centerImage()
        contentOffset = CGPoint(x: max(0, (contentSize.width - bounds.width) / 2),
                                y: max(0, (contentSize.height - bounds.height) / 2))
    }

    private func centerImage() {
        // Moving the image leaves the pinch's scroll insets stable.
        zoomedContent.center = CGPoint(x: max(bounds.width, contentSize.width) / 2,
                                       y: max(bounds.height, contentSize.height) / 2)
        let zoom = "\(Int((zoomScale * 100).rounded()))%"
        photo.accessibilityValue = turns == 0 ? zoom : "\(zoom)，逆时针 \(turns * 90)°"
    }

    func viewForZooming(in scrollView: UIScrollView) -> UIView? { zoomedContent }

    func scrollViewDidZoom(_ scrollView: UIScrollView) {
        if !fitting { centerImage() }
    }

    @objc private func pinchImage(_ pinch: UIPinchGestureRecognizer) {
        guard photo.image != nil else { return }
        if pinch.state == .began {
            pinchStartScale = zoomScale
            pinchAnchor = pinch.location(in: zoomedContent)
        }
        guard pinch.state == .began || pinch.state == .changed else { return }
        let touch = pinch.location(in: self)
        let viewportPoint = CGPoint(x: touch.x - bounds.minX, y: touch.y - bounds.minY)
        let scale = min(maximumZoomScale, max(minimumZoomScale, pinchStartScale * pinch.scale))
        setZoomScale(scale, animated: false)
        let anchor = zoomedContent.convert(pinchAnchor, to: self)
        contentOffset = CGPoint(x: max(0, min(contentSize.width - bounds.width, anchor.x - viewportPoint.x)),
                                y: max(0, min(contentSize.height - bounds.height, anchor.y - viewportPoint.y)))
    }

    @objc private func toggleZoom(_ tap: UITapGestureRecognizer) {
        guard photo.image != nil else { return }
        if zoomScale > minimumZoomScale {
            setZoomScale(minimumZoomScale, animated: animates)
        } else {
            let center = tap.location(in: zoomedContent)
            let width = bounds.width / 2
            let height = bounds.height / 2
            zoom(to: CGRect(x: center.x - width / 2, y: center.y - height / 2, width: width, height: height), animated: animates)
        }
    }
}

final class ImagePreviewController: UIViewController {
    let preview: ImagePreviewView

    init(path: String, name: String, reduceMotion: Bool) {
        preview = ImagePreviewView(path: path, name: name, reduceMotion: reduceMotion, standalone: true)
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override func loadView() { view = preview }
}
