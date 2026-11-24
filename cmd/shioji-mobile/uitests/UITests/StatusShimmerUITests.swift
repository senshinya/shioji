import XCTest
import UIKit

final class StatusShimmerUITests: XCTestCase {
    private func launch(_ board: String) -> XCUIApplication {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = ["--preview-board-" + board]
        app.launch()
        return app
    }

    private func visibleFrame(_ label: String, in app: XCUIApplication) -> CGRect {
        let element = app.descendants(matching: .any)[label].firstMatch
        XCTAssertTrue(element.waitForExistence(timeout: 10), app.debugDescription)
        return element.frame
    }

    private func samples(_ frames: [CGRect], in app: XCUIApplication) throws -> [Set<Data>] {
        Thread.sleep(forTimeInterval: 0.4)
        let screenWidth = app.frame.width
        var samples = frames.map { _ in Set<Data>() }
        for _ in 0..<10 {
            let image = XCUIScreen.main.screenshot().image
            let bitmap = try XCTUnwrap(image.cgImage)
            let scale = CGFloat(bitmap.width) / screenWidth
            for (index, frame) in frames.enumerated() {
                let crop = frame.applying(CGAffineTransform(scaleX: scale, y: scale)).integral
                let region = try XCTUnwrap(bitmap.cropping(to: crop))
                samples[index].insert(try XCTUnwrap(UIImage(cgImage: region).pngData()))
            }
            Thread.sleep(forTimeInterval: 0.23)
        }
        return samples
    }

    func testActiveTaskDurationSweepsWithStatus() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom == .pad, "Uses the pushed iPhone plan fixture")
        let app = launch("plan")
        let task = app.buttons["重连回归"]
        XCTAssertTrue(task.waitForExistence(timeout: 10))
        task.tap()
        let frames = try samples(["运行中", "42 秒"].map { visibleFrame($0, in: app) }, in: app)
        XCTAssertGreaterThan(frames[0].count, 1)
        XCTAssertGreaterThan(frames[1].count, 1, "The recorded duration must take part in the live sweep")
    }

    func testWaitingRequestStopsTheSweep() throws {
        let app = launch("request-question")
        let frames = try samples([visibleFrame("等待处理", in: app)], in: app)
        XCTAssertEqual(frames[0].count, 1)
    }

    func testRunningPlanAndWorkListTitlesSweep() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom == .pad, "Uses the pushed iPhone plan fixture")
        let app = launch("plan")
        let plan = visibleFrame("补充接纳回执测试", in: app)
        // The list button exposes the whole task as one accessible element.
        // Crop its elapsed-time column so the breathing dot cannot count.
        let task = app.buttons["重连回归"]
        XCTAssertTrue(task.waitForExistence(timeout: 10))
        let row = task.frame
        let duration = CGRect(x: row.maxX - 68, y: row.minY, width: 44, height: row.height)
        let frames = try samples([plan, duration], in: app)
        XCTAssertGreaterThan(frames[0].count, 1)
        XCTAssertGreaterThan(frames[1].count, 1)
    }

    func testHistoryLoadingCaptionSweeps() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom == .pad, "The cold conversation fixture opens the pushed iPhone page")
        let app = launch("running-session-cold-entry")
        let frames = try samples([visibleFrame("正在读取会话", in: app)], in: app)
        XCTAssertGreaterThan(frames[0].count, 1)
    }
}
