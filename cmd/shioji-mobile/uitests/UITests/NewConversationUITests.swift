import XCTest
import UIKit

final class NewConversationUITests: XCTestCase {
    private func launch(_ board: String) -> XCUIApplication {
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = ["--preview-board-" + board]
        app.launch()
        return app
    }

    private func capture(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    func testFirstSubmissionKeepsComposerVisibleWhileWaiting() {
        let app = launch("motion-home-handoff")
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 12))
        send.tap()

        let composer = app.textFields["消息"].firstMatch
        for statuses in [["正在发送"], ["正在准备环境", "正在准备会话"]] {
            let progress = app.staticTexts.matching(NSPredicate(format: "label IN %@", statuses)).firstMatch
            XCTAssertTrue(progress.waitForExistence(timeout: 10))
            capture(progress.label + " composer")
            XCTAssertTrue(composer.exists, "Keep the real composer visible during \(progress.label)")
            XCTAssertNotEqual(composer.value as? String, "核对新会话的准备状态")
            XCTAssertLessThan(progress.frame.maxY, composer.frame.minY)
            XCTAssertFalse(send.exists && send.isEnabled)
        }
    }

    func testAttachmentMenuOpensSystemPhotoPickerAndCancelsBackToDraft() {
        let app = launch("motion-home-handoff")
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        let add = app.buttons["添加附件"].firstMatch
        add.tap()
        capture("Attachment sources")
        let photos = app.buttons["从相册选择图片"].firstMatch
        XCTAssertTrue(photos.waitForExistence(timeout: 4))
        photos.tap()

        let cancel = app.buttons.matching(NSPredicate(format: "label IN %@", ["Cancel", "取消"])).firstMatch
        XCTAssertTrue(cancel.waitForExistence(timeout: 8), app.debugDescription)
        capture("System photo picker")
        cancel.tap()
        XCTAssertTrue(composer.waitForExistence(timeout: 5))
        XCTAssertEqual(composer.value as? String, "核对新会话的准备状态")
    }

    func testPhotoPickerAddsMultiplePhotosToTheSameDraft() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_PHOTO_FIXTURE"] == "1",
                          "Requires an isolated Worker and two photos in the simulator library")
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 12), app.debugDescription)
        compose.tap()

        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        let draft = composer.value as? String
        app.buttons["添加附件"].firstMatch.tap()
        app.buttons["从相册选择图片"].firstMatch.tap()

        let photos = app.images.matching(identifier: "PXGGridLayout-Info")
        let second = photos.element(boundBy: 1)
        XCTAssertTrue(second.waitForExistence(timeout: 12), app.debugDescription)
        capture("Photo library before selecting")
        photos.element(boundBy: 0).tap()
        XCTAssertTrue(second.exists, "The picker must stay open after the first selection")
        second.tap()
        let done = app.buttons.matching(NSPredicate(format: "identifier IN %@ OR label IN %@",
            ["Add", "Done", "添加", "完成"], ["Add", "Done", "添加", "完成"])).firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 5) && done.isEnabled, app.debugDescription)
        capture("Two photos selected in the native picker")
        done.tap()

        let imported = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ AND value == %@", "预览草稿附件 相册图片", "已上传"))
        XCTAssertTrue(imported.element(boundBy: 1).waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertEqual(imported.count, 2)
        XCTAssertTrue(imported.element(boundBy: 0).label.contains("相册图片 1."))
        XCTAssertTrue(imported.element(boundBy: 1).label.contains("相册图片 2."))
        XCTAssertEqual(composer.value as? String, draft)
        capture("Both photos uploaded in the original draft")
    }

    func testNewConversationShowsSubmissionFailureWithoutRecentOperationsEntry() {
        let app = launch("recent-operations")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 8))
        compose.tap()

        XCTAssertTrue(app.buttons["返回草稿"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["最近操作"].exists)
    }
}
