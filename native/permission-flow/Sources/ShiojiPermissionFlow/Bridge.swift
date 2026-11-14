import AppKit
import PermissionFlow

// Only authorization guidance is imported. No status store or protected-file
// probes run in Shioji, and this bridge never changes a system permission.
func validatedApp(atPath path: String) -> URL? {
    let url = URL(fileURLWithPath: path).standardizedFileURL
    guard url.pathExtension == "app", Bundle(url: url)?.bundleIdentifier != nil else { return nil }
    return url
}

@MainActor
private enum Guide {
    static var controller: PermissionFlowController?

    static func open(_ app: URL) -> Int32 {
        guard PermissionFlowResources.packageBundle != nil else { return 3 }
        // authorize() discards the navigation result. Check the real system
        // launch first, then let the controller own navigation and its panel.
        guard NSWorkspace.shared.open(PermissionFlowPane.fullDiskAccess.settingsURL) else { return 4 }
        controller?.closePanel()
        let next = PermissionFlow.makeController(configuration: .init(
            requiredAppURLs: [app],
            promptForAccessibilityTrust: false,
            localeIdentifier: "zh-Hans"
        ))
        controller = next
        // A fresh controller is needed when switching App / Runtime: upstream
        // merges candidates and always displays the first one.
        next.authorize(pane: .fullDiskAccess)
        return 0
    }

    static func close() {
        controller?.closePanel()
        controller = nil
    }
}

@_cdecl("shioji_permission_flow_version")
public func bridgeVersion() -> Int32 { 1 }

@_cdecl("shioji_permission_flow_open")
public func bridgeOpen(_ path: UnsafePointer<CChar>?) -> Int32 {
    guard let path, let app = validatedApp(atPath: String(cString: path)) else { return 1 }
    guard Thread.isMainThread else { return 2 }
    return MainActor.assumeIsolated { Guide.open(app) }
}

@_cdecl("shioji_permission_flow_close")
public func bridgeClose() {
    if Thread.isMainThread {
        MainActor.assumeIsolated { Guide.close() }
    } else {
        DispatchQueue.main.async { Guide.close() }
    }
}
