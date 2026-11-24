import XCTest

final class UsageLimitTests: XCTestCase {
    func testUsageNoticeIsAboveEditableDraft() {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = ["--preview-board-usage-limit"]
        app.launch()

        let notice = app.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", "使用额度已用尽")).firstMatch
        if !notice.waitForExistence(timeout: 3) {
            let session = app.buttons["测试断线重连"].firstMatch
            XCTAssertTrue(session.waitForExistence(timeout: 12))
            session.tap()
        }

        let draft = app.textFields["消息"].firstMatch
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 15))
        XCTAssertTrue(app.staticTexts["重置时间暂未知"].exists)
        XCTAssertFalse(app.buttons["用量详情"].exists)
        XCTAssertEqual(draft.value as? String, "再检查一下连续展开两个工具组的情况。")
        XCTAssertTrue(send.isEnabled)
        XCTAssertLessThan(notice.frame.maxY, draft.frame.minY)
    }
}
