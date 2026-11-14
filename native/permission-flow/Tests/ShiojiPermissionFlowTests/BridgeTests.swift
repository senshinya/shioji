import Foundation
import Testing
@testable import ShiojiPermissionFlow

@Test func rejectsMissingOrNonAppTargets() throws {
    #expect(validatedApp(atPath: "/a/missing.app") == nil)
    #expect(validatedApp(atPath: NSTemporaryDirectory()) == nil)
}

@Test func acceptsAnAppBundleButNotItsExecutable() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let app = root.appendingPathComponent("Shioji Runtime.app")
    let executable = app.appendingPathComponent("Contents/MacOS/runtime")
    try FileManager.default.createDirectory(at: executable.deletingLastPathComponent(), withIntermediateDirectories: true)
    let plist: [String: String] = ["CFBundleIdentifier": "dev.shioji.runtime", "CFBundleExecutable": "runtime", "CFBundlePackageType": "APPL"]
    try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0).write(to: app.appendingPathComponent("Contents/Info.plist"))
    try Data().write(to: executable)
    #expect(validatedApp(atPath: app.path)?.path == app.path)
    #expect(validatedApp(atPath: executable.path) == nil)
}
