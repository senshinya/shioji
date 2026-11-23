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
        let preparing = app.staticTexts["正在准备会话"]
        let running = app.staticTexts["进行中"]
        let message = app.staticTexts["核对新会话的准备状态"].firstMatch
        XCTAssertTrue(sending.waitForExistence(timeout: 3))
        XCTAssertTrue(message.exists)
        XCTAssertLessThan(message.frame.maxY, sending.frame.minY)
        let line = sending.frame
        XCTAssertTrue(preparing.waitForExistence(timeout: 9))
        XCTAssertFalse(sending.exists)
        XCTAssertEqual(preparing.frame.minX, line.minX, accuracy: 1)
        if !tablet {
            XCTAssertEqual(preparing.frame.midY, line.midY, accuracy: 1)
        }
        XCTAssertTrue(app.textFields["消息"].firstMatch.exists, "Worker acceptance opens the conversation before the harness confirms delivery")
        XCTAssertTrue(running.waitForExistence(timeout: 10))
        XCTAssertFalse(preparing.exists)
        if !tablet {
            XCTAssertEqual(running.frame.minX, line.minX, accuracy: 1)
            XCTAssertEqual(running.frame.midY, line.midY, accuracy: 2)
        }
        XCTAssertLessThan(message.frame.maxY, running.frame.minY)
    }

    func testSteeringWithKeyboardShowsMessageBeforeTheReceipt() {
        let app = launch("motion-steer-delayed")
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        composer.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        app.buttons["发送选项"].firstMatch.tap()
        app.descendants(matching: .any)["介入当前轮"].firstMatch.tap()

        let input = app.staticTexts.matching(identifier: "插入当前执行轮的消息")
        XCTAssertTrue(app.staticTexts["正在发送"].firstMatch.waitForExistence(timeout: 2))
        XCTAssertTrue(input.firstMatch.exists, "The bubble should take over as the composer clears, before the worker receipt")
        XCTAssertNotEqual(composer.value as? String, "插入当前执行轮的消息")
        XCTAssertTrue(app.staticTexts["进行中"].firstMatch.waitForExistence(timeout: 8))
        XCTAssertEqual(input.count, 1, "The journal item should take over the local bubble")
    }

    func testSteeringWithKeyboardKeepsRunningToolDetailsOpen() {
        let app = launch("motion-steer-tool")
        let composer = app.textFields["消息"].firstMatch
        let heading = app.staticTexts["核对介入前的命令"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertTrue(heading.waitForExistence(timeout: 5))
        composer.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        app.buttons["发送选项"].firstMatch.tap()
        app.descendants(matching: .any)["介入当前轮"].firstMatch.tap()

        let input = app.staticTexts["插入当前执行轮的消息"].firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 8))
        XCTAssertTrue(heading.exists, "Inserting input must preserve the outgoing tool disclosure")
        let copyOutput = app.buttons["复制工具输出"].firstMatch
        XCTAssertTrue(copyOutput.waitForExistence(timeout: 5), "The preceding tool-only reply should have its own footer")
        XCTAssertLessThan(heading.frame.midY, copyOutput.frame.midY)
        XCTAssertLessThan(copyOutput.frame.midY, input.frame.minY)
        XCTAssertLessThan(input.frame.maxY, composer.frame.minY)
    }

}
