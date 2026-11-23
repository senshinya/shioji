import UIKit
import QuartzCore
import UserNotifications
import QuickLook
import SafariServices
import UniformTypeIdentifiers

final class ShiojiUIKit: NSObject, MyGoPlugin, UIAdaptivePresentationControllerDelegate, UITabBarControllerDelegate, SFSafariViewControllerDelegate {
    private let composeTabID = "shioji.compose"
    private var forms: [Int: UINavigationController] = [:]
    private var sessionSearch: SessionSearchBar?
    private let keyboardTiming = KeyboardTiming()
    private var tabSlide: TabSlideCover?
    private var tabRoots: [UIViewController] = []
    private let rowMenus = NSMapTable<UIView, RowContextMenus>.weakToStrongObjects()
    private var forwardedPage: (browser: SFSafariViewController, close: PluginReply)?
    private let linkExporter = LinkExporter()
    private let composerPhotoPicker = ComposerPhotoPicker()
    private var split: SessionSplitController?
    private weak var tabs: UITabBarController?
    private var canvasColor = UIColor.systemBackground
    private var sidebarColor = UIColor.secondarySystemBackground
    private var secondaryInk = UIColor.secondaryLabel
    private var titleSize: CGFloat?
    private var scopeSize: CGFloat?
    private var titleGap: CGFloat = 4

    func call(_ method: String, _ args: [String: Any], _ reply: @escaping PluginReply) {
        switch method {
        case "configureHome":
            guard let id = args["home"] as? Int, let home = controller(id) else {
                reply(.failure(PluginFailure("MissingHome", "找不到会话主页")))
                return
            }
            if let appearance = args["appearance"] as? [String: Any] {
                canvasColor = Self.adaptiveColor(appearance["canvas"], fallback: .systemBackground)
                sidebarColor = Self.adaptiveColor(appearance["sidebar"], fallback: .secondarySystemBackground)
                secondaryInk = Self.adaptiveColor(appearance["secondary"], fallback: .secondaryLabel)
                titleSize = (appearance["titleSize"] as? NSNumber).map { CGFloat($0.doubleValue) }
                scopeSize = (appearance["scopeSize"] as? NSNumber).map { CGFloat($0.doubleValue) }
                if let gap = appearance["titleGap"] as? NSNumber {
                    titleGap = CGFloat(gap.doubleValue)
                }
            }
            home.navigationItem.title = ""
            configureHomeNavigation(home)
            sidebarBackground(for: home, wide: false)
            tabs = home.tabBarController
            Host.shared.root.view.backgroundColor = canvasColor
            keyboardTiming.attach(to: Host.shared.root.view)
            Host.shared.root.keyboardAnimationChanged = { [weak self] from, to, bottom, duration, curve in
                self?.keyboardTiming.animate(from: from, to: to, bottom: bottom, duration: duration, curve: curve)
            }
            tabs?.view.backgroundColor = canvasColor
            tabs?.tabBar.tintColor = .label
            tabs?.delegate = self
            home.navigationItem.largeTitleDisplayMode = .never
            home.navigationItem.backButtonDisplayMode = .minimal
            for key in ["devices", "settings"] {
                if let id = args[key] as? Int, let root = controller(id) {
                    root.navigationItem.largeTitleDisplayMode = .never
                    root.navigationItem.backButtonDisplayMode = .minimal
                    configureInlineBar(root)
                }
            }
            if let detailID = args["detail"] as? Int, let detail = controller(detailID) {
                installSplit(home: home, detail: detail, minimumWidth: args["minimumWidth"] as? Double ?? 840)
            } else if #available(iOS 27.0, *), let tabs {
                let image = (args["composeIcon"] as? String)
                    .flatMap { Data(base64Encoded: $0) }
                    .flatMap { UIImage(data: $0, scale: 3)?.withRenderingMode(.alwaysTemplate) }
                installComposeTab(in: tabs, image: image)
            }
            if let bar = tabs?.tabBar { configureTabBarLabels(bar) }
            installNavigationShortcuts(args["shortcuts"] as? [[String: Any]] ?? [])
            reply(.success(nil))
        case "search":
            if let id = args["home"] as? Int {
                let values = args["anchor"] as? [Double]
                let anchor = values.flatMap { v -> CGRect? in
                    guard v.count == 4, v[2] > 0, v[3] > 0 else { return nil }
                    return CGRect(x: v[0], y: v[1], width: v[2], height: v[3])
                }
                openSearch(home: id, toggle: args["toggle"] as? Bool == true, anchor: anchor,
                           reduceMotion: args["reduceMotion"] as? Bool == true)
            }
            reply(.success(nil))
        case "closeSearch":
            sessionSearch?.close()
            reply(.success(nil))
        case "configurePage":
            if let id = args["window"] as? Int, let page = controller(id) {
                page.hidesBottomBarWhenPushed = true
                page.navigationItem.backButtonDisplayMode = .minimal
                configureInlineBar(page)
                if let title = args["title"] as? String {
                    setConversationTitle(title, subtitle: args["subtitle"] as? String ?? "", inlineScope: args["inlineScope"] as? Bool == true,
                                     reduceMotion: args["reduceMotion"] as? Bool == true, on: page)
                }
                if #available(iOS 26.0, *), args["commit"] as? Bool == true,
                   let button = page.navigationItem.rightBarButtonItem {
                    button.style = .prominent
                    button.tintColor = .label
                }
            }
            reply(.success(nil))
        case "composerAttachments":
            guard let id = args["window"] as? Int, let page = controller(id), let container = page.viewIfLoaded,
                  container.window != nil, page.presentedViewController == nil,
                  let anchor = args["rect"] as? [Double], anchor.count == 4 else {
                reply(.success(nil))
                return
            }
            let photoLimit = args["photoLimit"] as? Int ?? 0
            let menu = ComposerAttachmentMenu(appearance: container.traitCollection, pickPhoto: { [weak page, composerPhotoPicker] completion in
                guard let page else {
                    completion(.success(nil))
                    return
                }
                composerPhotoPicker.present(from: page, limit: photoLimit, reply: completion)
            }, reply: reply)
            if let presentation = menu.popoverPresentationController {
                presentation.delegate = menu
                presentation.backgroundColor = menu.surfaceColor
                presentation.sourceView = container
                let rect = CGRect(x: anchor[0], y: anchor[1], width: anchor[2], height: anchor[3])
                presentation.sourceRect = rect.intersection(container.bounds).isEmpty ? container.bounds : rect
                presentation.permittedArrowDirections = [.down, .up]
            }
            page.present(menu, animated: true)
        case "reorderNavigation":
            guard let id = args["home"] as? Int, let page = controller(id),
                  page.viewIfLoaded?.window != nil, page.navigationController?.topViewController === page,
                  page.presentedViewController == nil, page.navigationController?.presentedViewController == nil,
                  let title = args["title"] as? String, let rows = args["rows"] as? [[String: Any]],
                  rows.count > 1 else {
                reply(.success(nil))
                return
            }
            let ordering = NavigationOrdering(title: title, rows: rows, reply: reply)
            let form = UINavigationController(rootViewController: ordering)
            form.modalPresentationStyle = .formSheet
            form.preferredContentSize = CGSize(width: 500, height: min(560, max(240, 100 + CGFloat(rows.count) * 54)))
            form.view.tintColor = .label
            form.presentationController?.delegate = ordering
            page.present(form, animated: true)
        case "rowContextMenus":
            if let id = args["window"] as? Int, let page = controller(id),
               let sequence = args["sequence"] as? Int, let view = page.view {
                let menus = rowMenus.object(forKey: view) ?? RowContextMenus(view: view, window: id)
                rowMenus.setObject(menus, forKey: view)
                menus.update(sequence: sequence, rows: args["rows"] as? [[String: Any]] ?? [])
            }
            reply(.success(nil))
        case "mountContextPreview":
            if let id = args["window"] as? Int, let view = controller(id)?.viewIfLoaded,
               let previewID = args["preview"] as? Int, let preview = controller(previewID),
               let request = args["request"] as? String {
                rowMenus.object(forKey: view)?.mountPreview(preview, request: request)
            }
            reply(.success(nil))
        case "fitContextPreview":
            if let id = args["window"] as? Int, let view = controller(id)?.viewIfLoaded,
               let request = args["request"] as? String, let height = args["height"] as? Double {
                rowMenus.object(forKey: view)?.fitPreview(height: height, request: request)
            }
            reply(.success(nil))
        case "confirmArchiveDeletion", "confirmStorageCleanup", "confirmWorkspaceArchive", "confirmDevicePathRemoval":
            guard let id = args["window"] as? Int, let page = controller(id),
                  page.viewIfLoaded?.window != nil, page.navigationController?.topViewController === page,
                  let title = args["title"] as? String, let detail = args["detail"] as? String else {
                if method == "confirmStorageCleanup" {
                    reply(.failure(PluginFailure("StoragePageInactive", "当前设备诊断页未处于前台")))
                } else {
                    reply(.success(false))
                }
                return
            }
            let alert = UIAlertController(title: title, message: detail, preferredStyle: .alert)
            let cancel = UIAlertAction(title: "取消", style: .cancel) { _ in reply(.success(false)) }
            alert.addAction(cancel)
            let action: String
            switch method {
            case "confirmStorageCleanup": action = "清理缓存"
            case "confirmWorkspaceArchive": action = "归档工作区"
            case "confirmDevicePathRemoval": action = "移除连接"
            default: action = "彻底删除"
            }
            alert.addAction(UIAlertAction(title: action, style: method == "confirmWorkspaceArchive" ? .default : .destructive) { _ in
                reply(.success(true))
            })
            alert.preferredAction = cancel
            alert.view.tintColor = .label
            PluginHost.present(alert)
        case "conversationTitle":
            if let id = args["window"] as? Int, let page = controller(id), let title = args["title"] as? String {
                setConversationTitle(title, subtitle: args["subtitle"] as? String ?? "", inlineScope: args["inlineScope"] as? Bool == true,
                                     reduceMotion: args["reduceMotion"] as? Bool == true, on: page)
            }
            reply(.success(nil))
        case "toggleSidebar":
            if let split, !split.isCollapsed {
                if split.displayMode == .secondaryOnly {
                    split.show(.primary)
                } else {
                    split.hide(.primary)
                }
            }
            reply(.success(nil))
        case "selectTab":
            if let index = args["index"] as? Int {
                selectTab(index)
            }
            reply(.success(nil))
        case "attentionSource":
            guard let id = args["window"] as? Int, shortcutSource(allowDraft: true)?.window.id == id else {
                reply(.success(false))
                return
            }
            if args["navigate"] as? Bool == true {
                selectTab(0)
            }
            reply(.success(true))
        case "resetDeviceNavigation":
            for id in args["roots"] as? [Int] ?? [] {
                if let root = controller(id) {
                    root.navigationController?.popToViewController(root, animated: false)
                }
            }
            reply(.success(nil))
        case "showPage":
            if let id = args["window"] as? Int, let page = controller(id) {
                page.navigationController?.popToViewController(page, animated: true)
            }
            reply(.success(nil))
        case "tabsHidden":
            if let hidden = args["hidden"] as? Bool, let tabs {
                if #available(iOS 18.0, *) {
                    tabs.setTabBarHidden(hidden || shouldHideSplitTabs, animated: true)
                } else {
                    tabs.tabBar.isHidden = hidden || shouldHideSplitTabs
                }
            }
            reply(.success(nil))
        case "popPage":
            if let id = args["window"] as? Int, let page = controller(id),
               let nav = page.navigationController,
               let index = nav.viewControllers.firstIndex(of: page), index > 0 {
                nav.popToViewController(nav.viewControllers[index - 1], animated: false)
            }
            reply(.success(nil))
        case "presentForm":
            guard let id = args["window"] as? Int, let page = controller(id) else {
                reply(.failure(PluginFailure("MissingForm", "找不到新建会话页面")))
                return
            }
            let title = args["title"] as? String
            page.title = title ?? "新建会话"
            page.navigationItem.largeTitleDisplayMode = .never
            (page as? WindowController)?.barAtRoot = true
            let form = UINavigationController(rootViewController: page)
            form.modalPresentationStyle = .formSheet
            form.preferredContentSize = CGSize(width: title == nil ? 590 : 460, height: 540)
            if title != nil, page.traitCollection.userInterfaceIdiom == .phone,
               let sheet = form.sheetPresentationController {
                sheet.detents = [.medium(), .large()]
                sheet.prefersGrabberVisible = true
            }
            page.navigationItem.leftBarButtonItem = UIBarButtonItem(title: title == nil ? "取消" : "关闭", primaryAction: UIAction { [weak self] _ in
                self?.dismissForm(id)
            })
            forms[id] = form
            PluginHost.present(form)
            form.presentationController?.delegate = self
            reply(.success(nil))
        case "notificationSettings":
            UNUserNotificationCenter.current().getNotificationSettings { settings in
                let authorization: String
                switch settings.authorizationStatus {
                case .authorized, .provisional, .ephemeral: authorization = "allowed"
                case .denied: authorization = "denied"
                default: authorization = "undetermined"
                }
                DispatchQueue.main.async { reply(.success(["authorization": authorization])) }
            }
        case "authorizeNotifications":
            UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { allowed, error in
                DispatchQueue.main.async {
                    if let error {
                        reply(.failure(PluginFailure("NotificationAuthorizationFailed", error.localizedDescription)))
                    } else if allowed {
                        reply(.success(nil))
                    } else {
                        reply(.failure(PluginFailure("NotificationsDenied", "请在系统设置中允许 Shioji 通知")))
                    }
                }
            }
        case "quickLook":
            guard let id = args["window"] as? Int, let page = controller(id),
                  page.viewIfLoaded?.window != nil, page.presentedViewController == nil,
                  page.navigationController == nil || page.navigationController?.topViewController === page,
                  let path = args["path"] as? String, let name = args["name"] as? String,
                  let mime = args["mime"] as? String else {
                reply(.success(nil))
                return
            }
            do {
                let preview = try AttachmentPreview(stagedFile: URL(fileURLWithPath: path), name: name, mime: mime)
                guard preview.canShowItem else {
                    reply(.success(false))
                    return
                }
                let navigation = UINavigationController(rootViewController: preview)
                preview.navigationItem.leftBarButtonItem = UIBarButtonItem(systemItem: .close, primaryAction: UIAction { [weak navigation] _ in
                    navigation?.dismiss(animated: true)
                })
                preview.navigationItem.leftBarButtonItem?.accessibilityIdentifier = "shioji-close-quick-look"
                navigation.modalPresentationStyle = .fullScreen
                page.present(navigation, animated: true)
            } catch {
                reply(.failure(PluginFailure("AttachmentPreviewUnavailable", "无法预览此附件")))
                return
            }
            reply(.success(true))
        case "beginLinkExport":
            guard let window = args["window"] as? Int, let page = controller(window), let id = args["id"] as? String else {
                reply(.failure(PluginFailure("MissingExportSource", "找不到链接所属页面")))
                return
            }
            linkExporter.begin(id: id, from: page, reply: reply)
        case "finishLinkExport":
            guard let id = args["id"] as? String else {
                reply(.failure(PluginFailure("MissingExportID", "无法完成文件保存")))
                return
            }
            linkExporter.finish(id: id, path: args["path"] as? String, problem: args["problem"] as? String, reply: reply)
        case "openForwardedPage":
            guard forwardedPage == nil, let id = args["window"] as? Int, let page = controller(id),
                  let nav = page.navigationController, nav.topViewController === page,
                  page.viewIfLoaded?.window != nil, nav.presentedViewController == nil,
                  let address = args["url"] as? String, let destination = URL(string: address),
                  destination.scheme == "http" || destination.scheme == "https",
                  destination.host == "127.0.0.1", destination.port != nil, destination.user == nil else {
                reply(.failure(PluginFailure("ForwardedPageUnavailable", "无法打开此网页，请重试")))
                return
            }
            let browser = SFSafariViewController(url: destination)
            browser.dismissButtonStyle = .done
            browser.delegate = self
            forwardedPage = (browser, reply)
            nav.present(browser, animated: true)
        case "openAgentAuthorization":
            guard let id = args["window"] as? Int, let page = controller(id),
                  let nav = page.navigationController, nav.topViewController === page,
                  page.viewIfLoaded?.window != nil, nav.presentedViewController == nil,
                  let address = args["url"] as? String, let destination = URL(string: address),
                  destination.scheme == "https", let host = destination.host, !host.isEmpty, destination.user == nil else {
                reply(.failure(PluginFailure("AuthorizationUnavailable", "无法打开授权页，请重试")))
                return
            }
            let browser = SFSafariViewController(url: destination)
            browser.dismissButtonStyle = .done
            nav.present(browser, animated: true)
            reply(.success(nil))
        case "openNotificationSettings":
            guard let url = URL(string: UIApplication.openNotificationSettingsURLString) else {
                reply(.failure(PluginFailure("NotificationSettingsUnavailable", "无法打开系统通知设置")))
                return
            }
            UIApplication.shared.open(url) { opened in
                if opened {
                    reply(.success(nil))
                } else {
                    reply(.failure(PluginFailure("NotificationSettingsUnavailable", "无法打开系统通知设置")))
                }
            }
        case "dismissForm":
            if let id = args["window"] as? Int { dismissForm(id) }
            reply(.success(nil))
        default:
            reply(.failure(PluginFailure("UnknownMethod", method)))
        }
    }

    func safariViewControllerDidFinish(_ controller: SFSafariViewController) {
        if let page = forwardedPage, page.browser === controller {
            forwardedPage = nil
            page.close(.success(nil))
        }
    }

    private static func adaptiveColor(_ value: Any?, fallback: UIColor) -> UIColor {
        guard let rgb = value as? [Int], rgb.count == 2 else { return fallback }
        let tones = rgb.map { value in
            UIColor(red: CGFloat((value >> 16) & 255) / 255,
                    green: CGFloat((value >> 8) & 255) / 255,
                    blue: CGFloat(value & 255) / 255, alpha: 1)
        }
        return UIColor { $0.userInterfaceStyle == .dark ? tones[1] : tones[0] }
    }

    private func scaledFont(_ size: CGFloat?, style: UIFont.TextStyle, weight: UIFont.Weight = .regular) -> UIFont {
        guard let size else { return UIFont.preferredFont(forTextStyle: style) }
        return UIFontMetrics(forTextStyle: style).scaledFont(for: .systemFont(ofSize: size, weight: weight))
    }

    private func setConversationTitle(_ title: String, subtitle: String, inlineScope: Bool, reduceMotion: Bool = false, on page: UIViewController) {
        if let view = page.navigationItem.titleView as? ConversationTitleStack,
           let label = view.arrangedSubviews.first as? UILabel,
           (view.arrangedSubviews.dropFirst().first as? ConversationScopeStack)?.arrangedSubviews.last.flatMap({ $0 as? UILabel })?.text ?? "" == subtitle {
            if label.text != title {
                if reduceMotion || UIAccessibility.isReduceMotionEnabled {
                    label.text = title
                } else {
                    UIView.transition(with: label, duration: 0.16, options: [.transitionCrossDissolve, .beginFromCurrentState, .allowUserInteraction]) {
                        label.text = title
                    }
                }
            }
            return
        }
        let label = UILabel()
        label.text = title
        label.accessibilityIdentifier = "conversation-title"
        label.font = scaledFont(titleSize, style: .headline, weight: .semibold)
        label.adjustsFontForContentSizeCategory = true
        label.lineBreakMode = .byTruncatingTail
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        label.accessibilityTraits.insert(.header)
        let caption = UILabel()
        caption.text = subtitle
        caption.font = scaledFont(scopeSize, style: .caption2)
        caption.textColor = secondaryInk
        caption.adjustsFontForContentSizeCategory = true
        caption.lineBreakMode = .byTruncatingTail
        caption.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let display = UIImageView(image: UIImage(systemName: "display", withConfiguration: UIImage.SymbolConfiguration(pointSize: 11)))
        display.tintColor = caption.textColor
        display.isAccessibilityElement = false
        let scope = ConversationScopeStack(arrangedSubviews: [display, caption])
        scope.spacing = 4
        scope.alignment = .center
        let view = ConversationTitleStack(arrangedSubviews: subtitle.isEmpty ? [label] : [label, scope])
        view.directionalLayoutMargins = NSDirectionalEdgeInsets(top: 0, leading: 4, bottom: 0, trailing: 4)
        view.isLayoutMarginsRelativeArrangement = true
        if inlineScope {
            view.axis = .horizontal
            view.spacing = 8
            view.alignment = .firstBaseline
            label.setContentHuggingPriority(UILayoutPriority(249), for: .horizontal)
            label.setContentCompressionResistancePriority(UILayoutPriority(249), for: .horizontal)
            caption.lineBreakMode = .byTruncatingMiddle
            if !subtitle.isEmpty {
                scope.widthAnchor.constraint(lessThanOrEqualTo: view.layoutMarginsGuide.widthAnchor, multiplier: 0.56).isActive = true
            }
        } else {
            view.axis = .vertical
            view.spacing = titleGap
            view.alignment = .leading
        }
        page.navigationItem.titleView = view
    }

    private func controller(_ id: Int) -> UIViewController? {
        MyGoWindows.controller(NSNumber(value: id))
    }

    private func configureInlineBar(_ page: UIViewController) {
        guard let screen = page as? WindowController else { return }
        screen.compactInlineBar = true
        screen.view.backgroundColor = canvasColor
        screen.window.backgroundColor = canvasColor
        screen.navigationController?.view.backgroundColor = canvasColor
        observeTextDismissal(screen)
        let appearance = UINavigationBarAppearance()
        appearance.configureWithTransparentBackground()
        screen.navigationItem.standardAppearance = appearance
        screen.navigationItem.scrollEdgeAppearance = appearance
    }

    private func observeTextDismissal(_ screen: WindowController) {
        guard let surface = screen.window as? SurfaceWindow else { return }
        let id = surface.id
        surface.onTextInputDismissed = {
            PluginHost.emit("shiojiUIKit", "composerKeyboardDismissed", id)
        }
    }

    private func openSearch(home id: Int, toggle: Bool, anchor: CGRect?, reduceMotion: Bool) {
        guard let page = controller(id) as? WindowController else { return }
        if let search = sessionSearch {
            if toggle { search.close() }
            return
        }
        if toggle {
            selectTab(0)
            if let split, !split.isCollapsed, split.displayMode == .secondaryOnly { split.show(.primary) }
            if let nav = page.navigationController, nav.topViewController !== page {
                nav.popToViewController(page, animated: true)
                if let transition = nav.transitionCoordinator,
                   transition.animate(alongsideTransition: nil, completion: { [weak self] context in
                       if !context.isCancelled { self?.openSearch(home: id, toggle: false, anchor: anchor, reduceMotion: reduceMotion) }
                   }) {
                    return
                }
            }
        }
        page.loadViewIfNeeded()
        let fallback = CGRect(x: page.view.bounds.width - 112,
                              y: page.view.safeAreaInsets.top, width: 44, height: 44)
        let frame = anchor.map { page.window.convert($0, to: page.view) } ?? fallback
        let search = SessionSearchBar(width: page.view.bounds.width, anchor: frame, reduceMotion: reduceMotion)
        sessionSearch = search
        search.onQuery = { PluginHost.emit("shiojiUIKit", "searchQuery", $0) }
        search.onSubmit = { PluginHost.emit("shiojiUIKit", "searchSubmit", true) }
        search.field.onMove = { PluginHost.emit("shiojiUIKit", "searchMove", $0) }
        search.onClosing = { PluginHost.emit("shiojiUIKit", "searchActive", false) }
        search.onClose = { [weak self, weak search] in
            if self?.sessionSearch === search { self?.sessionSearch = nil }
        }
        page.view.addSubview(search)
        PluginHost.emit("shiojiUIKit", "searchActive", true)
        search.open()
    }

    private func selectTab(_ index: Int) {
        guard let tabs else { return }
        let roots = tabRoots.isEmpty ? tabs.viewControllers ?? [] : tabRoots
        if roots.indices.contains(index) { prepareTabSlide(tabs, to: roots[index]) }
        if #available(iOS 27.0, *), tabs.prominentTabIdentifier == composeTabID,
           tabs.tabs.indices.contains(index), tabs.tabs[index].identifier != composeTabID {
            tabs.selectedTab = tabs.tabs[index]
        } else if tabs.viewControllers?.indices.contains(index) == true {
            tabs.selectedIndex = index
        }
        syncSplitTabs()
        playTabSlide(in: tabs)
    }

    private func installNavigationShortcuts(_ shortcuts: [[String: Any]]) {
        let root = Host.shared.root
        root.appKeyCommands = shortcuts.flatMap { shortcut -> [UIKeyCommand] in
            guard let action = shortcut["action"] as? String, let title = shortcut["title"] as? String,
                  let key = shortcut["key"] as? String else { return [] }
            let flags: UIKeyModifierFlags = shortcut["shift"] as? Bool == true ? [.command, .shift] : .command
            // Layouts can report the plus key as either a shifted equal or a literal plus.
            let shifted = flags.union(.shift)
            let inputs: [(String, UIKeyModifierFlags)] = action == "zoom-in"
                ? [(key, flags), ("=", flags), (key, shifted), ("=", shifted)]
                : [(key, flags)]
            return inputs.map { input, modifiers in
                let command = UIKeyCommand(title: title, action: #selector(RootViewController.performAppKeyCommand(_:)),
                                           input: input, modifierFlags: modifiers, propertyList: action, discoverabilityTitle: title)
                if #available(iOS 15.0, *) { command.wantsPriorityOverSystemBehavior = true }
                return command
            }
        }
        root.handleAppKeyCommand = { [weak self] command in
            guard let action = command.propertyList as? String,
                  let page = self?.shortcutSource(allowDraft: action == "attention" || action.hasPrefix("zoom-")) else { return }
            PluginHost.emit("shiojiUIKit", "navigationShortcut", ["action": action, "window": page.window.id])
        }
    }

    private func shortcutSource(allowDraft: Bool) -> WindowController? {
        guard let tabs, let selected = tabs.selectedViewController else { return nil }
        let nav: UINavigationController?
        if let split, selected === split {
            nav = split.viewController(for: split.isCollapsed ? .primary : .secondary) as? UINavigationController
        } else {
            nav = selected as? UINavigationController
        }
        guard let page = nav?.topViewController as? WindowController else { return nil }
        let ancestors: [UIViewController?] = [Host.shared.root, tabs, selected, nav, page]
        for controller in ancestors {
            guard let modal = controller?.presentedViewController, !(modal is UISearchController) else { continue }
            if allowDraft, modal.presentedViewController == nil, !modal.isBeingDismissed,
               let form = forms.values.first(where: { $0 === modal }),
               let draft = form.topViewController as? WindowController, draft.presentedViewController == nil {
                return draft
            }
            return nil
        }
        return page
    }

    private func dismissForm(_ id: Int) {
        guard let form = forms[id], !form.isBeingDismissed else { return }
        form.dismiss(animated: true) { [weak self] in self?.formDismissed(id) }
    }

    private func formDismissed(_ id: Int) {
        guard forms.removeValue(forKey: id) != nil else { return }
        PluginHost.emit("shiojiUIKit", "formDismissed", id)
    }

    func presentationControllerDidDismiss(_ presentationController: UIPresentationController) {
        if let id = forms.first(where: { $0.value === presentationController.presentedViewController })?.key {
            formDismissed(id)
        }
    }

    private func configureHomeNavigation(_ home: UIViewController) {
        if let screen = home as? WindowController {
            screen.loadViewIfNeeded()
            screen.barAtRoot = false
            screen.titles?.contentInsetAdjustmentBehavior = .never
            screen.window.usesSceneSafeTop = screen.traitCollection.userInterfaceIdiom == .phone
        }
        if let nav = home.navigationController, nav.topViewController === home {
            nav.setNavigationBarHidden(true, animated: false)
        }
    }

    // The Go surface starts inside the safe area. Match the UIKit backing
    // above it so the sidebar color reaches the same top edge in both themes.
    private func sidebarBackground(for home: UIViewController, wide: Bool) {
        let background = wide ? sidebarColor : canvasColor
        home.view.backgroundColor = background
        (home as? WindowController)?.window.backgroundColor = background
        home.navigationController?.view.backgroundColor = background
    }

    private var shouldHideSplitTabs: Bool {
        guard let tabs else { return false }
        if let split, tabs.selectedViewController === split {
            return split.wideLayout
        }
        if tabs.traitCollection.userInterfaceIdiom == .pad, let nav = tabs.selectedViewController as? UINavigationController {
            return nav.viewControllers.count > 1 && nav.topViewController?.hidesBottomBarWhenPushed == true
        }
        return false
    }

    private func syncSplitTabs() {
        guard let tabs else { return }
        let hidden = shouldHideSplitTabs
        if #available(iOS 18.0, *) {
            if tabs.isTabBarHidden != hidden {
                tabs.setTabBarHidden(hidden, animated: false)
            }
        } else {
            tabs.tabBar.isHidden = hidden
        }
        split?.view.setNeedsLayout()
    }

    func tabBarController(_ tabBarController: UITabBarController, didSelect viewController: UIViewController) {
        syncSplitTabs()
        playTabSlide(in: tabBarController)
    }

    func tabBarController(_ tabBarController: UITabBarController, shouldSelect viewController: UIViewController) -> Bool {
        prepareTabSlide(tabBarController, to: viewController)
        return true
    }

    private func prepareTabSlide(_ tabs: UITabBarController, to destination: UIViewController) {
        let roots = tabRoots.isEmpty ? tabs.viewControllers ?? [] : tabRoots
        guard let current = tabs.selectedViewController, current !== destination,
              let previous = roots.firstIndex(of: current), let next = roots.firstIndex(of: destination),
              let content = current.viewIfLoaded, content.window != nil else { return }
        if tabSlide?.destination === destination { return }
        tabSlide?.cover.removeFromSuperview()
        tabSlide = nil
        var body = tabs.view.bounds
        if !tabs.tabBar.isHidden && tabs.tabBar.window != nil {
            let bar = tabs.tabBar.convert(tabs.tabBar.bounds, to: tabs.view)
            if bar.minY > 0 && bar.minY < body.height { body.size.height = bar.minY }
        }
        let source = content.convert(body, from: tabs.view)
        guard let snapshot = content.resizableSnapshotView(from: source, afterScreenUpdates: false, withCapInsets: .zero) else { return }
        let cover = UIView(frame: body)
        cover.isUserInteractionEnabled = false
        cover.accessibilityElementsHidden = true
        cover.clipsToBounds = true
        cover.backgroundColor = canvasColor
        snapshot.frame = cover.bounds
        cover.addSubview(snapshot)
        tabs.view.addSubview(cover)
        tabSlide = TabSlideCover(cover: cover, outgoing: snapshot, destination: destination, direction: previous < next ? 1 : -1)
    }

    private func playTabSlide(in tabs: UITabBarController) {
        guard let slide = tabSlide, !slide.started, let destination = slide.destination,
              tabs.selectedViewController === destination else { return }
        slide.started = true
        tabs.view.bringSubviewToFront(slide.cover)
        let page = (destination as? UINavigationController)?.topViewController as? WindowController
        let surface = page?.window as? SurfaceWindow
        var ready = false
        let reveal = { [weak self, weak tabs, weak destination, weak surface] in
            guard let self, self.tabSlide === slide, !ready else { return }
            ready = true
            surface?.onFirstFrame = nil
            guard let tabs, let content = destination?.viewIfLoaded else {
                slide.cover.removeFromSuperview()
                self.tabSlide = nil
                return
            }
            let visible = content.convert(slide.cover.frame, from: tabs.view)
            guard let incoming = content.resizableSnapshotView(from: visible, afterScreenUpdates: false, withCapInsets: .zero) else {
                slide.cover.removeFromSuperview()
                self.tabSlide = nil
                return
            }
            let distance = slide.direction * slide.cover.bounds.width
            incoming.frame = slide.cover.bounds.offsetBy(dx: distance, dy: 0)
            slide.cover.addSubview(incoming)
            UIView.animate(withDuration: UIAccessibility.isReduceMotionEnabled ? 0 : 0.26, delay: 0,
                           options: [.curveEaseOut, .allowUserInteraction]) {
                slide.outgoing.frame.origin.x = -distance
                incoming.frame.origin.x = 0
            } completion: { [weak self] _ in
                slide.cover.removeFromSuperview()
                if self?.tabSlide === slide { self?.tabSlide = nil }
            }
        }
        let present = { DispatchQueue.main.asyncAfter(deadline: .now() + 1.0 / 30, execute: reveal) }
        if let surface, !surface.hasPresentedFirstFrame {
            surface.onFirstFrame = present
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.50, execute: reveal)
        } else {
            present()
        }
    }

    private func configureTabBarLabels(_ bar: UITabBar) {
        func styleLabels(_ appearance: UITabBarAppearance) -> UITabBarAppearance {
            for layout in [appearance.stackedLayoutAppearance, appearance.inlineLayoutAppearance,
                           appearance.compactInlineLayoutAppearance] {
                for state in [layout.normal, layout.selected, layout.disabled, layout.focused] {
                    var attributes = state.titleTextAttributes
                    attributes[.font] = UIFont.systemFont(ofSize: 10, weight: .medium)
                    state.titleTextAttributes = attributes
                }
            }
            return appearance
        }
        bar.standardAppearance = styleLabels(bar.standardAppearance)
        if let scrollEdge = bar.scrollEdgeAppearance {
            bar.scrollEdgeAppearance = styleLabels(scrollEdge)
        }
    }

    @available(iOS 27.0, *)
    private func installComposeTab(in tabs: UITabBarController, image: UIImage?) {
        guard let roots = tabs.viewControllers, roots.count == 3 else { return }
        tabRoots = roots
        let destinations = roots.enumerated().map { index, root in
            let item = root.tabBarItem
            return UITab(title: item?.title ?? "", image: item?.image, identifier: "shioji.root.\(index)") { _ in root }
        }
        let compose = UITab(title: "新建会话", image: image ?? UIImage(systemName: "square.and.pencil"),
                            identifier: composeTabID) { _ in UIViewController() }
        tabs.tabs = destinations + [compose]
        tabs.selectedTab = destinations[0]
        tabs.prominentTabIdentifier = composeTabID
        PluginHost.emit("shiojiUIKit", "prominentCompose", true)
    }

    @available(iOS 18.0, *)
    func tabBarController(_ tabBarController: UITabBarController, shouldSelectTab tab: UITab) -> Bool {
        guard tab.identifier == composeTabID else {
            if let index = tabBarController.tabs.firstIndex(of: tab), tabRoots.indices.contains(index) {
                prepareTabSlide(tabBarController, to: tabRoots[index])
            }
            return true
        }
        if let home = tabBarController.tabs.first {
            tabBarController.selectedTab = home
            PluginHost.emit("shiojiUIKit", "compose", true)
        }
        return false
    }

    @available(iOS 18.0, *)
    func tabBarController(_ tabBarController: UITabBarController, didSelectTab selectedTab: UITab, previousTab: UITab?) {
        syncSplitTabs()
        playTabSlide(in: tabBarController)
    }

    private func installSplit(home: UIViewController, detail: UIViewController, minimumWidth: CGFloat) {
        guard let primary = home.navigationController, let tabs = primary.tabBarController,
              var controllers = tabs.viewControllers, let index = controllers.firstIndex(of: primary) else { return }
        let split = SessionSplitController(style: .doubleColumn)
        split.tabBarItem = primary.tabBarItem
        split.minimumWidth = minimumWidth
        split.primaryPage = home as? WindowController
        split.detailPage = detail as? WindowController
        if let page = split.detailPage { observeTextDismissal(page) }
        split.tabHost = tabs
        split.updateSidebar = { [weak self, weak home] wide in
            if let home { self?.sidebarBackground(for: home, wide: wide) }
            DispatchQueue.main.async { [weak self] in self?.syncSplitTabs() }
        }
        let wasSelected = tabs.selectedViewController === primary
        controllers[index] = split
        tabRoots = controllers
        tabs.setViewControllers(controllers, animated: false)
        tabs.view.tintColor = .label
        tabs.tabBar.tintColor = .label

        split.preferredSplitBehavior = .tile
        split.preferredDisplayMode = .oneBesideSecondary
        split.preferredPrimaryColumnWidthFraction = 0.3
        split.minimumPrimaryColumnWidth = 280
        split.maximumPrimaryColumnWidth = 320
        split.preferredPrimaryColumnWidth = 320
        split.presentsWithGesture = false
        split.displayModeButtonVisibility = .never
        split.delegate = split
        let secondary = UINavigationController(rootViewController: detail)
        secondary.delegate = primary.delegate
        split.setViewController(primary, for: .primary)
        split.setViewController(secondary, for: .secondary)
        if wasSelected { tabs.selectedViewController = split }
        self.split = split
    }
}

// The guide tracks the keyboard in this scene. The notification's UIKit
// curve also provides a position on keyboard transitions where the guide
// exposes only an endpoint.
private final class KeyboardTiming: NSObject {
    private let guideProbe = UIView()
    private let timingProbe = UIView(frame: CGRect(x: 0, y: 0, width: 1, height: 1))
    private weak var viewport: UIView?
    private var displayLink: CADisplayLink?
    private var started: CFTimeInterval = 0
    private var duration: TimeInterval = 0
    private var bottom: CGFloat = 0
    private var from: CGFloat = 0
    private var target: CGFloat = 0
    private var peak: CGFloat = 0
    private var morphing = false
    private var guideIsMoving = false
    private var last: CGFloat?

    func attach(to view: UIView) {
        guard viewport !== view else { return }
        viewport = view
        guideProbe.isUserInteractionEnabled = false
        guideProbe.accessibilityElementsHidden = true
        guideProbe.translatesAutoresizingMaskIntoConstraints = false
        timingProbe.isUserInteractionEnabled = false
        timingProbe.accessibilityElementsHidden = true
        view.addSubview(timingProbe)
        view.addSubview(guideProbe)
        NSLayoutConstraint.activate([
            guideProbe.topAnchor.constraint(equalTo: view.keyboardLayoutGuide.topAnchor),
            guideProbe.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            guideProbe.widthAnchor.constraint(equalToConstant: 1),
            guideProbe.heightAnchor.constraint(equalToConstant: 1),
        ])
    }

    func animate(from: CGFloat, to: CGFloat, bottom: CGFloat, duration: TimeInterval, curve: Int) {
        let interrupted = displayLink != nil && morphing
        let current = displayLink == nil ? from : last ?? from
        displayLink?.invalidate()
        displayLink = nil
        timingProbe.layer.removeAllAnimations()
        timingProbe.center.y = current
        self.bottom = bottom
        self.from = current
        target = to
        peak = max(from, to, current)
        morphing = interrupted || min(from, to) <= bottom + 80
        self.duration = duration
        started = CACurrentMediaTime()
        guideIsMoving = false
        last = nil
        guard duration > 0, abs(to - current) > 0.25 else {
            Host.shared.root.keyboardPresentationTop = nil
            emitMotion(moving: false)
            return
        }
        emitMotion(moving: true)
        report(coverage: current)
        let options = UIView.AnimationOptions(rawValue: UInt(max(0, curve)) << 16)
        UIView.animate(withDuration: duration, delay: 0, options: [options, .allowUserInteraction]) {
            self.timingProbe.center.y = to
        }
        let link = CADisplayLink(target: self, selector: #selector(sample))
        link.add(to: .main, forMode: .common)
        displayLink = link
    }

    @objc private func sample() {
        guard let viewport else { finish(); return }
        let elapsed = CACurrentMediaTime() - started
        let guide = viewport.bounds.maxY - (guideProbe.layer.presentation()?.frame.minY ?? guideProbe.frame.minY)
        let low = min(from, target)
        let high = max(from, target)
        if guide > low + 0.75, guide < high - 0.75 { guideIsMoving = true }
        let position = guideIsMoving ? guide : timingProbe.layer.presentation()?.position.y ?? from
        if elapsed > duration && (abs(position - target) < 0.75 || elapsed > duration + 0.15) {
            finish()
        } else {
            report(coverage: min(high, max(low, position)))
        }
    }

    private func report(coverage: CGFloat) {
        guard let viewport, let window = viewport.window,
              last.map({ abs(coverage - $0) > 0.15 }) ?? true else { return }
        last = coverage
        Host.shared.root.keyboardPresentationTop = viewport.convert(
            CGPoint(x: 0, y: viewport.bounds.maxY - coverage), to: window.screen.coordinateSpace).y
    }

    private func finish() {
        displayLink?.invalidate()
        displayLink = nil
        Host.shared.root.keyboardPresentationTop = nil
        emitMotion(moving: false)
    }

    private func emitMotion(moving: Bool) {
        PluginHost.emit("shiojiUIKit", "keyboardMotion",
                        ["bottom": bottom, "peak": peak, "moving": moving, "morphing": morphing])
    }
}

private final class SessionSearchField: UISearchTextField {
    var onMove: ((Int) -> Void)?

    override var keyCommands: [UIKeyCommand]? {
        guard markedTextRange == nil, !(text ?? "").isEmpty else { return super.keyCommands }
        let arrows = [UIKeyCommand.inputUpArrow, UIKeyCommand.inputDownArrow].map { key in
            let command = UIKeyCommand(input: key, modifierFlags: [], action: #selector(moveResult(_:)))
            command.wantsPriorityOverSystemBehavior = true
            return command
        }
        return arrows + (super.keyCommands ?? [])
    }

    @objc private func moveResult(_ command: UIKeyCommand) {
        onMove?(command.input == UIKeyCommand.inputUpArrow ? -1 : 1)
    }
}

private final class SessionSearchBar: UIView {
    let field = SessionSearchField()
    var onQuery: ((String) -> Void)?
    var onSubmit: (() -> Void)?
    var onClosing: (() -> Void)?
    var onClose: (() -> Void)?

    private let closeButton = UIButton(type: .close)
    private let capsule = UIView()
    private let glyph = UIImageView(image: UIImage(systemName: "magnifyingglass"))
    private let anchor: CGRect
    private let reduceMotion: Bool
    private var expanded = false
    private var closing = false

    init(width: CGFloat, anchor: CGRect, reduceMotion: Bool) {
        self.reduceMotion = reduceMotion
        self.anchor = CGRect(x: anchor.minX, y: 0, width: anchor.width, height: anchor.height)
        super.init(frame: CGRect(x: 0, y: anchor.minY, width: width, height: anchor.height))
        autoresizingMask = [.flexibleWidth]
        capsule.backgroundColor = .secondarySystemBackground
        capsule.clipsToBounds = true
        capsule.layer.cornerRadius = anchor.height / 2
        capsule.frame = self.anchor
        addSubview(capsule)
        glyph.tintColor = .secondaryLabel
        glyph.contentMode = .scaleAspectFit
        glyph.frame = CGRect(x: (anchor.height - 18) / 2, y: (anchor.height - 18) / 2, width: 18, height: 18)
        capsule.addSubview(glyph)
        field.backgroundColor = .clear
        field.borderStyle = .none
        field.accessibilityIdentifier = "session-search"
        field.accessibilityLabel = "搜索会话"
        field.accessibilityTraits.insert(.searchField)
        field.placeholder = "搜索会话与内容"
        field.autocorrectionType = .no
        field.returnKeyType = .search
        field.clearButtonMode = .whileEditing
        field.alpha = 0
        field.addAction(UIAction { [weak self] _ in
            guard let self, self.field.markedTextRange == nil else { return }
            self.onQuery?(self.field.text ?? "")
        }, for: .editingChanged)
        field.addAction(UIAction { [weak self] _ in
            guard let self, self.field.markedTextRange == nil else { return }
            self.onSubmit?()
        }, for: .editingDidEndOnExit)
        capsule.addSubview(field)
        closeButton.accessibilityLabel = "关闭"
        closeButton.alpha = 0
        closeButton.addAction(UIAction { [weak self] _ in self?.close() }, for: .primaryActionTriggered)
        addSubview(closeButton)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    private var expandedFrame: CGRect {
        CGRect(x: 16, y: 0, width: max(44, bounds.width - 84), height: bounds.height)
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        closeButton.frame = CGRect(x: bounds.width - 60, y: 0, width: 44, height: bounds.height)
        if expanded { capsule.frame = expandedFrame }
        field.frame = CGRect(origin: .zero, size: expandedFrame.size)
    }

    func open() {
        layoutIfNeeded()
        expanded = true
        UIView.animate(withDuration: (reduceMotion || UIAccessibility.isReduceMotionEnabled) ? 0 : 0.23, delay: 0,
                       options: [.beginFromCurrentState, .curveEaseOut, .allowUserInteraction]) {
            self.capsule.frame = self.expandedFrame
            self.field.alpha = 1
            self.closeButton.alpha = 1
            self.glyph.alpha = 0
        }
        field.becomeFirstResponder()
    }

    func close() {
        guard !closing else { return }
        closing = true
        expanded = false
        isUserInteractionEnabled = false
        accessibilityElementsHidden = true
        onClosing?()
        field.resignFirstResponder()
        UIView.animate(withDuration: (reduceMotion || UIAccessibility.isReduceMotionEnabled) ? 0 : 0.23, delay: 0,
                       options: [.beginFromCurrentState, .curveEaseOut, .allowUserInteraction]) {
            self.capsule.frame = self.anchor
            self.field.alpha = 0
            self.closeButton.alpha = 0
            self.glyph.alpha = 1
        } completion: { _ in
            self.removeFromSuperview()
            self.onClose?()
        }
    }
}

private final class TabSlideCover {
    let cover: UIView
    let outgoing: UIView
    weak var destination: UIViewController?
    let direction: CGFloat
    var started = false

    init(cover: UIView, outgoing: UIView, destination: UIViewController, direction: CGFloat) {
        self.cover = cover
        self.outgoing = outgoing
        self.destination = destination
        self.direction = direction
    }
}

private final class ConversationScopeStack: UIStackView {
    override var forFirstBaselineLayout: UIView { arrangedSubviews.last ?? self }
}

private final class ConversationTitleStack: UIStackView {
    override var intrinsicContentSize: CGSize {
        let measured = super.intrinsicContentSize
        // Request the navigation bar's full remaining title area. UIKit
        // still clips this area between its back and trailing controls.
        return CGSize(width: UIView.layoutFittingExpandedSize.width, height: measured.height)
    }
}

private final class SessionSplitController: UISplitViewController, UISplitViewControllerDelegate {
    private var reportedWide: Bool?
    private var forcedCompact = false
    var wideLayout: Bool { reportedWide == true }
    var minimumWidth: CGFloat = 840
    weak var primaryPage: WindowController?
    weak var detailPage: WindowController?
    weak var tabHost: UITabBarController?
    var updateSidebar: ((Bool) -> Void)?

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        let narrow = view.bounds.width < minimumWidth
        alignSidebarToolbar(wide: !narrow && !isCollapsed)
        if narrow != forcedCompact {
            forcedCompact = narrow
            if narrow {
                traitOverrides.horizontalSizeClass = .compact
            } else {
                traitOverrides.remove(UITraitHorizontalSizeClass.self)
            }
            view.setNeedsLayout()
            return
        }
        // UIKit finishes reparenting the primary navigation stack before Go pushes another page.
        if narrow && !isCollapsed { return }
        let wide = !isCollapsed
        guard reportedWide != wide else { return }
        reportedWide = wide
        updateSidebar?(wide)
        let notify: () -> Void = { [weak self] in
            guard let self, self.reportedWide == wide else { return }
            self.alignSidebarToolbar(wide: wide)
            PluginHost.emit("shiojiUIKit", "wideChanged", wide)
        }
        if let transition = transitionCoordinator {
            transition.animate(alongsideTransition: nil) { _ in notify() }
        } else {
            DispatchQueue.main.async(execute: notify)
        }
    }

    private func alignSidebarToolbar(wide: Bool) {
        guard let primary = primaryPage, let detail = detailPage else { return }
        guard wide, view.window != nil else {
            primary.window.contentSafeTop = nil
            detail.window.contentSafeTop = nil
            return
        }
        // UIKit reserves a separate sidebar bar inset. Use the split's single
        // safe edge for both custom toolbars while UIKit owns rotation and search.
        let left = primary.window
        let right = detail.window
        var edge = view.convert(CGPoint(x: 0, y: view.safeAreaInsets.top), to: nil)
        if #available(iOS 18.0, *), tabHost?.isTabBarHidden == true, let window = view.window {
            // The hidden system tab still contributes safe area to its host.
            // The sidebar owns the tabs in this layout, so use the window's
            // status area as the reference for both custom toolbars.
            edge = window.convert(CGPoint(x: 0, y: max(window.safeAreaInsets.top, 33)), to: nil)
        } else {
            if #available(iOS 26.0, *), let guide = tabHost?.contentLayoutGuide,
               let owner = guide.owningView {
                let tabEdge = owner.convert(CGPoint(x: 0, y: guide.layoutFrame.minY), to: nil)
                edge.y = max(edge.y, tabEdge.y)
            }
            if let detailContainer = detail.navigationController?.viewIfLoaded {
                let detailEdge = detailContainer.convert(CGPoint(x: 0, y: detailContainer.safeAreaInsets.top), to: nil)
                edge.y = max(edge.y, detailEdge.y)
            }
        }
        if left.window != nil, !primary.barAtRoot {
            left.contentSafeTop = max(0, left.convert(edge, from: nil).y)
        }
        if right.window != nil {
            right.contentSafeTop = max(0, right.convert(edge, from: nil).y)
        }
    }

    func splitViewController(_ svc: UISplitViewController,
                             topColumnForCollapsingToProposedTopColumn proposedTopColumn: UISplitViewController.Column)
        -> UISplitViewController.Column { .primary }
}

private final class AttachmentPreview: QLPreviewController, QLPreviewControllerDataSource {
    private let item: AttachmentPreviewItem
    private let temporaryDirectory: URL?

    init(url: URL, name: String, temporaryDirectory: URL? = nil) {
        item = AttachmentPreviewItem(url: url, name: name)
        self.temporaryDirectory = temporaryDirectory
        super.init(nibName: nil, bundle: nil)
        dataSource = self
    }

    convenience init(stagedFile: URL, name: String, mime: String) throws {
        let files = FileManager.default
        let directory = files.temporaryDirectory.appendingPathComponent("shioji-preview-" + UUID().uuidString, isDirectory: true)
        let suppliedName = (name as NSString).lastPathComponent
        let basename = ["", ".", "..", "/"].contains(suppliedName) ? "attachment" : suppliedName
        let ext = (basename as NSString).pathExtension.lowercased()
        let declaredType = mime == "application/octet-stream" ? nil : UTType(mimeType: mime)
        let extensionType = UTType(filenameExtension: ext)
        let compatible = declaredType.flatMap { type in
            extensionType.map { $0.conforms(to: type) || type.conforms(to: $0) }
        } ?? (declaredType == nil)
        let previewName = !ext.isEmpty && compatible ? basename : "attachment." + (declaredType?.preferredFilenameExtension ?? "bin")
        let local = directory.appendingPathComponent(previewName, isDirectory: false)
        try files.createDirectory(at: directory, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        do {
            try files.linkItem(at: stagedFile, to: local)
        } catch {
            try? files.removeItem(at: directory)
            throw error
        }
        self.init(url: local, name: name, temporaryDirectory: directory)
    }

    deinit {
        if let temporaryDirectory { try? FileManager.default.removeItem(at: temporaryDirectory) }
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func numberOfPreviewItems(in controller: QLPreviewController) -> Int { 1 }
    func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> QLPreviewItem { item }

    var canShowItem: Bool { QLPreviewController.canPreview(item) }
}

private final class AttachmentPreviewItem: NSObject, QLPreviewItem {
    let previewItemURL: URL?
    let previewItemTitle: String?

    init(url: URL, name: String) {
        previewItemURL = url
        previewItemTitle = name
    }
}
