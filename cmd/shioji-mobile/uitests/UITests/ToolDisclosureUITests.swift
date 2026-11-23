import XCTest

final class ToolDisclosureUITests: XCTestCase {
    func testNestedToolDisclosureRetainsItsPositionInLongHistory() {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = ["--preview-board-transcript-tool-tail-disclosure"]
        app.launch()

        let heading = app.buttons["核对连接恢复"].firstMatch
        let output = app.staticTexts["已比对连接事件游标"].firstMatch
        XCTAssertTrue(heading.waitForExistence(timeout: 12))
        XCTAssertTrue(heading.isHittable)
        XCTAssertFalse(output.exists)

        let position = heading.frame.minY
        heading.tap()
        XCTAssertTrue(output.waitForExistence(timeout: 4), "Expanding a nested tool must keep its output in view")
        XCTAssertTrue(heading.isHittable)
        XCTAssertEqual(heading.frame.minY, position, accuracy: 2)

        heading.tap()
        XCTAssertTrue(output.waitForNonExistence(timeout: 4))
        XCTAssertTrue(heading.isHittable)
        XCTAssertEqual(heading.frame.minY, position, accuracy: 2)
    }
}
