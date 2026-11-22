import XCTest
import UIKit

final class SubmissionStatusUITests: XCTestCase {
    private func launch(_ board: String) -> XCUIApplication {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = ["--preview-board-" + board]
        app.launch()
        return app
    }

    func testSteerUpdatesTheExistingProgressLine() {
        let app = launch("motion-steer-delayed")
        let running = app.staticTexts["进行中"]
        XCTAssertTrue(running.waitForExistence(timeout: 12))
        let progress = running.frame
        app.buttons["发送选项"].firstMatch.tap()
        app.descendants(matching: .any).matching(identifier: "介入当前轮").firstMatch.tap()

        let sending = app.staticTexts["正在发送"]
        XCTAssertTrue(sending.waitForExistence(timeout: 3))
        XCTAssertFalse(running.exists, "The existing progress line should display the sending caption")
        XCTAssertEqual(sending.frame.minX, progress.minX, accuracy: 1)
        XCTAssertEqual(sending.frame.midY, progress.midY, accuracy: 2)

        XCTAssertTrue(running.waitForExistence(timeout: 10))
        XCTAssertFalse(sending.exists)
        XCTAssertLessThan(running.frame.maxY, app.textFields["消息"].firstMatch.frame.minY)
        XCTAssertEqual(running.frame.minX, progress.minX, accuracy: 1)
    }

    func testFirstSubmissionKeepsOneLineThroughPreparationAndRunning() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("motion-home-handoff")
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 12))
        send.tap()

        let sending = app.staticTexts["正在发送"]
        let preparing = app.staticTexts["正在准备环境"]
        let running = app.staticTexts["进行中"]
        let message = app.staticTexts["核对新会话的准备状态"].firstMatch
        XCTAssertTrue(sending.waitForExistence(timeout: 3))
        XCTAssertTrue(message.exists)
        XCTAssertLessThan(message.frame.maxY, sending.frame.minY)
        let line = sending.frame
        XCTAssertTrue(preparing.waitForExistence(timeout: 9))
        XCTAssertFalse(sending.exists)
        XCTAssertEqual(preparing.frame.minX, line.minX, accuracy: 1)
        XCTAssertEqual(preparing.frame.midY, line.midY, accuracy: 1)
        XCTAssertTrue(running.waitForExistence(timeout: 10))
        XCTAssertFalse(preparing.exists)
        if !tablet {
            XCTAssertEqual(running.frame.minX, line.minX, accuracy: 1)
            XCTAssertEqual(running.frame.midY, line.midY, accuracy: 2)
        }
        XCTAssertLessThan(message.frame.maxY, running.frame.minY)
    }

}
