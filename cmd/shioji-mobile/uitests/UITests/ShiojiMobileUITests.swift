import XCTest
import UIKit

final class ShiojiMobileUITests: XCTestCase {
    private func launch(_ argument: String) -> XCUIApplication {
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchArguments = [argument]
        app.launch()
        return app
    }

    private func hasVisibleInk(in frame: CGRect) -> Bool {
        let image = XCUIScreen.main.screenshot().image
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let crop = UIGraphicsImageRenderer(size: frame.size, format: format).image { _ in
            image.draw(at: CGPoint(x: -frame.minX, y: -frame.minY))
        }
        guard let bitmap = crop.cgImage else { return false }
        var pixels = [UInt8](repeating: 0, count: bitmap.width * bitmap.height * 4)
        return pixels.withUnsafeMutableBytes { bytes in
            guard let context = CGContext(data: bytes.baseAddress, width: bitmap.width, height: bitmap.height,
                                          bitsPerComponent: 8, bytesPerRow: bitmap.width * 4,
                                          space: CGColorSpaceCreateDeviceRGB(),
                                          bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            context.draw(bitmap, in: CGRect(x: 0, y: 0, width: bitmap.width, height: bitmap.height))
            var darkest = 255
            var lightest = 0
            for index in stride(from: 0, to: bytes.count, by: 4) {
                let luminance = (Int(bytes[index]) + Int(bytes[index + 1]) + Int(bytes[index + 2])) / 3
                darkest = min(darkest, luminance)
                lightest = max(lightest, luminance)
            }
            return lightest - darkest > 45
        }
    }

    private func capture(_ name: String, app: XCUIApplication) {
        let att = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        att.name = name
        att.lifetime = .keepAlways
        add(att)
    }

    private func joinFixtureTailnet(_ peerName: String, app: XCUIApplication) {
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        let network = app.buttons["Tailnet"].firstMatch
        XCTAssertTrue(network.waitForExistence(timeout: 8))
        network.tap()
        let join = app.buttons["在浏览器授权"]
        XCTAssertTrue(join.waitForExistence(timeout: 8))
        join.tap()
        XCTAssertTrue(app.buttons[peerName].waitForExistence(timeout: 30), app.debugDescription)
        app.navigationBars["Tailnet"].buttons["BackButton"].firstMatch.tap()
        app.buttons["会话"].firstMatch.tap()
    }

    private func captureBoards(_ cases: [(String, String)], tablet: Bool = false) {
        for (board, title) in cases {
            XCTContext.runActivity(named: board) { _ in
                if tablet {
                    let orientation: UIDeviceOrientation = board == "pad-portrait" ? .portrait : .landscapeLeft
                    if XCUIDevice.shared.orientation != orientation {
                        XCUIDevice.shared.orientation = orientation
                    }
                }
                let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
                app.launchArguments = ["--preview-board-" + board]
                app.launch()
                let target = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch
                if target.waitForExistence(timeout: 8) {
                    // UIKit exposes the incoming accessibility tree before its push has finished.
                    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.65))
                    if board == "create", app.buttons["发送"].exists {
                        print("CREATE-SEND-GEOMETRY", app.frame, app.buttons["发送"].frame)
                    }
                    capture("Board " + board, app: app)
                } else {
                    capture("Mismatch " + board, app: app)
                    XCTFail("Missing expected native control for " + board + ": " + title)
                }
                app.terminate()
            }
        }
    }

    func testFrozenPhoneBoardsConversation() {
        captureBoards([
            ("inbox", "切换会话范围"), ("chat", "会话活动"), ("approval", "命令审批"),
            ("create", "新建会话"), ("diff", "internal/transport/connection.go"), ("file", "connection.go"),
            ("request-question", "回答问题"), ("request-access", "文件与权限审批"),
            ("attention", "待我处理"), ("operation-receipt", "消息是否送达仍待确认"), ("queue-manager", "待发消息操作"),
            ("steer", "发送选项"), ("plan", "计划与任务"), ("child-agent", "连接验证"),
            ("background-task", "重连回归"), ("message-search", "全文搜索"), ("history-match", "会话活动"),
        ])
    }

    func testFrozenPhoneBoardsDevices() {
        captureBoards([
            ("device-list", "MacBook Pro"), ("pair", "检查连接"), ("verify", "信任主机，继续配置 SSH"),
            ("device-detail", "Mac mini"), ("offline", "重连"), ("ssh-auth", "验证 SSH 登录"),
            ("worker-setup", "执行服务"), ("agent-config", "Codex"), ("workspace-config", "工作区设置"),
            ("directory-picker", "选择远程目录"), ("tailnet-setup", "Tailnet"), ("device-health", "设备诊断"),
            ("hook-review", "Hooks"), ("port-forwards", "端口转发"), ("import-session", "导入会话"),
        ])
    }

    func testForkOpensConversationWithoutAnIntermediatePage() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-fork-session")
        let fork = app.buttons["从这里分叉"].firstMatch
        XCTAssertTrue(fork.waitForExistence(timeout: 8), app.debugDescription)
        let originalDraft = app.textFields["消息"].firstMatch
        XCTAssertEqual(originalDraft.value as? String, "保留原会话草稿")
        fork.tap()
        let title = app.staticTexts.matching(NSPredicate(format: "label == %@", "测试断线重连（分叉）")).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(app.navigationBars["分叉会话"].exists)
        XCTAssertFalse(app.buttons["创建分叉"].exists)
        let sourceReply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "已把重连和执行状态分开处理")).firstMatch
        XCTAssertTrue(sourceReply.waitForExistence(timeout: 8))
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "也确认一下，重复连线时")).firstMatch.exists)
        XCTAssertNotEqual(app.textFields["消息"].firstMatch.value as? String, "保留原会话草稿")
        capture("Board fork-session", app: app)
    }

    func testComposerHasRoomForAccessibilityText() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_AUDIT_LARGE_TEXT"] == "1",
                          "Requires an accessibility Dynamic Type setting")
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("--preview-board-create")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 10), app.debugDescription)
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        capture("Draft composer at accessibility text size", app: app)
        XCTAssertGreaterThanOrEqual(editor.frame.height, 37)
        XCTAssertLessThanOrEqual(editor.frame.maxY, send.frame.minY)
        app.terminate()

        let conversation = launch("--preview-board-chat")
        let message = conversation.textFields["消息"].firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 10), conversation.debugDescription)
        capture("Conversation composer at accessibility text size", app: conversation)
        XCTAssertGreaterThanOrEqual(message.frame.height, 37)
    }

    func testFrozenPhoneBoardsManagement() {
        captureBoards([
            ("session-archive", "已归档"),
            ("branch-switch", "切换分支"), ("diff-compare", "交换基准与目标"), ("diff-annotation", "代码选区评论"),
            ("reference-picker", "插入引用"), ("attachment-transfer", "附件"),
            ("schedule-list", "定时任务"), ("schedule-editor", "编辑定时任务"),
            ("schedule-detail", "任务详情"), ("schedule-run", "运行回执"),
            ("notification-prefs", "通知"), ("input-prefs", "输入与外观"),
        ])
    }

    func testConnectedDeviceAppearanceFollowsTheDeviceAndPersists() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_DEVICE_APPEARANCE_FIXTURE"] == "1" else {
            throw XCTSkip("Requires two enrolled Workers")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        let host = "127.0.0.1"
        let alias = "Studio Shelf"
        let devices = app.buttons["设备"].firstMatch
        let edit = app.buttons["编辑设备"].firstMatch
        let editor = app.navigationBars["编辑设备"].firstMatch
        let name = app.textFields["设备显示名称"].firstMatch
        let icon = app.buttons["设备图标"].firstMatch
        app.launch()
        XCTAssertTrue(devices.waitForExistence(timeout: 12))
        devices.tap()
        let original = app.buttons[host].firstMatch
        XCTAssertTrue(original.waitForExistence(timeout: 10))
        original.tap()
        XCTAssertTrue(app.navigationBars[host].firstMatch.waitForExistence(timeout: 8))
        capture("Device detail before editing", app: app)
        XCTAssertTrue(edit.waitForExistence(timeout: 6))
        edit.tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 6))
        XCTAssertTrue(name.waitForExistence(timeout: 6))
        name.tap()
        app.typeText("Discard this")
        editor.buttons["BackButton"].tap()
        XCTAssertTrue(app.navigationBars[host].firstMatch.waitForExistence(timeout: 6))

        edit.tap()
        XCTAssertTrue(name.waitForExistence(timeout: 6))
        XCTAssertFalse((name.value as? String ?? "").contains("Discard this"))
        name.tap()
        app.typeText(alias)
        XCTAssertTrue(icon.waitForExistence(timeout: 6))
        icon.tap()
        let server = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "服务器")).firstMatch
        XCTAssertTrue(server.waitForExistence(timeout: 6))
        server.tap()
        XCTAssertEqual(icon.value as? String, "服务器")
        capture("Device appearance uses the shared native form", app: app)
        app.buttons["保存设备外观"].firstMatch.tap()
        let renamed = app.navigationBars[alias].firstMatch
        XCTAssertTrue(renamed.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(app.staticTexts[host].firstMatch.exists)
        renamed.buttons["BackButton"].tap()
        XCTAssertTrue(app.buttons[alias].firstMatch.waitForExistence(timeout: 6))
        XCTAssertEqual(app.buttons.matching(identifier: host).count, 1)
        app.buttons["会话"].firstMatch.tap()
        app.buttons["搜索会话"].firstMatch.tap()
        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        search.tap()
        search.typeText(alias)
        XCTAssertTrue(app.buttons["当前设备的归档校验"].firstMatch.waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["另一设备的归档校验"].firstMatch.waitForNonExistence(timeout: 6))

        app.terminate()
        app.launch()
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        devices.tap()
        let persisted = app.buttons[alias].firstMatch
        XCTAssertTrue(persisted.waitForExistence(timeout: 8))
        XCTAssertEqual(app.buttons.matching(identifier: host).count, 1)
        capture("Renamed device and other device stay distinct", app: app)
        persisted.tap()
        XCTAssertTrue(edit.waitForExistence(timeout: 8))
        edit.tap()
        XCTAssertTrue(name.waitForExistence(timeout: 6))
        XCTAssertEqual(name.value as? String, alias)
        XCTAssertEqual(icon.value as? String, "服务器")
    }

    func testDeviceSetupKeepsPrimaryActionsAtTheBottom() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        for (route, formLabel, actionLabel) in [
            ("pair", "主机", "检查连接"),
            ("verify", "密钥类型", "取消"),
            ("worker-setup", "尚未安装", "安装执行服务"),
        ] {
            XCTContext.runActivity(named: route) { _ in
                let app = launch("--preview-board-" + route)
                let form = app.staticTexts[formLabel].firstMatch
                let action = app.buttons[actionLabel].firstMatch
                XCTAssertTrue(form.waitForExistence(timeout: 8))
                XCTAssertTrue(action.waitForExistence(timeout: 8))
                capture("Setup footer " + route, app: app)
                XCTAssertLessThan(app.frame.maxY - action.frame.maxY, 80)
                if route == "pair" {
                    XCTAssertEqual(app.staticTexts.matching(identifier: "连接方式").count, 1)
                    if !tablet {
                        let username = app.textFields["用户名"].firstMatch
                        username.tap()
                        let keyboard = app.keyboards.firstMatch
                        XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
                        XCTAssertTrue(action.isHittable)
                        XCTAssertLessThanOrEqual(action.frame.maxY, keyboard.frame.minY)
                        capture("Pair footer with keyboard", app: app)
                    }
                } else if route == "worker-setup" {
                    XCTAssertFalse(app.staticTexts["Shioji Worker"].exists)
                }
                app.terminate()
            }
        }
    }

    func testAttachmentResultHasABottomActionAndShowsItsContents() {
        let app = launch("--preview-board-attachment-transfer")
        let read = app.buttons["读取附件"]
        XCTAssertTrue(read.waitForExistence(timeout: 8))
        capture("Remote attachment before reading", app: app)
        XCTAssertLessThan(app.frame.maxY - read.frame.maxY, 80)
        XCTAssertTrue(app.buttons["选择本机文件"].exists)
        read.tap()
        let content = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "4 组用例通过")).firstMatch
        XCTAssertTrue(content.waitForExistence(timeout: 5))
        XCTAssertFalse(read.exists)
        capture("Remote attachment after reading", app: app)
    }

    func testNavigationPagesUseCompactTopSpacing() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let cases = [
            ("device-list", "远程设备"), ("device-detail", "连接"),
            ("input-prefs", "外观"), ("notification-prefs", "系统通知"),
            ("attention", "需要行动"), ("plan", "计划步骤"),
            ("schedule-list", "已安排"), ("schedule-editor", "任务"),
        ]
        var firstSectionGap: CGFloat?
        for (route, heading) in cases {
            XCTContext.runActivity(named: route) { _ in
                let app = launch("--preview-board-" + route)
                continueAfterFailure = true
                let bar = app.navigationBars.firstMatch
                let content = app.staticTexts[heading].firstMatch
                XCTAssertTrue(bar.waitForExistence(timeout: 6), route)
                XCTAssertTrue(content.waitForExistence(timeout: 6), route)
                guard bar.exists, content.exists else { return }
                let gap = content.frame.minY - bar.frame.maxY
                print("SHIOJI-PAGE-START \(route) \(gap) pt")
                capture("Navigation spacing \(route)", app: app)
                XCTAssertGreaterThanOrEqual(gap, 0, route)
                XCTAssertLessThanOrEqual(gap, 22, route)
                if let firstSectionGap {
                    XCTAssertEqual(gap, firstSectionGap, accuracy: 1, "First section alignment on \(route)")
                } else {
                    firstSectionGap = gap
                }
            }
        }
    }

    func testGoalDockAndContinuationStayInConversation() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-goal")
        let goal = app.buttons["目标摘要"].firstMatch
        XCTAssertTrue(goal.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue((goal.value as? String)?.contains("执行中") ?? false)
        let continuation = app.staticTexts["目标自动续跑"].firstMatch
        XCTAssertTrue(continuation.exists)
        let hook = app.staticTexts["已补充工作区上下文"].firstMatch
        XCTAssertTrue(hook.exists)
        XCTAssertLessThan(continuation.frame.minY, hook.frame.minY)
        capture("Goal continuation marker and dock", app: app)

        let pause = app.buttons["暂停目标"].firstMatch
        XCTAssertGreaterThanOrEqual(pause.frame.height, 44)
        pause.tap()
        let resume = app.buttons["恢复目标"].firstMatch
        XCTAssertTrue(resume.waitForExistence(timeout: 5))
        XCTAssertTrue((goal.value as? String)?.contains("已暂停") ?? false)
        goal.tap()
        capture("Paused Goal with full objective", app: app)
        resume.tap()
        XCTAssertTrue(pause.waitForExistence(timeout: 5))
        app.buttons["目标操作"].firstMatch.tap()
        app.descendants(matching: .any)["清除目标"].firstMatch.tap()
        let confirmation = app.alerts["清除目标？"].firstMatch
        XCTAssertTrue(confirmation.waitForExistence(timeout: 5))
        confirmation.buttons["清除目标"].tap()
        XCTAssertTrue(goal.waitForNonExistence(timeout: 6))
        XCTAssertTrue(continuation.exists)
    }

    func testGoalSlashCommandUsesAnInlineTagAndChecksWorkerSupport() {
        let app = launch("--preview-board-session-goal")
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        composer.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        app.typeText("/go")
        let command = app.buttons["设置目标"].firstMatch
        XCTAssertTrue(command.waitForExistence(timeout: 6), app.debugDescription)
        capture("Goal shown among slash commands", app: app)
        command.tap()
        XCTAssertTrue(command.waitForNonExistence(timeout: 5))
        app.typeText("Verify queue receipt")
        XCTAssertTrue((composer.value as? String)?.contains("Verify queue receipt") ?? false)
        XCTAssertTrue(app.buttons["加入待发队列"].isEnabled)
        XCTAssertGreaterThanOrEqual(app.keyboards.firstMatch.frame.minY - app.buttons["加入待发队列"].frame.maxY, 8)
        capture("Goal tag retains the objective in the composer", app: app)

        app.terminate()
        app.launchArguments = ["--preview-board-session-goal-legacy"]
        app.launch()
        XCTAssertTrue(app.buttons["目标摘要"].waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["目标操作"].exists)
        composer.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        app.typeText("/go")
        XCTAssertFalse(command.waitForExistence(timeout: 2))
    }

    func testGoalRoundTripWithOwningWorker() throws {
        guard let progress = ProcessInfo.processInfo.environment["SHIOJI_GOAL_WORKER_PROGRESS"],
              let progressURL = URL(string: progress) else {
            throw XCTSkip("Requires the isolated Goal Worker fixture")
        }
        func expectModelInput(_ turn: Int) {
            let submitted = expectation(description: "The Worker delivered model input for turn \(turn)")
            let url = progressURL.appendingPathComponent(String(turn))
            URLSession.shared.dataTask(with: url) { _, response, _ in
                XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
                submitted.fulfill()
            }.resume()
            wait(for: [submitted], timeout: 17)
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let workspace = app.buttons["切换会话范围"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 18))
        let button = app.tabBars.buttons["新建会话"].firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 12))
        button.tap()
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10), app.debugDescription)
        if !((composer.value as? String)?.contains("SHIOJI_SLOW") ?? false) {
            composer.tap()
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
            app.typeText("/go")
            let command = app.buttons["设置目标"].firstMatch
            XCTAssertTrue(command.waitForExistence(timeout: 8), app.debugDescription)
            command.tap()
            app.typeText("SHIOJI_SLOW verify owning workspace")
        }
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.isEnabled)
        send.tap()
        let goal = app.buttons["目标摘要"].firstMatch
        if !goal.waitForExistence(timeout: 3), app.navigationBars["新建会话"].exists, send.exists, send.isEnabled,
           !app.keyboards.firstMatch.exists, (composer.value as? String)?.contains("SHIOJI_SLOW") == true {
            // On the simulator the first tap can only dismiss the moving keyboard.
            // A disabled send button belongs to an in-flight operation and is never retried.
            send.tap()
        }
        XCTAssertTrue(goal.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue((goal.value as? String)?.contains("SHIOJI_SLOW verify owning workspace") ?? false)
        expectModelInput(1)
        capture("Goal accepted by the owning device", app: app)
        let pause = app.buttons["暂停目标"].firstMatch
        XCTAssertTrue(pause.waitForExistence(timeout: 8))
        pause.tap()
        let resume = app.buttons["恢复目标"].firstMatch
        XCTAssertTrue(resume.waitForExistence(timeout: 12), app.debugDescription)
        let stop = app.buttons["停止"].firstMatch
        XCTAssertTrue(stop.waitForExistence(timeout: 12))
        stop.tap()
        let ready = NSPredicate { _, _ in resume.exists && resume.isEnabled }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: ready, object: resume)], timeout: 14), .completed)
        resume.tap()
        XCTAssertTrue(pause.waitForExistence(timeout: 12))
        XCTAssertTrue(stop.waitForExistence(timeout: 14))
        expectModelInput(2)
        capture("Goal resumed on the same Worker", app: app)
        let composerBottom = composer.frame.maxY
        app.buttons["目标操作"].firstMatch.tap()
        app.descendants(matching: .any)["清除目标"].firstMatch.tap()
        let confirm = app.alerts["清除目标？"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.buttons["清除目标"].tap()
        XCTAssertTrue(goal.waitForNonExistence(timeout: 12))
        XCTAssertEqual(composer.frame.maxY, composerBottom, accuracy: 1)
        capture("Goal cleared with the composer anchored", app: app)
        if stop.exists { stop.tap() }
    }

    func testLatestGoalRoundIncludesCompactionAndHookBeforeItsMarker() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_GOAL_PREFIX_WORKER"] == "1" else {
            throw XCTSkip("Requires the isolated Worker with a Goal round and pre-marker history")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons["核对自动续跑的历史页"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15), app.debugDescription)
        session.tap()
        let marker = app.staticTexts["目标自动续跑"]
        let compact = app.staticTexts["上下文已压缩"]
        let hook = app.buttons["已补充工作区上下文"]
        let reply = app.staticTexts["本轮续跑已核对。"]
        let earlier = app.staticTexts["前一轮核对完成。"]
        XCTAssertTrue(reply.waitForExistence(timeout: 10), app.debugDescription)
        capture("Latest Goal round from the Worker", app: app)
        XCTAssertTrue(marker.exists)
        XCTAssertTrue(compact.exists, "A one-round history page must retain its pre-marker compaction")
        XCTAssertTrue(hook.exists, "A one-round history page must retain its pre-marker context")
        XCTAssertFalse(earlier.exists)
        XCTAssertLessThan(marker.frame.midY, compact.frame.midY)
        XCTAssertLessThan(compact.frame.midY, hook.frame.midY)
        XCTAssertLessThan(hook.frame.midY, reply.frame.midY)
        hook.tap()
        XCTAssertTrue(app.staticTexts["已重新读取工作区操作说明。"].waitForExistence(timeout: 6))
        capture("Context opened within the same Goal round", app: app)

        for _ in 0..<4 where !earlier.isHittable { app.swipeDown() }
        XCTAssertTrue(earlier.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(app.buttons["加载更早消息"].exists)
        XCTAssertEqual(app.staticTexts.matching(identifier: "目标自动续跑").count, 1)
        XCTAssertEqual(app.staticTexts.matching(identifier: "上下文已压缩").count, 1)
        capture("Earlier history loaded without duplicating the Goal records", app: app)
    }

    func testAutomaticGoalContinuationRendersWorkerHistory() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let endpoint = environment["SHIOJI_GOAL_CONTINUATION_ENDPOINT"], let progress = URL(string: endpoint),
              let mode = environment["SHIOJI_GOAL_CONTINUATION_MODE"] else {
            throw XCTSkip("Requires an isolated Codex Goal continuation")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons["自动续跑验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 22), app.debugDescription)
        session.tap()
        let goal = app.buttons["目标摘要"].firstMatch
        XCTAssertTrue(goal.waitForExistence(timeout: 12))

        func finishModelResponse(_ number: Int) {
            let reply = expectation(description: "Model response \(number) was released")
            var request = URLRequest(url: progress.appendingPathComponent(String(number)))
            request.httpMethod = "POST"
            URLSession.shared.dataTask(with: request) { _, response, _ in
                XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
                reply.fulfill()
            }.resume()
            wait(for: [reply], timeout: 17)
        }

        let replies = app.staticTexts.matching(identifier: "SHIOJI_SLOW_DONE")
        let marker = app.staticTexts["目标自动续跑"]
        let twoReplies = NSPredicate { _, _ in replies.count == 2 }
        if mode != "history" {
            finishModelResponse(1)
            XCTAssertTrue(replies.firstMatch.waitForExistence(timeout: 10))
            XCTAssertFalse(marker.exists)
            XCTAssertEqual(replies.count, 1)
            capture("The first reply appears while the continuation runs", app: app)
            app.buttons["暂停目标"].tap()
            XCTAssertTrue(app.buttons["恢复目标"].waitForExistence(timeout: 10))
            finishModelResponse(2)
        }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: twoReplies, object: app)], timeout: 12), .completed)
        if mode == "before" {
            XCTAssertFalse(marker.exists)
            capture("Worker history before continuation events", app: app)
            return
        }
        func assertMarkerBetweenReplies() {
            XCTAssertTrue(marker.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertEqual(app.staticTexts.matching(identifier: "目标自动续跑").count, 1)
            let frames = replies.allElementsBoundByIndex.map(\.frame).sorted { $0.minY < $1.minY }
            XCTAssertEqual(frames.count, 2)
            guard frames.count == 2 else { return }
            XCTAssertLessThan(frames[0].maxY, marker.frame.minY)
            XCTAssertLessThan(marker.frame.maxY, frames[1].minY)
        }
        assertMarkerBetweenReplies()
        capture(tablet ? "iPad reads the persisted continuation" : "Continuation arrives between real replies", app: app)
        if !tablet {
            app.terminate()
            app.launch()
            XCTAssertTrue(session.waitForExistence(timeout: 12))
            session.tap()
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: twoReplies, object: app)], timeout: 12), .completed)
            assertMarkerBetweenReplies()
            capture("Reopened iPhone history retains one marker", app: app)
        }
    }

    func testArchiveDeleteProgressReusesTheActionRow() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        var app = launch("--preview-board-session-archive")
        let search = app.textFields["搜索标题或工作区"].firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 8))
        let count = app.staticTexts["4 个会话"].firstMatch
        let deleteAll = app.buttons["删除全部"].firstMatch
        let first = app.buttons["旧版 SSH 配置迁移"].firstMatch
        XCTAssertTrue(count.exists)
        XCTAssertTrue(deleteAll.exists)
        XCTAssertTrue(first.exists)
        let searchFrame = search.frame
        let countFrame = count.frame
        let firstY = first.frame.minY
        let actionCenter = deleteAll.frame.midY
        capture("Archive ready to delete", app: app)
        app.terminate()

        app = launch("--preview-board-session-archive-progress")
        let activeSearch = app.textFields["搜索标题或工作区"].firstMatch
        XCTAssertTrue(activeSearch.waitForExistence(timeout: 8))
        let progress = app.descendants(matching: .any).matching(identifier: "删除中 1/2").firstMatch
        let activeCount = app.staticTexts["4 个会话"].firstMatch
        XCTAssertTrue(progress.exists)
        XCTAssertTrue(activeCount.exists)
        capture("Archive deletion progress", app: app)
        XCTAssertLessThanOrEqual(activeSearch.frame.maxY, activeCount.frame.minY)
        XCTAssertEqual(progress.frame.midY, activeCount.frame.midY, accuracy: 1)
        XCTAssertEqual(progress.frame.midY, actionCenter, accuracy: 1)
        XCTAssertLessThan(activeCount.frame.maxX, progress.frame.minX)
        XCTAssertLessThan(activeCount.frame.minX, activeSearch.frame.minX)
        XCTAssertEqual(activeCount.frame.minX, countFrame.minX, accuracy: 1)
        XCTAssertEqual(activeSearch.frame.minX, searchFrame.minX, accuracy: 1)
        XCTAssertEqual(activeSearch.frame.width, searchFrame.width, accuracy: 1)
        XCTAssertEqual(app.buttons["旧版 SSH 配置迁移"].firstMatch.frame.minY, firstY, accuracy: 1)
        XCTAssertFalse(app.buttons["删除全部"].exists)
    }

    func testArchiveSearchAndScopedDeletion() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-archive")
        XCTAssertTrue(app.navigationBars["已归档"].waitForExistence(timeout: 8))
        let first = app.buttons["旧版 SSH 配置迁移"]
        XCTAssertTrue(first.waitForExistence(timeout: 6))
        if tablet {
            let sidebar = app.buttons["会话视图"].firstMatch
            XCTAssertTrue(sidebar.exists)
            XCTAssertLessThan(sidebar.frame.maxX, first.frame.minX)
        }
        capture("Archived sessions before row actions", app: app)
        let delete = app.descendants(matching: .any)["彻底删除"].firstMatch
        first.press(forDuration: 0.8)
        guard delete.waitForExistence(timeout: 5) else { return XCTFail("Long press must open archived session actions") }
        let preview = app.descendants(matching: .any)["archiveConversationPreview"].firstMatch
        XCTAssertTrue(preview.waitForExistence(timeout: 5), "An archived session must preview its conversation")
        XCTAssertLessThan(preview.frame.height, app.frame.height * 0.72)
        XCTAssertFalse(preview.descendants(matching: .any)["消息"].exists)
        let recent = ["开始迁移前检查", "核对新配置的 SSH 主机。", "SSH 主机核验通过。", "旧配置可以清理了吗？", "已保留旧配置的备份。"]
        var priorBottom = preview.frame.minY + 36
        for message in recent {
            let record = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", message)).firstMatch
            XCTAssertTrue(record.waitForExistence(timeout: 5), message)
            XCTAssertGreaterThan(record.frame.minY, priorBottom, message)
            priorBottom = record.frame.minY
        }
        XCTAssertTrue(app.buttons["复制消息"].firstMatch.exists)
        XCTAssertTrue(app.buttons["复制回复"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["用时 2秒"].firstMatch.exists)
        let lastFooterAction = app.buttons.matching(identifier: "复制回复").allElementsBoundByIndex.last
        XCTAssertNotNil(lastFooterAction)
        if let lastFooterAction {
            let fitted = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                let gap = preview.frame.maxY - lastFooterAction.frame.maxY
                return (2...6).contains(gap)
            }, object: nil)
            XCTAssertEqual(XCTWaiter.wait(for: [fitted], timeout: 4), .completed,
                           "The last reply should leave one 4-point item gap. Measured: \(preview.frame.maxY - lastFooterAction.frame.maxY)")
        }
        XCTAssertFalse(app.sheets.firstMatch.exists, "Row actions must appear in a native context menu, not an action sheet")
        XCTAssertFalse(app.buttons["归档记录操作：旧版 SSH 配置迁移"].exists)
        XCTAssertTrue(app.descendants(matching: .any)["恢复会话"].firstMatch.exists)
        XCTAssertTrue(app.navigationBars["已归档"].exists)
        capture("Archived session long-press menu", app: app)
        delete.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        capture("Native confirmation identifies an archived session", app: app)
        alert.buttons["取消"].tap()
        XCTAssertTrue(first.exists)
        first.tap()
        let conversation = app.navigationBars["旧版 SSH 配置迁移"].firstMatch
        XCTAssertTrue(conversation.waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["复制消息"].firstMatch.exists)
        XCTAssertTrue(app.buttons["复制回复"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["用时 2秒"].firstMatch.exists)
        conversation.buttons["BackButton"].tap()
        XCTAssertTrue(first.waitForExistence(timeout: 8))
        first.press(forDuration: 0.8)
        XCTAssertTrue(delete.waitForExistence(timeout: 5))
        delete.tap()
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        alert.buttons["彻底删除"].tap()
        XCTAssertTrue(first.waitForNonExistence(timeout: 8))
        XCTAssertTrue(app.buttons["整理运行日志"].exists)

        app.buttons["工作区"].tap()
        let workspace = app.buttons["旧版构建工具"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 6))
        workspace.tap()
        XCTAssertTrue(app.navigationBars["旧版构建工具"].waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["保留 iOS 构建记录"].exists)
        let search = app.textFields["搜索标题或工作区"]
        search.tap()
        app.typeText("iOS")
        XCTAssertTrue(app.staticTexts["1 个会话"].waitForExistence(timeout: 6))
        XCTAssertFalse(app.buttons["macOS 测试历史"].exists)
        app.buttons["删除全部"].tap()
        XCTAssertTrue(alert.waitForExistence(timeout: 6))
        let count = alert.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "2 个归档会话")).firstMatch
        XCTAssertTrue(count.exists, app.debugDescription)
        capture("Filtered workspace shows the whole deletion scope", app: app)
        alert.buttons["彻底删除"].tap()
        XCTAssertTrue(app.staticTexts["没有归档会话"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.navigationBars["旧版构建工具"].exists)
        capture("The archived workspace remains after scoped session deletion", app: app)
        app.navigationBars["旧版构建工具"].buttons.firstMatch.tap()
        XCTAssertTrue(workspace.waitForExistence(timeout: 6))
    }

    func testFleetArchiveAggregatesAndRoutesOperationsToBothWorkers() throws {
        guard let remoteAddress = ProcessInfo.processInfo.environment["SHIOJI_FLEET_ARCHIVE_REMOTE"] else {
            throw XCTSkip("Requires two enrolled Workers with archives")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        func choose(_ title: String) {
            let item = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch
            XCTAssertTrue(item.waitForExistence(timeout: 6), title)
            item.tap()
        }
        func archivedRow(_ title: String) -> XCUIElement {
            app.buttons.matching(NSPredicate(format: "label == %@ AND value CONTAINS %@", title, "只读")).firstMatch
        }
        let local = archivedRow("当前设备的独立归档")
        let remote = archivedRow("另一设备的独立归档")
        XCTAssertTrue(app.buttons["会话视图"].firstMatch.waitForExistence(timeout: 12))
        app.buttons["会话视图"].firstMatch.tap()
        choose("归档")
        let archive = app.navigationBars["已归档"]
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        XCTAssertTrue(local.waitForExistence(timeout: 12))
        XCTAssertTrue(remote.waitForExistence(timeout: 12))
        capture("Archive from both enrolled Workers", app: app)
        let retention = app.buttons[remoteAddress].firstMatch
        XCTAssertTrue(retention.waitForExistence(timeout: 8))
        retention.tap()
        choose("7 天")
        let updated = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "7 天"), object: retention)
        XCTAssertEqual(XCTWaiter.wait(for: [updated], timeout: 8), .completed)
        let search = app.textFields["搜索标题或工作区"].firstMatch
        search.tap()
        app.typeText(remoteAddress)
        XCTAssertTrue(local.waitForNonExistence(timeout: 8))
        XCTAssertTrue(remote.exists)
        app.buttons["清除归档搜索"].firstMatch.tap()
        XCTAssertTrue(local.waitForExistence(timeout: 8))
        remote.press(forDuration: 0.8)
        XCTAssertTrue(app.staticTexts["另一设备的归档正文。"].firstMatch.waitForExistence(timeout: 12))
        choose("恢复会话")
        XCTAssertTrue(remote.waitForNonExistence(timeout: 8))
        XCTAssertTrue(local.exists)

        let remoteChild = archivedRow("另一设备的工作区归档")
        XCTAssertTrue(remoteChild.waitForExistence(timeout: 6))
        remoteChild.tap()
        let conversation = app.navigationBars["另一设备的工作区归档"]
        XCTAssertTrue(conversation.waitForExistence(timeout: 12))
        XCTAssertTrue(app.staticTexts["另一设备的归档正文。"].firstMatch.waitForExistence(timeout: 8))
        XCTAssertFalse(app.textFields["消息"].exists)
        conversation.buttons["BackButton"].tap()
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        XCTAssertTrue(local.exists)
        app.buttons["工作区"].tap()
        XCTAssertEqual(app.buttons.matching(identifier: "同名归档工作区").count, 2)
        app.buttons["删除全部"].firstMatch.tap()
        let confirm = app.alerts.firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 8))
        XCTAssertTrue(confirm.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", remoteAddress)).firstMatch.exists)
        capture("Batch confirmation names both archive owners", app: app)
        confirm.buttons["彻底删除"].tap()
        XCTAssertTrue(app.staticTexts["没有归档工作区"].waitForExistence(timeout: 20))
        XCTAssertEqual(app.buttons.matching(identifier: "同名归档工作区").count, 0)
    }

    func testSessionListLongPressShowsHistoryAndArchiveAction() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-inbox")
        let row = app.buttons["测试断线重连"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 8))
        row.press(forDuration: 0.8)
        let preview = app.descendants(matching: .any)["archiveConversationPreview"].firstMatch
        guard preview.waitForExistence(timeout: 5) else { return XCTFail("A session row must present a native conversation preview") }
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "已把重连和执行状态分开处理。")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 8))
        XCTAssertTrue(app.descendants(matching: .any)["置顶会话"].firstMatch.exists)
        let archive = app.descendants(matching: .any)["归档会话"].firstMatch
        XCTAssertTrue(archive.exists)
        capture("Session list system preview and actions", app: app)
        archive.tap()
        XCTAssertTrue(app.navigationBars["归档会话"].firstMatch.waitForExistence(timeout: 8))
        app.buttons["归档会话"].firstMatch.tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 8))
    }

    func testPinnedPreviewReadsOwningWorkerWithoutSwitchingDevices() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_PINNED_DEVICES"] == "1" else {
            throw XCTSkip("Requires two enrolled workers")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let local = app.buttons["当前设备的归档校验"]
        let remote = app.buttons["另一设备的归档校验"]
        XCTAssertTrue(local.waitForExistence(timeout: 15))
        XCTAssertTrue(remote.waitForExistence(timeout: 15))
        local.tap()
        XCTAssertTrue(app.links["当前设备的完整记录"].waitForExistence(timeout: 12))
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        composer.tap()
        app.typeText("Local draft for preview check")
        if !tablet {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(remote.waitForExistence(timeout: 8))
        remote.press(forDuration: 0.8)
        let preview = app.descendants(matching: .any)["archiveConversationPreview"].firstMatch
        XCTAssertTrue(preview.waitForExistence(timeout: 8))
        XCTAssertTrue(app.links["另一设备的完整记录"].waitForExistence(timeout: 12))
        XCTAssertTrue(app.descendants(matching: .any)["取消置顶"].firstMatch.exists)
        XCTAssertTrue(app.descendants(matching: .any)["会话操作"].firstMatch.exists)
        if tablet { XCTAssertTrue(app.descendants(matching: .any)["在右侧查看"].firstMatch.exists) }
        capture("Pinned session preview reads its owning worker", app: app)
        let outsideMenu = tablet ? CGVector(dx: 0.87, dy: 0.36) : CGVector(dx: 0.04, dy: 0.12)
        app.coordinate(withNormalizedOffset: outsideMenu).tap()
        XCTAssertTrue(preview.waitForNonExistence(timeout: 6))
        if !tablet { local.tap() }
        XCTAssertTrue(app.links["当前设备的完整记录"].waitForExistence(timeout: 8))
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, "Local draft for preview check")
        if tablet {
            remote.press(forDuration: 0.8)
            let parallel = app.descendants(matching: .any)["在右侧查看"].firstMatch
            XCTAssertTrue(parallel.waitForExistence(timeout: 8))
            parallel.tap()
            XCTAssertTrue(app.links["另一设备的完整记录"].waitForExistence(timeout: 12))
            XCTAssertTrue(app.links["当前设备的完整记录"].exists)
            XCTAssertEqual(composer.value as? String, "Local draft for preview check")
            capture("Opened the owning worker on the right and kept the local draft", app: app)
        }
    }

    func testTappingArchivePreviewOpensThatConversation() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-archive")
        let row = app.buttons["旧版 SSH 配置迁移"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 8))
        row.press(forDuration: 0.8)
        XCTAssertTrue(app.staticTexts["已保留旧配置的备份。"].firstMatch.waitForExistence(timeout: 8))
        // UIKit exposes the preview content in its source view's offscreen
        // accessibility frame while the presented snapshot is onscreen.
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.34)).tap()
        XCTAssertTrue(app.navigationBars["旧版 SSH 配置迁移"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
    }

    func testArchivePreviewUsesFullViewportWhenHistoryOverflows() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-archive")
        let row = app.buttons["保留 iOS 构建记录"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 8))
        row.press(forDuration: 0.8)
        let latest = app.staticTexts["构建记录 12 已保留。"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 8), app.debugDescription)
        let preview = app.descendants(matching: .any)["archiveConversationPreview"].firstMatch
        XCTAssertGreaterThan(preview.frame.height, app.frame.height * (tablet ? 0.58 : 0.63))
        XCTAssertFalse(app.staticTexts["构建记录 1 已保留。"].exists)
        XCTAssertTrue(app.descendants(matching: .any)["彻底删除"].firstMatch.exists)
    }

    func testArchiveDeletionReachesOwningWorker() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_ARCHIVE_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated archive worker fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let kept = app.buttons["保留当前会话"].firstMatch
        XCTAssertTrue(kept.waitForExistence(timeout: 18), app.debugDescription)
        app.buttons["会话视图"].firstMatch.tap()
        app.descendants(matching: .any)["归档"].firstMatch.tap()
        let archive = app.navigationBars["已归档"]
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        let removed = app.buttons[title]
        XCTAssertTrue(removed.waitForExistence(timeout: 8), app.debugDescription)
        removed.press(forDuration: 0.8)
        app.descendants(matching: .any)["彻底删除"].firstMatch.tap()
        let dialog = app.alerts.firstMatch
        XCTAssertTrue(dialog.waitForExistence(timeout: 6))
        dialog.buttons["彻底删除"].tap()
        XCTAssertTrue(removed.waitForNonExistence(timeout: 14))

        app.buttons["工作区"].tap()
        let workspace = app.buttons["隔离归档构建项目"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        workspace.press(forDuration: 0.8)
        app.descendants(matching: .any)["彻底删除"].firstMatch.tap()
        XCTAssertTrue(dialog.waitForExistence(timeout: 6))
        capture("Worker workspace deletion identifies its contained session", app: app)
        dialog.buttons["彻底删除"].tap()
        XCTAssertTrue(workspace.waitForNonExistence(timeout: 14))
        XCTAssertTrue(app.staticTexts["没有归档工作区"].waitForExistence(timeout: 6))
        capture("Owning Worker accepted archive deletion", app: app)
        archive.buttons.firstMatch.tap()
        XCTAssertTrue(kept.waitForExistence(timeout: 8), app.debugDescription)
    }

    func testBoardSessionActions() {
        let app = launch("--preview-session")
        let more = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 12))
        more.tap()
        let actions = app.buttons["会话操作"].firstMatch
        XCTAssertTrue(actions.waitForExistence(timeout: 6))
        actions.tap()
        XCTAssertTrue(app.navigationBars["会话操作"].waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["保存名称"].exists)
        capture("Board session-actions", app: app)
    }

    func testSessionActionsOnlyOffersSaveAfterEditingTheName() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-actions")
        let navigation = app.navigationBars["会话操作"]
        XCTAssertTrue(navigation.waitForExistence(timeout: 8))
        let name = app.textFields["会话名称"].firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 6))
        let save = app.buttons["保存名称"].firstMatch
        XCTAssertFalse(save.exists, "A clean session name should not offer a save action")
        capture("Session actions with no pending rename", app: app)
        name.tap()
        app.typeText("x")
        XCTAssertTrue(save.waitForExistence(timeout: 5) && save.isEnabled)
        XCTAssertFalse(navigation.buttons["保存名称"].exists)
        capture("Inline save for the edited session name", app: app)
        app.typeText(XCUIKeyboardKey.delete.rawValue)
        XCTAssertTrue(save.waitForNonExistence(timeout: 5))
    }

    func testSessionActionsRestoreTheSourceNameAndOpenTheArchivedOrigin() throws {
        guard let control = ProcessInfo.processInfo.environment["SHIOJI_SESSION_METADATA_FIXTURE"] else {
            throw XCTSkip("Requires an isolated Worker with a persisted fork")
        }
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("")
        let fork = app.buttons["我设置的分叉名"].firstMatch
        XCTAssertTrue(fork.waitForExistence(timeout: 15), app.debugDescription)
        fork.tap()
        let message = app.textFields["消息"].firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(message.value as? String, "保留分叉的未发送草稿")
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["会话操作"].firstMatch.tap()
        let actions = app.navigationBars["会话操作"].firstMatch
        XCTAssertTrue(actions.waitForExistence(timeout: 8))
        let restore = app.buttons["使用来源名称"].firstMatch
        guard restore.waitForExistence(timeout: 5) else {
            capture("The fork actions have no source name action", app: app)
            return XCTFail("Mobile session actions do not offer the Worker-provided name")
        }
        capture("Session actions include the source name and origin", app: app)
        let name = app.textFields["会话名称"].firstMatch
        restore.tap()
        let restored = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "Agent 提供的分叉名"), object: name)
        XCTAssertEqual(XCTWaiter.wait(for: [restored], timeout: 10), .completed, app.debugDescription)
        XCTAssertTrue(restore.waitForNonExistence(timeout: 8))

        name.tap()
        app.typeText("mobile edit")
        let draftTitle = name.value as? String
        XCTAssertTrue(draftTitle?.contains("mobile edit") == true)
        try Data().write(to: URL(fileURLWithPath: control + ".race"))
        let otherRename = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            FileManager.default.fileExists(atPath: control + ".raced")
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [otherRename], timeout: 8), .completed)
        app.buttons["保存名称"].firstMatch.tap()
        let conflict = app.staticTexts["会话已改变，请重新载入后再试"].firstMatch
        XCTAssertTrue(conflict.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(name.value as? String, draftTitle)
        let keyboard = app.keyboards.firstMatch
        let editingTitle = keyboard.exists
        if editingTitle && keyboard.frame.height > 100 {
            XCTAssertLessThan(conflict.frame.maxY, keyboard.frame.minY)
        }
        capture("A simultaneous rename preserves the local title draft", app: app)

        if editingTitle {
            actions.buttons["BackButton"].firstMatch.tap()
            let actionsEntry = app.buttons["会话操作"].firstMatch
            XCTAssertTrue(actionsEntry.waitForExistence(timeout: 8), app.debugDescription)
            actionsEntry.tap()
            XCTAssertTrue(actions.waitForExistence(timeout: 8))
            XCTAssertEqual(name.value as? String, "另一端更新的分叉名")
        }
        let origin = app.buttons["查看来源会话"].firstMatch
        if !origin.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(origin.isHittable, app.debugDescription)
        origin.tap()
        XCTAssertTrue(app.staticTexts["分叉前的归档会话"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
        let sourceText = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "核对分叉前保存的消息")).firstMatch
        XCTAssertTrue(sourceText.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(actions.waitForNonExistence(timeout: 8))
        XCTAssertFalse(message.exists)
        capture("The archived origin opens as a read-only conversation", app: app)
        if !tablet { app.navigationBars.buttons["BackButton"].firstMatch.tap() }
        let renamed = app.buttons["另一端更新的分叉名"].firstMatch
        XCTAssertTrue(renamed.waitForExistence(timeout: 8), app.debugDescription)
        renamed.tap()
        XCTAssertTrue(message.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(message.value as? String, "保留分叉的未发送草稿")
    }

    func testSessionReloadPreservesHistoryDraftAndPausedQueue() {
        let app = launch("--preview-board-session-reload")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 10))
        editor.tap()
        app.typeText("keep my draft")
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["会话操作"].tap()
        XCTAssertTrue(app.navigationBars["会话操作"].waitForExistence(timeout: 6))
        capture("Session actions before runtime reload", app: app)
        let reload = app.buttons["重新加载"]
        guard reload.waitForExistence(timeout: 4) else { return XCTFail("The session actions have no runtime reload entry") }
        XCTAssertTrue(reload.isEnabled)
        reload.tap()
        XCTAssertTrue(app.staticTexts["会话已重新加载"].waitForExistence(timeout: 8))
        capture("The runtime was reloaded", app: app)
        app.navigationBars["会话操作"].buttons.firstMatch.tap()
        app.navigationBars["会话活动"].buttons.firstMatch.tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 6))
        XCTAssertEqual(editor.value as? String, "keep my draft")
        XCTAssertTrue(app.staticTexts["补上连接超时测试"].exists || app.buttons["补上连接超时测试"].exists)
        XCTAssertTrue(app.staticTexts["整理重连测试结果。"].exists)
    }

    func testSessionReloadReopeningChecksTheOriginalReceipt() {
        let app = launch("--preview-board-session-reload-receipt")
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        activity.tap()
        let actions = app.buttons["会话操作"]
        XCTAssertTrue(actions.waitForExistence(timeout: 6))
        actions.tap()
        let reload = app.buttons["重新加载"]
        XCTAssertTrue(reload.waitForExistence(timeout: 6))
        reload.tap()
        XCTAssertTrue(app.staticTexts["重新加载结果待核对"].waitForExistence(timeout: 6))
        XCTAssertFalse(reload.isEnabled)
        XCTAssertTrue(app.buttons["核对重新加载结果"].exists)
        capture("Original runtime reload receipt is unconfirmed", app: app)
        app.navigationBars["会话操作"].buttons.firstMatch.tap()
        XCTAssertTrue(actions.waitForExistence(timeout: 6))
        actions.tap()
        XCTAssertTrue(app.staticTexts["会话已重新加载"].waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["核对重新加载结果"].exists)
    }

    func testSessionReloadFromListAndGates() {
        let list = launch("--preview-board-session-reload-list")
        let target = list.buttons["核对会话重新加载"]
        XCTAssertTrue(target.waitForExistence(timeout: 10))
        target.press(forDuration: 0.7)
        let actions = list.buttons["会话操作"]
        XCTAssertTrue(actions.waitForExistence(timeout: 6))
        actions.tap()
        let reload = list.buttons["重新加载"]
        XCTAssertTrue(reload.waitForExistence(timeout: 6))
        XCTAssertTrue(reload.isEnabled)
        reload.tap()
        XCTAssertTrue(list.staticTexts["会话已重新加载"].waitForExistence(timeout: 8))
        list.navigationBars["会话操作"].buttons.firstMatch.tap()
        XCTAssertTrue(target.waitForExistence(timeout: 6))
        XCTAssertFalse(list.textFields["消息"].exists)
        list.terminate()
        for (route, reason) in [("session-reload-busy", "请先暂停待发队列"), ("session-reload-legacy", "需要更新执行服务")] {
            let app = launch("--preview-board-" + route)
            let more = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(more.waitForExistence(timeout: 10))
            more.tap()
            app.buttons["会话操作"].tap()
            let blocked = app.buttons["重新加载"]
            XCTAssertTrue(blocked.waitForExistence(timeout: 6))
            XCTAssertFalse(blocked.isEnabled)
            XCTAssertEqual(blocked.value as? String, reason)
            capture(reason, app: app)
            app.terminate()
        }
    }

    func testFrozenTabletBoards() {
        captureBoards([
            ("pad-chat", "切换侧栏"), ("pad-diff", "关闭改动"),
            ("pad-parallel", "关闭并排会话"), ("pad-portrait", "会话活动"),
        ], tablet: true)
    }

    func testArtboardPhoneRevisedLayouts() {
        captureBoards([
            ("inbox", "切换会话范围"), ("chat", "测试断线重连"),
            ("create", "选择工作区，Shioji"), ("queue-manager", "检查历史折叠状态"),
            ("reference-picker", "插入引用"), ("input-prefs", "每次加载轮次"),
        ])
    }

    func testArtboardDarkConversationAndQueue() {
        captureBoards([("chat", "测试断线重连"), ("queue-manager", "检查历史折叠状态"), ("inbox", "切换会话范围")])
    }

    func testArtboardDarkTablet() {
        captureBoards([("pad-chat", "切换侧栏")], tablet: true)
    }

    func testFeedbackConversationScreens() {
        captureBoards([("inbox", "切换会话范围"), ("chat", "测试断线重连"), ("create", "选择工作区，Shioji"),
                       ("diff", "internal/transport/connection.go"), ("file", "connection.go"),
                       ("queue-manager", "检查历史折叠状态"), ("approval", "命令审批")])
    }

    func testFeedbackDeviceScreens() {
        captureBoards([("device-list", "MacBook Pro"), ("verify", "信任主机，继续配置 SSH"),
                       ("device-detail", "Mac mini"), ("offline", "重连")])
    }

    func testRemoteSetupReviewBoards() {
        captureBoards([
            ("ssh-auth", "验证 SSH 登录"), ("worker-setup", "安装执行服务"), ("agent-config", "开始登录"),
            ("directory-picker", "选择此目录"), ("tailnet-setup", "在浏览器授权"), ("device-health", "清理缓存"),
            ("hook-review", "信任所选"), ("port-forwards", "建立转发"), ("import-session", "导入所选会话"),
        ])
    }

    func testPortForwardDevicePickerKeepsTheEnteredPorts() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-port-forwards")
        XCTAssertTrue(app.navigationBars["端口转发"].waitForExistence(timeout: 10))
        let device = app.buttons["转发设备"].firstMatch
        XCTAssertTrue(device.waitForExistence(timeout: 5), "Manual forwarding should accept any enrolled device")
        XCTAssertEqual(device.value as? String, "mac-mini.local:22")
        let target = app.textFields["远程端口"].firstMatch
        let local = app.textFields["本机端口"].firstMatch
        XCTAssertEqual(target.value as? String, "4173")
        XCTAssertEqual(local.value as? String, "4173")
        device.tap()
        let other = app.descendants(matching: .any)["macbook-pro.local:22"].firstMatch
        XCTAssertTrue(other.waitForExistence(timeout: 5))
        other.tap()
        XCTAssertEqual(device.value as? String, "macbook-pro.local:22")
        XCTAssertEqual(target.value as? String, "4173")
        XCTAssertEqual(local.value as? String, "4173")
        capture("Forward creation keeps the selected device and explicit ports", app: app)
    }

    func testForwardOpensAndRetriesForTheSelectedWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let remotePort = environment["SHIOJI_FORWARD_TARGET_PORT"],
              let localPort = environment["SHIOJI_FORWARD_LOCAL_PORT"],
              let worker = environment["SHIOJI_FORWARD_DEVICE_ADDRESS"],
              let control = environment["SHIOJI_FORWARD_CONTROL"] else {
            throw XCTSkip("Requires isolated forwarding workers and HTTP service")
        }
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        devices.tap()
        let forwarding = app.buttons["端口转发"].firstMatch
        XCTAssertTrue(forwarding.waitForExistence(timeout: 8))
        forwarding.tap()
        let device = app.buttons["转发设备"]
        XCTAssertTrue(device.waitForExistence(timeout: 8))
        device.tap()
        let second = app.descendants(matching: .any)[worker].firstMatch
        XCTAssertTrue(second.waitForExistence(timeout: 6))
        second.tap()
        XCTAssertEqual(device.value as? String, worker)
        for (label, value) in [("远程端口", remotePort), ("本机端口", localPort)] {
            let input = app.textFields[label].firstMatch
            XCTAssertTrue(input.exists)
            input.tap()
            app.typeText(value)
        }
        let create = app.buttons["建立转发"].firstMatch
        XCTAssertTrue(create.isHittable)
        create.tap()

        let address = "127.0.0.1:\(localPort)"
        let open = app.buttons["打开转发：" + address].firstMatch
        XCTAssertTrue(open.waitForExistence(timeout: 15), app.debugDescription)
        capture("The global port list shows the selected remote device", app: app)
        let done = app.buttons.matching(NSPredicate(format: "label IN %@", ["Done", "完成"])).firstMatch
        let page = app.staticTexts["已通过当前转发打开"].firstMatch
        open.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 14), app.debugDescription)
        done.coordinate(withNormalizedOffset: CGVector(dx: 0.20, dy: 0.5)).tap()
        XCTAssertTrue(open.waitForExistence(timeout: 8))

        func changeService(_ action: String) throws {
            try Data().write(to: URL(fileURLWithPath: control + "." + action))
            let ready = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                FileManager.default.fileExists(atPath: control + "." + action + ".ready")
            }, object: nil)
            XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 8), .completed)
        }
        try changeService("down")
        open.tap()
        XCTAssertTrue(done.waitForExistence(timeout: 6))
        XCTAssertTrue(page.waitForNonExistence(timeout: 10))
        done.coordinate(withNormalizedOffset: CGVector(dx: 0.20, dy: 0.5)).tap()
        let retry = app.buttons["重试"].firstMatch
        XCTAssertTrue(retry.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(open.exists)
        capture("An unavailable mapping offers retry instead of open", app: app)

        try changeService("up")
        retry.tap()
        XCTAssertTrue(open.waitForExistence(timeout: 12))
        XCTAssertFalse(retry.exists)
        let stop = app.buttons["关闭转发：" + address].firstMatch
        XCTAssertTrue(stop.exists)
        stop.tap()
        XCTAssertTrue(stop.waitForNonExistence(timeout: 8))
    }

    func testAsyncQuestionAnswersKeepTheConversationDraft() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let answer = environment["SHIOJI_ASYNC_QUESTION_ANSWER"] else {
            throw XCTSkip("Requires an isolated Worker with a transcript question")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let session = app.buttons["异步问题验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 12), app.debugDescription)
        session.tap()

        let question = app.staticTexts["这次如何更新？"].firstMatch
        XCTAssertTrue(question.waitForExistence(timeout: 12), app.debugDescription)
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 6))
        XCTAssertEqual(draft.value as? String, "保留会话草稿")

        if answer == "只更新索引" {
            app.buttons[answer].firstMatch.tap()
        } else {
            app.buttons["自定义答案"].firstMatch.tap()
            let input = app.textFields["自定义答案内容"].firstMatch
            XCTAssertTrue(input.waitForExistence(timeout: 5))
            input.tap()
            app.typeText(answer)
        }
        let send = app.buttons["提交答案"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5))
        if !send.isHittable { app.swipeUp() }
        capture("Nonblocking question in the conversation", app: app)
        send.tap()
        XCTAssertTrue(app.otherElements["已回答"].firstMatch.waitForExistence(timeout: 16), app.debugDescription)
        XCTAssertEqual(draft.value as? String, "保留会话草稿")
        capture("Accepted answer leaves the composer draft intact", app: app)
    }

    func testDeviceHealthShowsLoadHistory() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-device-health")
        XCTAssertTrue(app.navigationBars["设备诊断"].waitForExistence(timeout: 8))
        let charts = app.descendants(matching: .any)
        guard charts["CPU 负载折线图"].firstMatch.waitForExistence(timeout: 5) else {
            return XCTFail("Device diagnostics must show the CPU sampling history")
        }
        XCTAssertTrue(charts["内存 负载折线图"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["18%"].exists)
        XCTAssertTrue(app.staticTexts["7.8 GB"].exists)
        XCTAssertTrue(charts["保持唤醒"].firstMatch.exists)
        capture("Device load history and keep-awake", app: app)
    }

    func testDeviceStorageShowsUsageAndKeepsCleanupResultInPlace() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-device-health")
        XCTAssertTrue(app.navigationBars["设备诊断"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["会话与附件"].waitForExistence(timeout: 5),
                      "Retained originals must have a separate capacity")
        XCTAssertTrue(app.staticTexts["过期未完成上传"].exists)
        XCTAssertTrue(app.staticTexts["历史日志"].exists)
        let reclaimable = app.staticTexts["可回收"].firstMatch
        let cleanup = app.buttons["清理缓存"].firstMatch
        if !cleanup.isHittable { app.swipeUp() }
        XCTAssertTrue(cleanup.isHittable)
        let before = reclaimable.frame
        capture("Device storage before cleanup", app: app)
        cleanup.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        alert.buttons["取消"].tap()
        XCTAssertTrue(cleanup.isEnabled)
        cleanup.tap()
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        alert.buttons["清理缓存"].tap()
        let result = app.otherElements.containing(NSPredicate(format: "label BEGINSWITH %@", "已释放 ")).firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 8))
        XCTAssertEqual(reclaimable.frame.midY, before.midY, accuracy: 1)
        XCTAssertTrue(app.staticTexts["会话与附件"].exists)
        XCTAssertTrue(app.staticTexts["本机显示缓存"].exists)
        capture("Device storage after cleanup", app: app)
    }

    func testRemoteAndLocalCleanupActOnDifferentStorage() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_STORAGE_FIXTURE"] == "1",
                          "Requires isolated storage fixture")
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        devices.tap()
        let device = app.buttons["127.0.0.1"].firstMatch
        XCTAssertTrue(device.waitForExistence(timeout: 8))
        device.tap()
        let diagnostics = app.buttons["设备诊断"].firstMatch
        XCTAssertTrue(diagnostics.waitForExistence(timeout: 8))
        diagnostics.tap()
        let keepAwake = app.descendants(matching: .any)["保持唤醒"].firstMatch
        let freshHealth = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            keepAwake.exists && keepAwake.isEnabled &&
                app.descendants(matching: .any)["CPU 负载折线图"].firstMatch.exists &&
                app.descendants(matching: .any)["内存 负载折线图"].firstMatch.exists
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [freshHealth], timeout: 10), .completed, app.debugDescription)
        let remote = app.buttons["清理缓存"].firstMatch
        XCTAssertTrue(remote.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.staticTexts["12.0 KB"].firstMatch.exists)
        if !remote.isHittable { app.swipeUp() }
        remote.tap()
        let confirmation = app.alerts.firstMatch
        XCTAssertTrue(confirmation.waitForExistence(timeout: 5))
        confirmation.buttons["清理缓存"].tap()
        let remoteResult = app.otherElements["已释放 12.0 KB"].firstMatch
        XCTAssertTrue(remoteResult.waitForExistence(timeout: 8), app.debugDescription)
        capture("Owning device cleanup receipt", app: app)
        let local = app.buttons["清理本机显示缓存"].firstMatch
        if !local.isHittable { app.swipeUp() }
        XCTAssertTrue(local.waitForExistence(timeout: 6) && local.isHittable)
        XCTAssertTrue(app.staticTexts["16.0 KB"].firstMatch.exists)
        local.tap()
        XCTAssertTrue(app.otherElements["已释放 16.0 KB"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        capture("On-device cache cleanup receipt", app: app)
    }

    func testRecoveredConnectionRemovesTransportErrorFromConversation() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let endpoint = environment["SHIOJI_RECONNECT_FIXTURE"], let control = URL(string: endpoint) else {
            throw XCTSkip("Requires an isolated timed-out SSH handshake")
        }
        continueAfterFailure = false
        let baseline = environment["SHIOJI_RECONNECT_BASELINE"] == "1"
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let conversation = app.buttons["连接恢复验收"].firstMatch
        XCTAssertTrue(conversation.waitForExistence(timeout: 16), app.debugDescription)
        conversation.tap()
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, "保留这条还没发的草稿")
        let reconnect = app.buttons["重连"].firstMatch

        func awaitFixture(_ step: String) {
            let answer = expectation(description: step)
            var request = URLRequest(url: control.appendingPathComponent(step))
            request.httpMethod = "POST"
            URLSession.shared.dataTask(with: request) { _, response, _ in
                XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
                answer.fulfill()
            }.resume()
            wait(for: [answer], timeout: 25)
        }

        let transportFailure = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "ssh: handshake failed")).firstMatch
        awaitFixture("timed-out")
        XCTAssertTrue(reconnect.waitForExistence(timeout: 10), app.debugDescription)
        if baseline {
            XCTAssertTrue(transportFailure.waitForExistence(timeout: 8), app.debugDescription)
        } else {
            XCTAssertFalse(transportFailure.exists)
            app.buttons["打开当前设备详情"].firstMatch.tap()
            XCTAssertTrue(transportFailure.waitForExistence(timeout: 8), app.debugDescription)
            capture("The offline SSH error is visible in device details", app: app)
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
            XCTAssertTrue(reconnect.waitForExistence(timeout: 6))
        }

        awaitFixture("recover")
        XCTAssertTrue(app.staticTexts["SHIOJI_FAST_DONE"].firstMatch.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(reconnect.waitForNonExistence(timeout: 6))
        XCTAssertEqual(composer.value as? String, "保留这条还没发的草稿")
        if baseline {
            XCTAssertTrue(transportFailure.exists)
            capture("The old handshake failure remains after a successful connection", app: app)
        } else {
            XCTAssertFalse(transportFailure.exists)
            capture("Reconnected conversation keeps its draft without old transport feedback", app: app)
            app.buttons["打开当前设备详情"].firstMatch.tap()
            XCTAssertTrue(app.staticTexts["已连接"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertFalse(transportFailure.exists)
            capture("Connected device no longer shows the old timeout", app: app)
        }
    }

    func testOfflineKeepsConversationAndComposerVisible() {
        let app = launch("--preview-board-offline")
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "消息").firstMatch.exists)
        XCTAssertTrue(app.buttons["重连"].exists)
        XCTAssertFalse(app.navigationBars["连接中断"].exists)
    }

    func testConnectionStatusAndOperationHistoryAreSeparate() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-offline")
        let unavailable = app.otherElements["连接不可用"].firstMatch
        XCTAssertTrue(unavailable.waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["重连"].firstMatch.isHittable)
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue((draft.value as? String)?.contains("恢复连接") == true)
        XCTAssertTrue(app.staticTexts["执行状态待核对"].firstMatch.exists)
        XCTAssertFalse(app.staticTexts["Git 操作未完成"].exists)
        capture("Connection unavailable with the draft retained", app: app)

        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["最近操作"].firstMatch.tap()
        let history = app.navigationBars["最近操作"].firstMatch
        XCTAssertTrue(history.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["Git 操作未完成"].firstMatch.exists)
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "fixture-secret")).firstMatch.exists)
        XCTAssertFalse(app.staticTexts["另一会话的回执"].exists)
        capture("The session's operation feedback", app: app)
    }

    func testRecentOperationsOpensTheOriginalSessionAndClearsOnlySettledResults() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-recent-operations")
        let view = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(view.waitForExistence(timeout: 8))
        view.tap()
        capture("Session view menu before recent operations", app: app)
        let entry = app.descendants(matching: .any)["最近操作"].firstMatch
        guard entry.waitForExistence(timeout: 4) else { return XCTFail("The session list must offer recent operations") }
        entry.tap()
        let log = app.navigationBars["最近操作"].firstMatch
        XCTAssertTrue(log.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["备用 Mac · 工具仓库 · 编译检查"].firstMatch.exists)
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "fixture-secret")).firstMatch.exists)
        let source = app.buttons["查看会话：检查会话列表刷新"].firstMatch
        XCTAssertTrue(source.exists)
        capture("Recent operations across devices", app: app)
        source.tap()
        let session = app.navigationBars["检查会话列表刷新"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 8))
        session.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(log.waitForExistence(timeout: 8))
        app.buttons["清空已完成记录"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["提交结果待核对"].firstMatch.exists)
        XCTAssertFalse(app.staticTexts["备用 Mac 已核对"].firstMatch.exists)
        XCTAssertTrue(app.buttons["清空已完成记录"].firstMatch.exists == false)
        capture("Recent operations after clearing settled results", app: app)
    }

    func testRecentOperationsReconnectsToTheOwningWorker() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RECENT_WORKERS"] == "1",
                          "Requires two isolated SSH Workers")
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let home = app.buttons["会话视图"].firstMatch
        let history = app.navigationBars["最近操作"].firstMatch
        func openHistory() {
            XCTAssertTrue(home.waitForExistence(timeout: 8))
            home.tap()
            let item = app.descendants(matching: .any)["最近操作"].firstMatch
            XCTAssertTrue(item.waitForExistence(timeout: 6))
            item.tap()
            XCTAssertTrue(history.waitForExistence(timeout: 8))
        }
        func submitDraft() {
            let send = app.buttons["发送选项"].firstMatch
            XCTAssertTrue(send.waitForExistence(timeout: 12))
            send.tap()
            let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
            XCTAssertTrue(steer.waitForExistence(timeout: 6))
            steer.tap()
            let status = app.descendants(matching: .any)["消息未发送，草稿已保留"].firstMatch
            XCTAssertTrue(status.waitForExistence(timeout: 12), app.debugDescription)
        }
        for suffix in ["甲", "乙"] {
            let row = app.buttons["操作回执 · " + suffix].firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 15), app.debugDescription)
            row.tap()
            XCTAssertTrue(app.staticTexts["源 Worker 的历史 · " + suffix].firstMatch.waitForExistence(timeout: 15))
            submitDraft()
            if !tablet { app.navigationBars.buttons["BackButton"].firstMatch.tap() }
        }
        openHistory()
        capture("Feedback from both isolated SSH devices", app: app)
        for suffix in ["甲", "乙"] {
            let rejection = app.descendants(matching: .any)["验收 Worker 拒绝本次介入 · " + suffix].firstMatch
            let workerReceivedDraft = rejection.exists
            if !workerReceivedDraft {
                let transportFailure = app.descendants(matching: .any).matching(
                    NSPredicate(format: "label CONTAINS %@", "未建立命令连接，操作未发送")
                ).firstMatch
                XCTAssertTrue(transportFailure.exists, app.debugDescription)
            }
            let source = app.buttons["查看会话：操作回执 · " + suffix].firstMatch
            XCTAssertTrue(source.exists)
            source.tap()
            XCTAssertTrue(app.staticTexts["源 Worker 的历史 · " + suffix].firstMatch.waitForExistence(timeout: 20))
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "保留原草稿 · " + suffix)
            capture("Opened the original SSH Worker session " + suffix, app: app)
            if !workerReceivedDraft { submitDraft() }
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
            XCTAssertTrue(history.waitForExistence(timeout: 8))
            XCTAssertTrue(rejection.waitForExistence(timeout: 12), app.debugDescription)
        }
    }

    func testConnectionPhasesOfferScopedRecovery() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let cases = [("connection-syncing", "正在同步会话", false),
                     ("connection-switching", "正在切换线路", false),
                     ("connection-sync-failed", "会话同步受阻", true)]
        for (scenario, title, details) in cases {
            let app = launch("--preview-board-" + scenario)
            XCTAssertTrue(app.otherElements[title].firstMatch.waitForExistence(timeout: 8))
            XCTAssertTrue(app.textFields["消息"].firstMatch.exists)
            XCTAssertFalse(app.buttons["重连"].exists)
            XCTAssertEqual(app.buttons["设备详情"].firstMatch.exists, details)
            capture(title, app: app)
            app.terminate()
        }
    }

    func testAgentAndArchiveSegmentsMeetMinimumTouchHeight() {
        for (board, title, label) in [("agent-config", "Codex", "模型"), ("session-archive", "已归档", "工作区")] {
            XCTContext.runActivity(named: board) { _ in
                let app = launch("--preview-board-" + board)
                XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 8))
                let segment = app.descendants(matching: .any).matching(identifier: label).firstMatch
                XCTAssertTrue(segment.waitForExistence(timeout: 8))
                XCTAssertGreaterThanOrEqual(segment.frame.height, 44)
                XCTAssertTrue(hasVisibleInk(in: segment.frame.insetBy(dx: 6, dy: 9)), label)
                capture("Visible segments " + board, app: app)
                app.terminate()
            }
        }
    }

    func testRemoteAgentSelectionPersistsOnItsOwningWorker() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_AGENT_SELECTION_FIXTURE"] == "1" else {
            throw XCTSkip("Requires an isolated Worker with two configurable Agents")
        }
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("")
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 12), app.debugDescription)
        devices.tap()
        let device = app.buttons["127.0.0.1"].firstMatch
        XCTAssertTrue(device.waitForExistence(timeout: 8), app.debugDescription)
        device.tap()
        let remote = app.buttons["远程 Agent"].firstMatch
        XCTAssertTrue(remote.waitForExistence(timeout: 8))
        remote.tap()
        let first = app.buttons["Memory alpha"].firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 8))
        if tablet {
            XCTAssertFalse(devices.isHittable, "A pushed device page should show its own title and back control")
        }
        let alpha = app.buttons["启用 Memory alpha"].firstMatch
        let beta = app.buttons["启用 Memory beta"].firstMatch
        capture("Agent catalog before changing selection", app: app)
        XCTAssertTrue(alpha.waitForExistence(timeout: 5), "Agent selection is missing from the mobile device page")
        func waitForSwitch(_ toggle: XCUIElement, _ on: Bool) {
            let predicate = NSPredicate { _, _ in toggle.exists && toggle.isEnabled && toggle.isSelected == on }
            let expectation = XCTNSPredicateExpectation(predicate: predicate, object: nil)
            XCTAssertEqual(XCTWaiter.wait(for: [expectation], timeout: 10), .completed, app.debugDescription)
        }
        waitForSwitch(alpha, true)
        waitForSwitch(beta, true)
        alpha.tap()
        waitForSwitch(alpha, false)
        XCTAssertTrue(beta.isSelected)
        beta.tap()
        waitForSwitch(beta, false)
        capture("The remote device accepts an empty Agent selection", app: app)
        first.tap()
        app.buttons["高级"].firstMatch.tap()
        let path = app.textFields["覆盖路径"].firstMatch
        XCTAssertTrue(path.waitForExistence(timeout: 8))
        XCTAssertEqual(path.value as? String, "/usr/bin/true")
        path.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.5)).tap()
        app.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 4) + "false")
        XCTAssertEqual(path.value as? String, "/usr/bin/false")
        app.buttons["保存并继续"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["/usr/bin/false"].firstMatch.waitForExistence(timeout: 10))
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        waitForSwitch(alpha, false)
        alpha.tap()
        waitForSwitch(alpha, true)
        XCTAssertFalse(beta.isSelected)
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        remote.tap()
        waitForSwitch(alpha, true)
        waitForSwitch(beta, false)
        capture("Reopened Agent settings show the Worker selection", app: app)
    }

    func testNewConversationSwitchesRemoteAgentWithoutLosingItsDraft() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE"] == "1")
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 12), app.debugDescription)
        compose.tap()
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8), app.debugDescription)
        editor.tap()
        let message: String
        if ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_CHINESE_KEYBOARD"] == "1" {
            let keyboard = app.keyboards.firstMatch
            XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
            keyboard.keys["n"].tap()
            keyboard.keys["i"].tap()
            let candidate = app.staticTexts["你"].firstMatch
            XCTAssertTrue(candidate.waitForExistence(timeout: 5), app.debugDescription)
            candidate.tap()
            message = "你"
        } else {
            message = "Use the chosen Agent"
            app.typeText(message)
        }
        XCTAssertEqual(editor.value as? String, message)
        let run = app.buttons["运行配置"].firstMatch
        func choose(_ label: String, screenshot: String? = nil) {
            let option = app.descendants(matching: .any)[label].firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 6), app.debugDescription)
            XCTAssertGreaterThanOrEqual(option.frame.minX, app.frame.minX)
            XCTAssertLessThanOrEqual(option.frame.maxX, app.frame.maxX)
            if let screenshot {
                capture(screenshot, app: app)
            }
            option.tap()
        }
        func selectAgent(_ name: String, screenshot: String? = nil) {
            run.tap()
            choose(name, screenshot: screenshot)
        }
        selectAgent("Memory beta", screenshot: "Agent choices above the keyboard")
        XCTAssertEqual(editor.value as? String, message)
        run.tap()
        choose("beta-alternate")
        selectAgent("Memory alpha")
        XCTAssertEqual(run.value as? String, "alpha-model")
        selectAgent("Memory beta")
        XCTAssertEqual(run.value as? String, "beta-alternate")
        XCTAssertEqual(editor.value as? String, message)
        capture("New draft retains text and the selected Agent's model", app: app)
        app.buttons["发送"].firstMatch.tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@",
                                                          "from beta: \(message)")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12), app.debugDescription)
    }

    private func openParallelRunConfigFixture() throws -> XCUIApplication {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE"] == "1")
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = launch("")
        let left = app.buttons["左栏设备会话"].firstMatch
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12), app.debugDescription)
        // The cached row can precede the first connection and the detail pane.
        for _ in 0..<3 where !left.isSelected || !activity.isHittable {
            left.tap()
            let selected = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                left.isSelected && activity.isHittable
            }, object: nil)
            if XCTWaiter.wait(for: [selected], timeout: 3) == .completed { break }
        }
        XCTAssertTrue(left.isSelected && activity.isHittable, app.debugDescription)
        activity.tap()
        let parallel = app.buttons["并排查看会话"].firstMatch
        XCTAssertTrue(parallel.waitForExistence(timeout: 8), app.debugDescription)
        parallel.tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8), app.debugDescription)
        right.tap()
        XCTAssertTrue(app.textFields["右侧会话草稿"].firstMatch.waitForExistence(timeout: 8))
        return app
    }

    func testIPadParallelRunConfigurationTargetsItsOwnSession() throws {
        let app = try openParallelRunConfigFixture()
        func open(_ label: String) {
            let button = app.buttons[label].firstMatch
            XCTAssertTrue(button.waitForExistence(timeout: 12), app.debugDescription)
            button.tap()
        }
        func choose(_ label: String) {
            let option = app.descendants(matching: .any)[label].firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 6), app.debugDescription)
            option.tap()
        }
        let editor = app.textFields["右侧会话草稿"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "Second")
        let run = app.buttons["右侧运行配置"].firstMatch
        XCTAssertTrue(run.waitForExistence(timeout: 8), app.debugDescription)
        run.tap()
        let model = ProcessInfo.processInfo.environment["SHIOJI_RIGHT_FIXTURE_MODEL"] ?? "right-alternate"
        choose(model)
        run.tap()
        choose("high")
        run.tap()
        choose("Fast")
        run.tap()
        choose("Plan")
        open("右侧权限")
        choose("右栏拓展权限")
        XCTAssertTrue((run.value as? String)?.hasPrefix(model) == true)
        let left = app.textFields["消息"].firstMatch
        XCTAssertEqual(left.value as? String, "First Worker draft")
        capture("The right pane has its own runtime configuration", app: app)
        open("发送右侧消息")
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "from alpha: Second")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertGreaterThan(reply.frame.midX, app.frame.midX)
        XCTAssertEqual(left.value as? String, "First Worker draft")
    }


    func testPastingAScreenshotKeepsTheCurrentDraftAndSendsAnImage() throws {
        let mode = ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"]
        try XCTSkipUnless(mode == "paste-home" || mode == "paste-ipad")
        let parallel = mode == "paste-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12))
            compose.tap()
        }
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        let clipboard = XCUIApplication(bundleIdentifier: "ai.shioji.mobile.clipboard-fixture")
        func copyFromFixture(_ label: String) {
            clipboard.launch()
            let copy = clipboard.buttons[label]
            XCTAssertTrue(copy.waitForExistence(timeout: 5))
            copy.tap()
            app.activate()
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 8))
        }
        func paste() {
            let add = app.buttons[parallel ? "添加右侧附件" : "添加附件"].firstMatch
            let pasteItem = app.descendants(matching: .any)["composerClipboardPaste"].firstMatch
            for _ in 0..<3 where !pasteItem.exists {
                if add.isHittable {
                    add.tap()
                }
                if pasteItem.waitForExistence(timeout: 2) { break }
            }
            XCTAssertTrue(pasteItem.exists && pasteItem.isEnabled, app.debugDescription)
            capture("System paste control in the attachment popover", app: app)
            pasteItem.tap()
        }
        copyFromFixture("Copy text")
        paste()
        let expected = parallel ? "Secondclipboard text" : "clipboard text"
        let pastedText = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", expected), object: editor)
        XCTAssertEqual(XCTWaiter.wait(for: [pastedText], timeout: 5), .completed, app.debugDescription)
        copyFromFixture("Copy screenshot")
        paste()
        let attachment = app.buttons["移除 粘贴图片.png"].firstMatch
        XCTAssertTrue(attachment.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertEqual(editor.value as? String, expected)
        if parallel {
            let left = app.textFields["消息"].firstMatch
            XCTAssertEqual(left.value as? String, "First Worker draft")
            XCTAssertGreaterThan(attachment.frame.midX, app.frame.midX)
        }
        capture("Pasted screenshot belongs to its composer", app: app)
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        XCTAssertTrue(send.isEnabled)
        send.tap()
        let sent = app.buttons["预览 粘贴图片.png"].firstMatch
        XCTAssertTrue(sent.waitForExistence(timeout: 16), app.debugDescription)
        capture("Pasted screenshot appears in the sent message", app: app)
    }

    func testLocalDocumentBecomesRemoteReferenceOnlyAfterConfirmation() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "path-document")
        continueAfterFailure = false
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 12))
        compose.tap()
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "Review this document")
        let add = app.buttons["添加附件"].firstMatch
        add.tap()
        let filePicker = app.buttons["从文件选择附件"].firstMatch
        XCTAssertTrue(filePicker.waitForExistence(timeout: 6), app.debugDescription)
        filePicker.tap()
        let location = app.cells.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "iPhone", "iPad")).firstMatch
        if !location.exists {
            let browse = app.buttons["浏览"].firstMatch
            if browse.waitForExistence(timeout: 8) && browse.isHittable { browse.tap() }
        }
        if location.waitForExistence(timeout: 5) && location.isHittable {
            location.tap()
        } else {
            let local = app.buttons.matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "iPhone", "iPad")).firstMatch
            if local.waitForExistence(timeout: 5) && local.isHittable { local.tap() }
        }
        let document = app.cells.matching(NSPredicate(format: "label BEGINSWITH %@", "Shioji review")).firstMatch
        XCTAssertTrue(document.waitForExistence(timeout: 8), app.debugDescription)
        capture("PDF visible in native Files picker", app: app)
        if document.isHittable {
            document.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.24)).tap()
        }
        let open = app.buttons.matching(NSPredicate(format: "label IN %@", ["打开", "Open", "添加", "Add"])).firstMatch
        if open.exists && open.isEnabled && open.isHittable { open.tap() }
        let confirm = app.buttons["上传并引用"].firstMatch
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 7), app.debugDescription)
        XCTAssertFalse(send.isEnabled)
        capture("Document awaiting consent before any upload", app: app)
        confirm.tap()
        let prepared = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == true"), object: send)
        XCTAssertEqual(XCTWaiter.wait(for: [prepared], timeout: 16), .completed, app.debugDescription)
        XCTAssertTrue((editor.value as? String)?.contains("Review this document") == true)
        capture("PDF uploaded as an inline reference", app: app)
        send.tap()
        let sent = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Shioji review.pdf")).firstMatch
        XCTAssertTrue(sent.waitForExistence(timeout: 15), app.debugDescription)
        capture("PDF reference in the selected workspace conversation", app: app)
        sent.tap()
        let preview = app.buttons["原生预览"].firstMatch
        XCTAssertTrue(preview.waitForExistence(timeout: 8), app.debugDescription)
        preview.tap()
        let close = app.buttons["关闭预览"].firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 8), app.debugDescription)
        let bar = app.navigationBars["attachmentPreviewNavigation"].firstMatch
        XCTAssertTrue(bar.waitForExistence(timeout: 6))
        XCTAssertEqual(bar.frame.width, app.frame.width, accuracy: 1)
        let screen = app.frame
        let page = CGRect(x: screen.width * 0.28, y: screen.height * 0.21,
                          width: screen.width * 0.44, height: screen.height * 0.61)
        let visiblePage = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in self.hasVisibleInk(in: page) }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [visiblePage], timeout: 12), .completed, "PDF preview stayed blank")
        capture("The sent PDF opens in Quick Look", app: app)
        close.tap()
        XCTAssertTrue(preview.waitForExistence(timeout: 8))
    }

    func testInterruptedDocumentUploadCanRetryWithoutLosingItsDraft() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "path-document-retry")
        continueAfterFailure = false
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 12))
        compose.tap()
        let editor = app.textFields["消息"].firstMatch
        let retry = app.buttons["重试上传 Shioji review.pdf"].firstMatch
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(retry.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(editor.value as? String, "Review this document")
        XCTAssertFalse(send.isEnabled)
        XCTAssertFalse(app.buttons["上传并引用"].exists)
        capture("Interrupted PDF upload can be retried", app: app)
        retry.tap()
        let prepared = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == true"), object: send)
        XCTAssertEqual(XCTWaiter.wait(for: [prepared], timeout: 16), .completed, app.debugDescription)
        XCTAssertTrue((editor.value as? String)?.contains("Review this document") == true)
        send.tap()
        let sent = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Shioji review.pdf")).firstMatch
        XCTAssertTrue(sent.waitForExistence(timeout: 15), app.debugDescription)
    }

    func testNewConversationWorkspaceSwitchCarriesLocalAttachmentButKeepsSourceReferenceScoped() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "attachment-workspace")
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 12))
        compose.tap()
        let editor = app.textFields["消息"].firstMatch
        let portable = app.buttons["预览草稿附件 随稿笔记.txt"].firstMatch
        let foreign = app.buttons["预览草稿附件 源工作区.txt"].firstMatch
        let source = app.buttons["选择工作区，test"].firstMatch
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 8))
        XCTAssertTrue(portable.waitForExistence(timeout: 8))
        XCTAssertEqual(portable.value as? String, "已上传")
        XCTAssertTrue((editor.value as? String)?.contains("Keep the draft") == true)
        XCTAssertFalse(foreign.exists)
        capture("Workspace source with local attachment and owned reference", app: app)
        source.tap()
        let choice = app.descendants(matching: .any)["目标工作区"].firstMatch
        XCTAssertTrue(choice.waitForExistence(timeout: 6), app.debugDescription)
        choice.tap()
        let destination = app.buttons["选择工作区，目标工作区"].firstMatch
        XCTAssertTrue(destination.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertLessThanOrEqual(portable.frame.minY - destination.frame.maxY, 4)
        capture("Workspace switched with both draft resources", app: app)
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "已上传"), object: portable)
        XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 10), .completed, app.debugDescription)
        XCTAssertTrue(foreign.exists)
        XCTAssertEqual(foreign.value as? String, "属于其他工作区")
        XCTAssertFalse(send.isEnabled)
        XCTAssertTrue((editor.value as? String)?.contains("Keep the draft") == true)
        capture("Only the portable attachment is ready for the destination", app: app)
        app.buttons["移除 源工作区.txt"].firstMatch.tap()
        XCTAssertTrue(foreign.waitForNonExistence(timeout: 6))
        XCTAssertEqual(editor.value as? String, "Keep the draft")
        XCTAssertTrue(send.isEnabled)
        send.tap()
        XCTAssertTrue(app.buttons["随稿笔记.txt"].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        capture("Original local attachment sent to the selected workspace", app: app)
    }

    func testDraftAttachmentCanBePreviewedAndRetriedWithoutReplacingTheDraft() throws {
        let mode = ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"]
        try XCTSkipUnless(mode == "attachment-recovery-phone" || mode == "attachment-recovery-ipad")
        let parallel = mode == "attachment-recovery-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12))
            compose.tap()
        }
        let filename = "恢复样例.png"
        let draft = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let retry = app.buttons["重试上传 " + filename].firstMatch
        let preview = app.buttons["预览草稿附件 " + filename].firstMatch
        let remove = app.buttons["移除 " + filename].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 8))
        capture("Draft attachment before retry", app: app)
        XCTAssertTrue(retry.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(preview.exists && remove.exists)
        XCTAssertFalse(send.isEnabled)
        XCTAssertEqual(draft.value as? String, "Keep this draft")
        XCTAssertEqual(preview.value as? String, parallel ? "等待上传" : "上传失败")
        preview.tap()
        let done = app.buttons["关闭预览"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 8), app.debugDescription)
        let previewBar = app.navigationBars["attachmentPreviewNavigation"].firstMatch
        XCTAssertTrue(previewBar.exists)
        XCTAssertEqual(previewBar.frame.width, app.frame.width, accuracy: 1)
        let imageVisible = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            let screen = XCUIScreen.main.screenshot().image
            let format = UIGraphicsImageRendererFormat()
            format.scale = 1
            return [0.0, 45, 90, -45, -90].contains { offset in
                let sample = UIGraphicsImageRenderer(size: CGSize(width: 1, height: 1), format: format).image { _ in
                    screen.draw(at: CGPoint(x: -screen.size.width / 2, y: -screen.size.height / 2 - offset))
                }
                guard let image = sample.cgImage else { return false }
                var color = [UInt8](repeating: 0, count: 4)
                return color.withUnsafeMutableBytes { bytes in
                    guard let context = CGContext(data: bytes.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                                                  space: CGColorSpaceCreateDeviceRGB(),
                                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
                    context.draw(image, in: CGRect(x: 0, y: 0, width: 1, height: 1))
                    return bytes[0] < 60 && (65...150).contains(bytes[1]) && bytes[2] > 175
                }
            }
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [imageVisible], timeout: 8), .completed, app.debugDescription)
        let draftImage = app.images[filename].firstMatch
        XCTAssertTrue(draftImage.exists)
        XCTAssertTrue(previewBar.buttons["分享原图"].exists)
        previewBar.buttons["逆时针旋转 90°"].tap()
        let rotated = NSPredicate { _, _ in (draftImage.value as? String)?.contains("90°") == true }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: rotated, object: draftImage)], timeout: 5), .completed)
        capture("Rotated local draft image in the attachment preview", app: app)
        done.tap()
        XCTAssertTrue(retry.waitForExistence(timeout: 8))
        let canRetry = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == true"), object: retry)
        XCTAssertEqual(XCTWaiter.wait(for: [canRetry], timeout: 8), .completed)
        retry.tap()
        let uploaded = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "已上传"), object: preview)
        XCTAssertEqual(XCTWaiter.wait(for: [uploaded], timeout: 15), .completed, app.debugDescription)
        XCTAssertFalse(retry.exists)
        XCTAssertTrue(preview.exists && remove.exists)
        XCTAssertEqual(draft.value as? String, "Keep this draft")
        if parallel {
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        }
        capture("Recovered attachment in the originating draft", app: app)
        XCTAssertTrue(send.isEnabled)
        send.tap()
        XCTAssertTrue(app.buttons["预览 " + filename].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        capture("Recovered attachment is sent", app: app)
    }

    func testIPadParallelStopTargetsTheRightTurn() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "stop")
        let app = try openParallelRunConfigFixture()
        let stop = app.buttons["停止右侧回复"].firstMatch
        let available = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            stop.isHittable && stop.isEnabled
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [available], timeout: 10), .completed, app.debugDescription)
        capture("Right session has its own stop control", app: app)
        stop.tap()
        XCTAssertTrue(stop.waitForNonExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
    }

    func testPhoneStopsAReplyBeforeItsTurnStarts() throws {
        let environment = ProcessInfo.processInfo.environment
        try XCTSkipUnless(environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "starting-stop")
        XCUIDevice.shared.orientation = .portrait
        let app = launch("")
        let session = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 12), app.debugDescription)
        session.tap()
        XCTAssertTrue(app.buttons["会话活动"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 5))
        editor.tap()
        app.typeText("Cancel initialization")
        app.buttons["发送"].firstMatch.tap()
        let stop = app.buttons["停止"].firstMatch
        let available = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            stop.exists && stop.isHittable && stop.isEnabled
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [available], timeout: 10), .completed, app.debugDescription)
        let queued = app.staticTexts["After initialization"].firstMatch
        XCTAssertTrue(queued.waitForExistence(timeout: 4), app.debugDescription)
        capture("Stopping before the first turn", app: app)
        stop.tap()
        XCTAssertTrue(stop.waitForNonExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.staticTexts["队列已暂停"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(queued.exists)
        XCTAssertEqual(editor.value as? String, "Cancel initialization")
        capture("Cancelled initialization leaves the queue paused", app: app)
    }

    func testIPadStopSettlesWithoutStreamingLateDeltas() throws {
        let environment = ProcessInfo.processInfo.environment
        try XCTSkipUnless(environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "stop-feedback")
        let gate = try XCTUnwrap(environment["SHIOJI_MOBILE_STOP_FIXTURE_GATE"])
        let app = try openParallelRunConfigFixture()
        func reply(_ fragment: String) -> XCUIElement {
            app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", fragment)).firstMatch
        }
        XCTAssertTrue(reply("Partial reply before stop").waitForExistence(timeout: 8), app.debugDescription)
        let stop = app.buttons["停止右侧回复"].firstMatch
        let available = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            stop.exists && stop.isHittable && stop.isEnabled
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [available], timeout: 10), .completed, app.debugDescription)
        capture("Right reply before stopping", app: app)
        stop.tap()
        let observed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            FileManager.default.fileExists(atPath: gate + ".delta")
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [observed], timeout: 2), .completed)
        XCTAssertTrue(app.staticTexts["已停止"].firstMatch.exists, app.debugDescription)
        XCTAssertFalse(stop.exists)
        XCTAssertTrue(reply("Partial reply before stop").exists)
        XCTAssertFalse(reply("Late output").exists)
        capture("Right reply stops before the Worker settles", app: app)
        try Data().write(to: URL(fileURLWithPath: gate + ".settle"))
        XCTAssertTrue(reply("Final output after stop").waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        capture("Worker settles the stopped reply", app: app)
    }

    func testIPadParallelFileReferenceStaysWithTheRightDraft() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "attachment")
        let app = try openParallelRunConfigFixture()
        let add = app.buttons["添加右侧附件"].firstMatch
        XCTAssertTrue(add.waitForExistence(timeout: 8), app.debugDescription)
        add.tap()
        let references = app.descendants(matching: .any)["文件与 Skill 引用"].firstMatch
        XCTAssertTrue(references.waitForExistence(timeout: 6), app.debugDescription)
        references.tap()
        let file = app.buttons["owned-artifact.txt"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 10), app.debugDescription)
        file.tap()
        let editor = app.textFields["右侧会话草稿"].firstMatch
        let inserted = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            (editor.value as? String)?.contains("\u{fffc}") == true
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [inserted], timeout: 8), .completed, app.debugDescription)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        capture("Right draft keeps the file reference", app: app)
        app.buttons["发送右侧消息"].firstMatch.tap()
        XCTAssertTrue(app.buttons["预览引用：owned-artifact.txt"].firstMatch.waitForExistence(timeout: 12), app.debugDescription)
    }

    func testDraftSaveFailureKeepsTheDraftAndAllowsRetry() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let statePath = environment["SHIOJI_DRAFT_SAVE_FIXTURE"] else {
            throw XCTSkip("Requires an isolated simulator state directory")
        }
        let parallel = environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "retired-model-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12))
            compose.tap()
        }
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        let initial = try XCTUnwrap(editor.value as? String)
        let enabled = NSPredicate(format: "enabled == true")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)

        let files = FileManager.default
        let state = URL(fileURLWithPath: statePath)
        let backup = state.appendingPathExtension("fixture-backup")
        try files.moveItem(at: state, to: backup)
        try files.createDirectory(at: state, withIntermediateDirectories: false)
        var blocked = true
        defer {
            if blocked { try? files.removeItem(at: state) }
            try? files.removeItem(at: backup)
        }

        editor.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.22)).tap()
        app.typeText(" retry draft")
        let draft = initial + " retry draft"
        let warning = app.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", "草稿尚未保存")).firstMatch
        let retry = app.buttons["重试保存"].firstMatch
        let copy = app.buttons["复制草稿"].firstMatch
        let shown = warning.waitForExistence(timeout: 6)
        capture("Draft after a failed local write", app: app)
        XCTAssertTrue(shown, "The composer did not show that its draft was unsaved")
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertTrue(retry.isEnabled)
        XCTAssertTrue(copy.isEnabled)
        XCTAssertFalse(send.isEnabled)
        if parallel {
            XCTAssertTrue(app.buttons["发送"].firstMatch.isEnabled)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        }
        retry.tap()
        XCTAssertTrue(warning.exists)
        XCTAssertFalse(send.isEnabled)
        copy.tap()
        XCTAssertEqual(editor.value as? String, draft)

        try files.removeItem(at: state)
        blocked = false
        retry.tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 8), .completed)
        XCTAssertFalse(warning.exists)
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertTrue(try String(contentsOf: state, encoding: .utf8).contains(draft))
        capture("Retry saves the same draft and restores Send", app: app)
    }

    func testFindsBranchesAndKeepsTheDraftUntilExplicitAction() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let fixture = environment["SHIOJI_BRANCH_SEARCH_FIXTURE"] else {
            throw XCTSkip("Requires an isolated Worker with a long branch list")
        }
        let mode = environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"]
        let parallel = mode == "search-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = mode == "search-tablet-home" ? .landscapeLeft : .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12), app.debugDescription)
            compose.tap()
        }
        let target = "feature/mobile-search"
        let draft = "Keep this draft while finding a branch"
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(editor.value as? String, draft)

        func mark(_ phase: String) throws {
            try Data().write(to: URL(fileURLWithPath: fixture + "." + phase), options: .atomic)
            let observed = NSPredicate { _, _ in FileManager.default.fileExists(atPath: fixture + "." + phase + ".observed") }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: observed, object: nil)], timeout: 10), .completed)
        }
        let selector = app.buttons[parallel ? "切换到" : "选择分支"].firstMatch
        if parallel {
            try mark("switch")
            let review = app.buttons["查看右侧分支"].firstMatch
            XCTAssertTrue(review.waitForExistence(timeout: 10), app.debugDescription)
            review.tap()
            XCTAssertTrue(selector.waitForExistence(timeout: 8), app.debugDescription)
            selector.tap()
        } else {
            let opened = NSPredicate { _, _ in selector.exists || app.buttons["main"].firstMatch.exists }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: opened, object: nil)], timeout: 12), .completed)
            let worktree = app.descendants(matching: .any)["新建 worktree"].firstMatch
            XCTAssertTrue(worktree.isHittable)
            worktree.tap()
            (selector.exists ? selector : app.buttons["main"].firstMatch).tap()
        }
        let search = app.textFields["搜索分支"].firstMatch
        let available = search.waitForExistence(timeout: 5)
        if !available { capture("Branch chooser before search support", app: app) }
        XCTAssertTrue(available, "Branch selection must support search")
        search.tap()
        app.typeText("no-branch-match")
        let empty = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "没有匹配的分支")).firstMatch
        XCTAssertTrue(empty.waitForExistence(timeout: 5), app.debugDescription)
        app.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: "no-branch-match".count) + "MOBILE-SEARCH")
        let result = app.buttons[target].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 6), app.debugDescription)
        capture("Branch search finds the target in the owning repository", app: app)
        if mode == "search-tablet-home" && app.keyboards.firstMatch.exists {
            // UIKit translates the form sheet while the on-screen keyboard is
            // open. Tap its visible first result on the 1194 × 834 fixture.
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.43, dy: 0.235)).tap()
        } else {
            result.tap()
        }
        XCTAssertTrue(selector.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(selector.value as? String, target)
        try mark("intent")
        capture("Selected branch is shown while the checkout stays put", app: app)

        selector.tap()
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        app.navigationBars["选择分支"].buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(selector.waitForExistence(timeout: 8))
        XCTAssertEqual(selector.value as? String, target)
        if parallel {
            let apply = app.buttons["检查并切换"].firstMatch
            XCTAssertTrue(apply.isEnabled)
            apply.tap()
            XCTAssertTrue(editor.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
            XCTAssertTrue(app.buttons["发送"].firstMatch.isEnabled)
        }
        XCTAssertEqual(editor.value as? String, draft)
        let enabled = NSPredicate(format: "enabled == true")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)
        capture("The original draft is ready to send", app: app)
        send.tap()
        XCTAssertTrue(app.staticTexts[draft].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        let cleared = NSPredicate { _, _ in !editor.exists || (editor.value as? String) != draft }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: cleared, object: nil)], timeout: 15), .completed, app.debugDescription)
    }

    func testConfirmsTheRemoteCheckoutAndKeepsTheCurrentDraft() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let fixture = environment["SHIOJI_BRANCH_CONFIRM_FIXTURE"] else {
            throw XCTSkip("Requires an isolated SSH Worker with a Git checkout")
        }
        let parallel = environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "branch-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let session = app.buttons["左栏设备会话"].firstMatch
            XCTAssertTrue(session.waitForExistence(timeout: 12))
            session.tap()
        }
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        let notice = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "执行分支已改变")).firstMatch
        let draft = "Keep this draft on the confirmed branch"
        let enabled = NSPredicate(format: "enabled == true")
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)

        func change(_ phase: String) throws {
            try Data().write(to: URL(fileURLWithPath: fixture + "." + phase), options: .atomic)
            let written = NSPredicate { _, _ in FileManager.default.fileExists(atPath: fixture + "." + phase + ".observed") }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: written, object: nil)], timeout: 10), .completed)
        }
        try change("switch")
        let appeared = notice.waitForExistence(timeout: 10)
        capture("Changed branch keeps the original draft", app: app)
        XCTAssertTrue(appeared, "The draft needs a visible entry to reconcile the owning checkout")
        XCTAssertFalse(send.isEnabled)
        XCTAssertEqual(editor.value as? String, draft)
        if parallel {
            XCTAssertTrue(app.buttons["发送"].firstMatch.isEnabled)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        }
        app.buttons[parallel ? "查看右侧分支" : "查看分支"].firstMatch.tap()
        let confirm = app.buttons["确认当前分支"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 8))
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: confirm)], timeout: 8), .completed)
        capture("Review the actual branch before confirmation", app: app)

        if !parallel {
            try change("advance")
            confirm.tap()
            let reason = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "请刷新后再确认")).firstMatch
            XCTAssertTrue(reason.waitForExistence(timeout: 8))
            capture("A newer branch requires another review", app: app)
            app.buttons["刷新分支"].firstMatch.tap()
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: confirm)], timeout: 8), .completed)
        }
        confirm.tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)
        XCTAssertFalse(notice.exists)
        XCTAssertEqual(editor.value as? String, draft)
        capture("The confirmed branch can send the same draft", app: app)
        send.tap()
        XCTAssertTrue(app.staticTexts[draft].firstMatch.waitForExistence(timeout: 12), app.debugDescription)
    }

    func testAgentReadinessKeepsTheDraftAndRestoresSend() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let fixture = environment["SHIOJI_RUNTIME_READINESS_FIXTURE"] else {
            throw XCTSkip("Requires an isolated Worker with controlled runtime readiness")
        }
        let parallel = environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "retired-model-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12))
            compose.tap()
        }
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        let run = app.buttons[parallel ? "右侧运行配置" : "运行配置"].firstMatch
        let draft = "Keep the draft while the model changes"
        let enabled = NSPredicate(format: "enabled == true")
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertEqual(run.value as? String, "model-1")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)

        func phase(_ name: String) throws {
            try Data().write(to: URL(fileURLWithPath: fixture + "." + name), options: .atomic)
        }
        func label(_ title: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch
        }
        func verifyPaused(_ title: String, _ screenshot: String) -> CGRect {
            let notice = label(title)
            let appeared = notice.waitForExistence(timeout: 10)
            capture(screenshot, app: app)
            XCTAssertTrue(appeared, "The composer must explain why the connected Agent cannot accept a new message")
            XCTAssertFalse(send.isEnabled)
            XCTAssertEqual(editor.value as? String, draft)
            XCTAssertEqual(run.value as? String, "model-1")
            if parallel {
                XCTAssertTrue(app.buttons["发送"].firstMatch.isEnabled)
                XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
            }
            return notice.frame
        }

        try phase("unavailable")
        let unavailableFrame = verifyPaused("当前 Agent 尚未就绪", "Unavailable Agent keeps the selected model and draft")
        let details = app.buttons["设备详情"].firstMatch
        XCTAssertTrue(details.isEnabled)
        details.tap()
        XCTAssertTrue(app.buttons["远程 Agent"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        if parallel {
            let address = try XCTUnwrap(environment["SHIOJI_RIGHT_FIXTURE_ADDRESS"])
            app.buttons["连接路径"].firstMatch.tap()
            let route = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "直接 SSH，" + address + "，")).firstMatch
            XCTAssertTrue(route.waitForExistence(timeout: 8), app.debugDescription)
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, draft)

        try phase("refresh")
        let refreshingFrame = verifyPaused("正在刷新运行配置…", "Refreshing Agent keeps the selected model and draft")
        XCTAssertEqual(refreshingFrame.midY, unavailableFrame.midY, accuracy: 1, "The status stays vertically aligned when the recovery button disappears")
        try phase("ready")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: enabled, object: send)], timeout: 12), .completed)
        XCTAssertFalse(label("正在刷新运行配置…").exists)
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertEqual(run.value as? String, "model-1")
        capture("Agent ready with the same draft and model", app: app)
        send.tap()
        XCTAssertTrue(app.staticTexts[draft].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
    }

    func testRemovedModelKeepsTheDraftAndRequiresAnExplicitReplacement() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let refreshFile = environment["SHIOJI_MODEL_REMOVAL_FIXTURE"] else {
            throw XCTSkip("Requires an isolated Worker with a changing model catalog")
        }
        let parallel = environment["SHIOJI_RUN_CONFIG_FIXTURE_MODE"] == "retired-model-ipad"
        let app: XCUIApplication
        if parallel {
            app = try openParallelRunConfigFixture()
        } else {
            XCUIDevice.shared.orientation = .portrait
            app = launch("")
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 12), app.debugDescription)
            compose.tap()
        }
        let draft = "Keep the draft while the model changes"
        let editor = app.textFields[parallel ? "右侧会话草稿" : "消息"].firstMatch
        let run = app.buttons[parallel ? "右侧运行配置" : "运行配置"].firstMatch
        let send = app.buttons[parallel ? "发送右侧消息" : "发送"].firstMatch
        let leftSend = app.buttons["发送"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertEqual(run.value as? String, "model-1")
        let readyToSend = NSPredicate(format: "enabled == true")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: readyToSend, object: send)], timeout: 12), .completed)
        capture("Original model is available", app: app)
        try Data().write(to: URL(fileURLWithPath: refreshFile), options: .atomic)

        let disabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == false"), object: send)
        if XCTWaiter.wait(for: [disabled], timeout: 12) != .completed {
            capture("Unavailable model still allows tapping Send", app: app)
            XCTFail("The retired model still allowed Send", file: #filePath, line: #line)
        }
        XCTAssertEqual(run.value as? String, "model-1")
        XCTAssertTrue(run.isEnabled)
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertFalse(app.staticTexts["当前模型已不可用，请更换模型"].exists)
        if parallel {
            XCTAssertTrue(leftSend.isEnabled)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        }
        capture("Retired model keeps its draft and disables sending", app: app)
        run.tap()
        let replacement = app.descendants(matching: .any)["model-2"].firstMatch
        XCTAssertTrue(replacement.waitForExistence(timeout: 8), app.debugDescription)
        capture("Replacement model remains available in the same menu", app: app)
        replacement.tap()
        XCTAssertEqual(run.value as? String, "model-2")
        XCTAssertEqual(editor.value as? String, draft)
        XCTAssertTrue(send.isEnabled)
        capture("Explicit replacement restores the same draft's Send button", app: app)
        send.tap()
        let submitted = app.staticTexts.matching(NSPredicate(format: "label == %@", draft)).firstMatch
        XCTAssertTrue(submitted.waitForExistence(timeout: 15), app.debugDescription)
        capture("The preserved draft is sent with the replacement model", app: app)
        if !parallel {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
            let original = app.buttons["左栏设备会话"].firstMatch
            XCTAssertTrue(original.waitForExistence(timeout: 8), app.debugDescription)
            original.tap()
            let options = app.buttons["发送选项"].firstMatch
            XCTAssertTrue(options.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: readyToSend, object: options)], timeout: 12), .completed)
            XCTAssertEqual(run.value as? String, "model-1")
            options.tap()
            let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
            let enqueue = app.descendants(matching: .any)["加入待发队列"].firstMatch
            XCTAssertTrue(steer.waitForExistence(timeout: 6), app.debugDescription)
            XCTAssertTrue(steer.isEnabled)
            XCTAssertTrue(enqueue.exists)
            XCTAssertFalse(enqueue.isEnabled)
            capture("The old running turn still accepts steering", app: app)
            steer.tap()
            XCTAssertTrue(app.staticTexts["First Worker draft"].firstMatch.waitForExistence(timeout: 12), app.debugDescription)
            let stop = app.buttons["停止"].firstMatch
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: readyToSend, object: stop)], timeout: 6), .completed)
        } else {
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
            XCTAssertTrue(leftSend.isEnabled)
        }
    }

    func testRemoteModelPreferencesReachTheComposerAndSurviveRelaunch() throws {
        guard let refreshFile = ProcessInfo.processInfo.environment["SHIOJI_MODEL_MENU_FIXTURE"] else {
            throw XCTSkip("Requires an isolated Worker model catalog")
        }
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        func tap(_ name: String) {
            let button = app.buttons[name].firstMatch
            XCTAssertTrue(button.waitForExistence(timeout: 10), app.debugDescription)
            button.tap()
        }
        tap("设备")
        tap("127.0.0.1")
        tap("远程 Agent")
        tap("Memory alpha")
        tap("模型")
        capture("Worker's model catalog", app: app)
        tap("收藏 Alpha Focus")
        XCTAssertTrue(app.buttons["取消收藏 Alpha Focus"].firstMatch.exists)
        let show = app.buttons["在模型菜单显示 Alpha Compact"].firstMatch
        XCTAssertTrue(show.isSelected, app.debugDescription)
        show.tap()
        XCTAssertFalse(show.isSelected)
        tap("Alpha Focus 模型操作")
        let up = app.descendants(matching: .any)["上移"].firstMatch
        XCTAssertTrue(up.waitForExistence(timeout: 5), app.debugDescription)
        up.tap()
        let focus = app.staticTexts["Alpha Focus"].firstMatch
        let compact = app.staticTexts["Alpha Compact"].firstMatch
        XCTAssertLessThan(focus.frame.minY, compact.frame.minY)
        XCTAssertFalse(app.staticTexts["Alpha Fresh"].exists)
        try Data().write(to: URL(fileURLWithPath: refreshFile), options: .atomic)
        tap("刷新模型")
        XCTAssertTrue(app.staticTexts["Alpha Fresh"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(show.isSelected)
        capture("Model preferences after refreshing from the Worker", app: app)

        app.terminate()
        app.launch()
        tap("新建会话")
        tap("运行配置")
        let choices = app.descendants(matching: .any)
        let favorite = choices["Alpha Focus"].firstMatch
        let ordinary = choices["alpha-model"].firstMatch
        XCTAssertTrue(favorite.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(ordinary.exists)
        XCTAssertLessThan(favorite.frame.minY, ordinary.frame.minY)
        XCTAssertFalse(choices["Alpha Compact"].exists)
        XCTAssertTrue(choices["Alpha Fresh"].exists)
        capture("New conversation model menu uses the saved order", app: app)
    }

    func testRemoteRuntimeSettingsEditAndRetainWorkerOwnedValues() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_RUNTIME_SETTINGS_FIXTURE"] == "1" else {
            throw XCTSkip("Requires isolated runtime settings on the Worker")
        }
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("")
        func open(_ label: String) {
            let button = app.buttons[label].firstMatch
            XCTAssertTrue(button.waitForExistence(timeout: 10), app.debugDescription)
            if label.hasPrefix("FIXTURE_") || label == "添加变量" {
                let save = app.buttons["保存并继续"].firstMatch
                if save.exists && button.frame.midY >= save.frame.minY - 12 {
                    let start = app.coordinate(withNormalizedOffset: .zero)
                        .withOffset(CGVector(dx: button.frame.midX, dy: save.frame.minY - 45))
                    let finish = start.withOffset(CGVector(dx: 0, dy: -136))
                    start.press(forDuration: 0.04, thenDragTo: finish)
                }
            }
            button.tap()
        }
        open("设备")
        open("127.0.0.1")
        open("远程 Agent")
        open("Memory alpha")
        open("高级")
        let arguments = app.textFields["启动参数"].firstMatch
        capture("Agent advanced settings before typing", app: app)
        XCTAssertTrue(arguments.waitForExistence(timeout: 8), "The mobile Agent is missing the launch options")
        let directory = app.textFields["配置目录"].firstMatch
        let instructions = app.descendants(matching: .any)["自定义提示词输入"].firstMatch
        instructions.coordinate(withNormalizedOffset: CGVector(dx: 0.2, dy: 0.2)).tap()
        app.typeText("Use the fixture language.")
        directory.tap()
        app.typeText("/tmp")
        arguments.tap()
        app.typeText("--profile \"with space\"")
        open("模型")
        open("标题生成")
        let titleChoices = app.descendants(matching: .any).matching(identifier: "标题模型")
        XCTAssertTrue(titleChoices.element(boundBy: 1).waitForExistence(timeout: 6), app.debugDescription)
        titleChoices.element(boundBy: titleChoices.count - 1).tap()
        XCTAssertEqual(app.buttons["标题生成"].firstMatch.value as? String, "标题模型")
        open("高级")
        open("FIXTURE_KEEP")
        let name = app.textFields["变量名"].firstMatch
        let storedValue = app.textFields["变量值"].firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 8))
        XCTAssertTrue(storedValue.waitForExistence(timeout: 6))
        XCTAssertFalse(app.debugDescription.contains("fixture-existing-private-value"))
        open("显示变量值")
        XCTAssertTrue(app.buttons["隐藏变量值"].waitForExistence(timeout: 8))
        XCTAssertEqual(storedValue.value as? String, "fixture-existing-private-value")
        open("隐藏变量值")
        XCTAssertFalse(app.debugDescription.contains("fixture-existing-private-value"))
        name.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.5)).tap()
        app.typeText("_RENAMED")
        open("完成")
        XCTAssertTrue(app.buttons["FIXTURE_KEEP_RENAMED"].waitForExistence(timeout: 8), app.debugDescription)
        open("添加变量")
        let newName = app.textFields["变量名"].firstMatch
        XCTAssertTrue(newName.waitForExistence(timeout: 8), app.debugDescription)
        newName.tap()
        app.typeText("FIXTURE_MOBILE")
        app.textFields["变量值"].firstMatch.tap()
        app.typeText("mobile-fixture-value")
        XCTAssertFalse(app.debugDescription.contains("mobile-fixture-value"))
        open("完成")
        XCTAssertTrue(app.buttons["FIXTURE_MOBILE"].waitForExistence(timeout: 8), app.debugDescription)
        open("FIXTURE_DELETE")
        open("删除变量")
        open("保存并继续")
        let saved = app.descendants(matching: .any)["已保存运行设置"].firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(app.buttons["FIXTURE_KEEP_RENAMED"].exists)
        XCTAssertTrue(app.buttons["FIXTURE_MOBILE"].exists)
        XCTAssertFalse(app.buttons["FIXTURE_DELETE"].exists)
        capture("Saved launch options and masked environment list", app: app)
        open("模型")
        XCTAssertEqual(app.buttons["标题生成"].firstMatch.value as? String, "标题模型")
    }

    func testRemoteAgentDeviceCodeCanCancelAndComplete() {
        let app = launch("--preview-board-agent-config")
        let start = app.buttons["开始登录"]
        XCTAssertTrue(start.waitForExistence(timeout: 8))
        start.tap()
        let code = app.staticTexts["ABCD-EFGH"]
        XCTAssertTrue(code.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["打开授权页"].exists)
        capture("Device code on the owning Agent", app: app)
        app.buttons["取消登录"].tap()
        XCTAssertTrue(code.waitForNonExistence(timeout: 5))
        XCTAssertTrue(start.exists)
        start.tap()
        XCTAssertTrue(code.waitForExistence(timeout: 5))
        app.buttons["检查授权结果"].tap()
        XCTAssertTrue(app.staticTexts["已完成授权"].waitForExistence(timeout: 5))
        XCTAssertFalse(code.exists)
    }

    func testRemoteMCPAddsTogglesAndRemovesAnHTTPServiceInsideTheAgentTab() {
        let app = launch("--preview-board-agent-config")
        let tab = app.descendants(matching: .any).matching(identifier: "MCP").firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 8))
        tab.tap()
        capture("MCP tab before configuration", app: app)
        let add = app.buttons["添加 MCP 服务"]
        guard add.waitForExistence(timeout: 5) else { return XCTFail("The MCP tab has no Add action") }
        add.tap()
        app.descendants(matching: .any).matching(identifier: "HTTP").firstMatch.tap()
        let name = app.textFields["MCP 名称"]
        let address = app.textFields["MCP 服务地址"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        app.typeText("review-tools")
        app.typeText("\n")
        app.typeText("https://tools.example.test/mcp")
        XCTAssertEqual(address.value as? String, "https://tools.example.test/mcp")
        capture("Editing HTTP MCP in place", app: app)
        app.descendants(matching: .any).matching(identifier: "模型").firstMatch.tap()
        tab.tap()
        XCTAssertEqual(name.value as? String, "review-tools")
        XCTAssertEqual(address.value as? String, "https://tools.example.test/mcp")
        app.buttons["保存 MCP 服务"].tap()
        let edit = app.buttons["编辑 MCP：review-tools"]
        XCTAssertTrue(edit.waitForExistence(timeout: 5))
        XCTAssertTrue(app.navigationBars["Codex"].exists)
        let toggle = app.descendants(matching: .any).matching(identifier: "启用 MCP：review-tools").firstMatch
        XCTAssertTrue(toggle.exists)
        XCTAssertTrue(toggle.isSelected)
        toggle.tap()
        let disabled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            toggle.exists && !toggle.isSelected && (edit.value as? String) == "已停用"
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [disabled], timeout: 5), .completed)
        edit.tap()
        let delete = app.buttons["删除 MCP 服务"]
        XCTAssertTrue(delete.waitForExistence(timeout: 5))
        delete.tap()
        app.buttons["确认删除 MCP"].tap()
        XCTAssertTrue(edit.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons["编辑 MCP：Documentation"].exists)
        capture("MCP service list after editing", app: app)
    }

    func testRemoteMCPEditKeepsServerSideCredentialsAndSupportsAuthorization() {
        let app = launch("--preview-board-agent-config")
        let tab = app.descendants(matching: .any).matching(identifier: "MCP").firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 8))
        tab.tap()
        let documentation = app.buttons["编辑 MCP：Documentation"]
        guard documentation.waitForExistence(timeout: 5) else { return XCTFail("The MCP catalog did not load") }
        documentation.tap()
        XCTAssertTrue(app.textFields["MCP 名称"].waitForExistence(timeout: 5))
        app.buttons["高级选项"].tap()
        XCTAssertTrue(app.staticTexts["X-Service-Key"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["已保留"].exists)
        capture("MCP retained header", app: app)
        app.buttons["高级选项"].tap()
        app.buttons["保存 MCP 服务"].tap()
        XCTAssertTrue(documentation.waitForExistence(timeout: 5))
        app.buttons["授权 Calendar"].tap()
        let browser = app.buttons["打开 MCP 授权页"]
        XCTAssertTrue(browser.waitForExistence(timeout: 5))
        capture("MCP browser authorization", app: app)
        browser.tap()
        let done = app.buttons.matching(NSPredicate(format: "label == %@ OR label == %@", "Done", "完成")).firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 8))
        if UIDevice.current.userInterfaceIdiom == .phone {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.095, dy: 0.10)).tap()
        } else {
            done.tap()
        }
        XCTAssertTrue(browser.waitForExistence(timeout: 5))
        app.buttons["检查 MCP 授权结果"].tap()
        XCTAssertTrue(app.staticTexts["2 个工具"].waitForExistence(timeout: 5))
    }

    func testRemoteAgentAPISetupShowsNativeMaskedKeyAndConfirmedProvider() {
        let app = launch("--preview-board-agent-config")
        let mode = app.buttons["接入方式"]
        XCTAssertTrue(mode.waitForExistence(timeout: 8))
        mode.tap()
        let api = app.descendants(matching: .any).matching(identifier: "自定义 API").firstMatch
        XCTAssertTrue(api.waitForExistence(timeout: 5))
        api.tap()
        let endpoint = app.textFields["API 地址"]
        let key = app.textFields["API Key"]
        let connect = app.buttons["连接 API"]
        XCTAssertTrue(endpoint.waitForExistence(timeout: 5))
        XCTAssertTrue(key.exists)
        XCTAssertFalse(connect.isEnabled)
        XCTAssertFalse(app.buttons["保存并继续"].exists)
        endpoint.tap()
        app.typeText("https://api.example.test/v1")
        app.typeText("\n")
        app.typeText("fixture-key")
        XCTAssertFalse(app.debugDescription.contains("fixture-key"))
        capture("Remote API setup", app: app)
        app.typeText("\n")
        XCTAssertTrue(app.staticTexts["Team API"].waitForExistence(timeout: 5))
        XCTAssertFalse(key.exists)
        XCTAssertTrue(app.staticTexts["已配置"].exists)
        capture("Confirmed remote API", app: app)
    }

    func testRemoteAgentBrowserOpensSystemSheetFromSettings() {
        let app = launch("--preview-board-agent-config")
        let method = app.buttons["登录方式"]
        XCTAssertTrue(method.waitForExistence(timeout: 8))
        method.tap()
        app.descendants(matching: .any).matching(identifier: "浏览器授权").firstMatch.tap()
        app.buttons["开始登录"].tap()
        let open = app.buttons["打开授权页"]
        XCTAssertTrue(open.waitForExistence(timeout: 6))
        XCTAssertFalse(app.buttons["复制验证码"].exists)
        open.tap()
        let done = app.buttons.matching(NSPredicate(format: "label == %@ OR label == %@", "Done", "完成")).firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 8), app.debugDescription)
        if UIDevice.current.userInterfaceIdiom == .phone {
            // On iPhone, Safari also exposes a bottom stop-loading control.
            // The session's dismiss button is in the leading navigation slot.
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.095, dy: 0.10)).tap()
        } else {
            done.tap()
        }
        XCTAssertTrue(open.waitForExistence(timeout: 5))
    }

    func testPendingSessionCountAppearsOnlyOnTheCollapsedGroup() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-inbox")
        continueAfterFailure = false
        let group = app.buttons["待我处理"].firstMatch
        let row = app.buttons["核对 Worker 配置"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue((row.value as? String)?.contains("1 项待处理") == true)
        capture("Expanded group with a pending session", app: app)
        let rowFrame = row.frame
        XCTAssertFalse(hasVisibleInk(in: CGRect(x: rowFrame.maxX - 28, y: rowFrame.minY + 5, width: 20, height: rowFrame.height - 10)),
                       "The trailing part of the session row must be free of a numeric badge")
        group.tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 5))
        XCTAssertTrue((group.value as? String)?.contains("1 项待处理") == true)
        capture("Pending session total on the collapsed group", app: app)
        group.tap()
        XCTAssertTrue(row.waitForExistence(timeout: 5))
    }

    func testReceiptAndSteeringStayWithTheirConversation() {
        var app = launch("--preview-board-operation-receipt-long")
        XCTAssertTrue(app.buttons["查询接纳结果"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["会话活动"].exists)
        XCTAssertFalse(app.navigationBars["核对提交结果"].exists)
        let viewOriginal = app.buttons["查看原文"]
        let body = app.descendants(matching: .any)["待核对消息原文"].firstMatch
        XCTAssertFalse(body.exists)
        capture("Collapsed receipt beside the conversation", app: app)
        XCTAssertTrue(viewOriginal.exists, "A truncated preview needs an inline path to the full original")
        viewOriginal.tap()
        XCTAssertTrue(body.waitForExistence(timeout: 4), app.debugDescription)
        let original = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "清单末尾")).firstMatch
        XCTAssertTrue(original.exists, app.debugDescription)
        XCTAssertLessThan(body.frame.height, app.frame.height / 3)
        let originalTop = original.frame.minY
        let lookup = app.buttons["查询接纳结果"]
        let lookupTop = lookup.frame.minY
        body.swipeUp()
        XCTAssertLessThan(original.frame.minY, originalTop, "The text should scroll inside the receipt")
        XCTAssertEqual(lookup.frame.minY, lookupTop, accuracy: 1)
        XCTAssertTrue(lookup.isEnabled)
        XCTAssertTrue(app.buttons["会话活动"].exists)
        capture("The pending receipt shows the scrollable original message inline", app: app)
        app.buttons["收起原文"].tap()
        XCTAssertTrue(body.waitForNonExistence(timeout: 4))
        app.terminate()
        app = launch("--preview-board-steer")
        let send = app.descendants(matching: .any).matching(identifier: "发送选项").firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.buttons["会话活动"].exists)
        send.tap()
        capture("Steering menu", app: app)
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "介入当前轮").firstMatch.waitForExistence(timeout: 4), app.debugDescription)
    }

    func testSteerMenuSendsAfterDismissingTheSystemTextMenu() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_SUBMISSION_RECEIPT_FIXTURE"] == "healthy",
                          "Requires a running turn and a saved draft on the isolated Worker")
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let session = app.buttons["发送回执验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 12))
        session.tap()
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8), app.debugDescription)
        composer.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        composer.coordinate(withNormalizedOffset: CGVector(dx: 0.12, dy: 0.25)).press(forDuration: 0.55)
        let editMenu = app.descendants(matching: .any)
            .matching(NSPredicate(format: "label IN[c] %@", ["Select All", "全选", "全選"])).firstMatch
        XCTAssertTrue(editMenu.waitForExistence(timeout: 5), app.debugDescription)
        capture("System edit menu above the focused composer", app: app)

        func tapSurface(_ element: XCUIElement) {
            let frame = element.frame
            let origin = app.coordinate(withNormalizedOffset: .zero)
            origin.withOffset(CGVector(dx: frame.midX - app.frame.minX, dy: frame.midY - app.frame.minY)).tap()
        }
        let send = app.buttons["发送选项"].firstMatch
        XCTAssertTrue(send.isEnabled)
        // The first outside touch dismisses UIKit's edit menu on iPad.
        tapSurface(send)
        XCTAssertTrue(editMenu.waitForNonExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(app.keyboards.firstMatch.exists)
        XCTAssertEqual(composer.value as? String, "Send this once.")
        let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
        if !steer.exists {
            tapSurface(send)
        }
        XCTAssertTrue(steer.waitForExistence(timeout: 5), app.debugDescription)
        capture("Send options opened from the focused composer", app: app)
        XCTAssertTrue(steer.isEnabled)
        tapSurface(steer)

        let message = app.staticTexts["Send this once."].firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertNotEqual(composer.value as? String, "Send this once.")
        capture("Steering arrived in the original running turn", app: app)
    }

    func testSubmissionLooksUpOnlyAnUncertainReceipt() throws {
        let mode = ProcessInfo.processInfo.environment["SHIOJI_SUBMISSION_RECEIPT_FIXTURE"]
        try XCTSkipUnless(mode != nil, "Requires an isolated Worker that delays the harness receipt")
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let session = app.buttons["发送回执验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 12))
        session.tap()
        let send = app.buttons["发送选项"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 8), app.debugDescription)
        send.tap()
        let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
        XCTAssertTrue(steer.waitForExistence(timeout: 5))
        steer.tap()

        let lookup = app.buttons["查询接纳结果"].firstMatch
        capture("Waiting for a normal submission receipt", app: app)
        XCTAssertFalse(lookup.waitForExistence(timeout: 2), "A healthy in-flight submission exposed a receipt lookup")
        XCTAssertTrue(app.buttons["正在发送"].firstMatch.exists)
        let text = "Send this once."
        if mode == "uncertain" {
            XCTAssertTrue(lookup.waitForExistence(timeout: 12), app.debugDescription)
            XCTAssertTrue(lookup.isEnabled)
            XCTAssertTrue(app.staticTexts["消息是否送达仍待确认"].firstMatch.exists)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, text)
            capture("Only an uncertain submission offers receipt lookup", app: app)
            app.buttons["查看原文"].tap()
            let original = app.descendants(matching: .any)["待核对消息原文"].firstMatch
            XCTAssertTrue(original.waitForExistence(timeout: 5), app.debugDescription)
            XCTAssertTrue(app.staticTexts[text].firstMatch.exists)
            lookup.tap()
            let queryReady = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == true"), object: lookup)
            XCTAssertEqual(XCTWaiter.wait(for: [queryReady], timeout: 6), .completed)
            XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, text)
        } else {
            let accepted = app.staticTexts[text].firstMatch
            XCTAssertTrue(accepted.waitForExistence(timeout: 12), app.debugDescription)
            XCTAssertTrue(app.buttons["正在发送"].firstMatch.waitForNonExistence(timeout: 6))
            XCTAssertFalse(lookup.exists)
            XCTAssertNotEqual(app.textFields["消息"].firstMatch.value as? String, text)
            capture("Normal receipt settles without a lookup", app: app)
        }
    }

    func testRejectedSubmissionShowsTheWorkerReasonBesideTheDraft() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_SUBMISSION_RECEIPT_FIXTURE"] == "rejected",
                          "Requires an isolated Worker with a definitive send rejection")
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("")
        let session = app.buttons["发送回执验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 14), app.debugDescription)
        session.tap()
        let send = app.buttons["发送选项"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 8), app.debugDescription)
        send.tap()
        let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
        XCTAssertTrue(steer.waitForExistence(timeout: 6))
        steer.tap()

        let failed = app.otherElements.containing(NSPredicate(format: "label BEGINSWITH %@", "消息未送达 Agent")).firstMatch
        XCTAssertTrue(failed.waitForExistence(timeout: 14), app.debugDescription)
        let draft = app.textFields["消息"].firstMatch
        XCTAssertEqual(draft.value as? String, "Send this once.")
        XCTAssertFalse(app.buttons["查询接纳结果"].exists)
        capture("The execution device rejected the submission", app: app)

        let reason = app.staticTexts["Agent 暂时无法接收补充消息，请稍后重试。"].firstMatch
        XCTAssertTrue(reason.exists, "The submission notice should explain why the Worker rejected it")
        let details = app.descendants(matching: .any)["发送失败详情"].firstMatch
        XCTAssertFalse(details.exists)
        app.buttons["查看详情"].firstMatch.tap()
        XCTAssertTrue(details.waitForExistence(timeout: 5), app.debugDescription)
        let diagnostic = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "发送诊断末尾")).firstMatch
        XCTAssertTrue(diagnostic.exists, app.debugDescription)
        XCTAssertTrue(diagnostic.label.contains("token=[已隐藏]"))
        XCTAssertFalse(diagnostic.label.contains("fixture-secret"))
        let heading = failed.frame.minY
        let beginning = diagnostic.frame.minY
        details.swipeUp()
        XCTAssertLessThan(diagnostic.frame.minY, beginning)
        XCTAssertEqual(failed.frame.minY, heading, accuracy: 1)
        capture("The expanded failure keeps the full redacted diagnostic", app: app)

        app.buttons["返回草稿"].firstMatch.tap()
        XCTAssertTrue(failed.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(draft.value as? String, "Send this once.")
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["最近操作"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["最近操作"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "发送诊断末尾")).firstMatch.exists)
    }

    func testSendingAfterLongReplyKeepsTheNewMessageAboveTheComposer() throws {
        let mode = ProcessInfo.processInfo.environment["SHIOJI_TRANSCRIPT_SEND"]
        try XCTSkipUnless(mode == "steer" || mode == "follow-up", "Requires an isolated Worker with overflowing conversation history")
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let session = app.buttons["同一轮回复验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15))
        session.tap()
        let previous = app.staticTexts["STEER SCROLL ANCHOR"].firstMatch
        XCTAssertTrue(previous.waitForExistence(timeout: 10), app.debugDescription)
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(draft.exists)
        let message = "Keep this message in view.\nInclude the new constraint.\nContinue with the remaining work."
        XCTAssertEqual(draft.value as? String, message)
        let sent = app.staticTexts[message].firstMatch
        let before = previous.frame.minY
        let latest = app.buttons["回到最新消息"].firstMatch
        XCTAssertFalse(latest.exists)
        if mode == "steer" {
            let send = app.buttons["发送选项"].firstMatch
            XCTAssertTrue(send.isEnabled, app.debugDescription)
            send.tap()
            let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
            XCTAssertTrue(steer.waitForExistence(timeout: 5), app.debugDescription)
            capture("Before inserting a message into the current turn", app: app)
            steer.tap()
        } else {
            let send = app.buttons["发送"].firstMatch
            XCTAssertTrue(send.isEnabled, app.debugDescription)
            capture("Before sending again after a long first reply", app: app)
            send.tap()
        }
        XCTAssertTrue(sent.waitForExistence(timeout: 12), app.debugDescription)
        let deadline = Date().addingTimeInterval(4)
        while Date() < deadline && (sent.frame.height <= 0 || sent.frame.maxY > draft.frame.minY + 1) {
            Thread.sleep(forTimeInterval: 0.05)
        }
        XCTAssertGreaterThan(sent.frame.height, 0)
        XCTAssertGreaterThan(sent.frame.minY, app.frame.minY + 80)
        XCTAssertLessThanOrEqual(sent.frame.maxY, draft.frame.minY + 1)
        XCTAssertNotEqual(draft.value as? String, message)
        XCTAssertFalse(latest.exists)
        if previous.exists && previous.frame.height > 0 {
            XCTAssertLessThan(previous.frame.minY, before + 1)
        }
        capture("The accepted message is visible above the composer", app: app)
    }

    func testInsertedInputKeepsBothReplyFooters() throws {
        let mode = ProcessInfo.processInfo.environment["SHIOJI_STEER_FOOTER_COPY"]
        try XCTSkipUnless(mode != nil, "Requires the isolated Worker with an inserted message")
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let session = app.buttons["同一轮回复验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15))
        session.tap()
        let live = ProcessInfo.processInfo.environment["SHIOJI_STEER_FOOTER_LIVE"] == "1"
        let initial = app.staticTexts["这是初步结论。"].firstMatch
        let inserted = app.staticTexts[live ? "Apply the additional constraint." : "补充一个约束。"].firstMatch
        if live {
            XCTAssertTrue(initial.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertFalse(app.buttons["轮次中途无法分叉"].exists)
            capture("Running reply before steering", app: app)
            let send = app.buttons["发送选项"].firstMatch
            XCTAssertTrue(send.waitForExistence(timeout: 6), app.debugDescription)
            send.tap()
            let steer = app.descendants(matching: .any)["介入当前轮"].firstMatch
            XCTAssertTrue(steer.waitForExistence(timeout: 5))
            steer.tap()
            XCTAssertTrue(app.buttons["轮次中途无法分叉"].waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertTrue(inserted.waitForExistence(timeout: 5))
            capture("Earlier receipt and accepted steering message", app: app)
        }
        let laterReply = app.staticTexts["已根据补充约束复查。"].firstMatch
        XCTAssertTrue(laterReply.waitForExistence(timeout: 8), app.debugDescription)
        capture("Reply footers around an inserted message", app: app)
        let copies = app.buttons.matching(identifier: "复制回复")
        XCTAssertEqual(copies.count, 2, "Each side of the inserted message must have its own reply footer")
        let earlier = copies.element(boundBy: 0)
        let later = copies.element(boundBy: 1)
        XCTAssertTrue(initial.exists)
        XCTAssertTrue(inserted.exists)
        XCTAssertGreaterThan(earlier.frame.minY, initial.frame.maxY)
        XCTAssertLessThan(earlier.frame.maxY, inserted.frame.minY)
        XCTAssertGreaterThan(later.frame.minY, laterReply.frame.maxY)
        XCTAssertFalse(app.buttons["轮次中途无法分叉"].firstMatch.isEnabled)
        XCTAssertTrue(app.buttons["从这里分叉"].firstMatch.exists)
        XCTAssertEqual(app.buttons.matching(identifier: "回复详情").count, 1)
        if !live {
            XCTAssertTrue(app.staticTexts["用时 1分20秒"].firstMatch.exists)
            XCTAssertTrue(app.staticTexts["用时 3分00秒"].firstMatch.exists)
        }
        if mode == "earlier" {
            earlier.tap()
        } else {
            later.tap()
        }
    }

    func testRequestPanelHeaderCentersItsGlyphs() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        for (board, name, content) in [
            ("request-question", "回答问题", "自定义答案"),
            ("approval", "命令审批", "允许这一次"),
            ("request-access", "文件与权限审批", "允许此次变更"),
        ] {
            let app = launch("--preview-board-" + board)
            let header = app.buttons[name].firstMatch
            XCTAssertTrue(header.waitForExistence(timeout: 8))
            let frame = header.frame
            XCTAssertGreaterThanOrEqual(frame.height, 43)
            capture("Centered request header " + board, app: app)
            XCTAssertTrue(hasVisibleInk(in: CGRect(x: frame.minX + 10, y: frame.midY - 4, width: 16, height: 8)),
                          "The leading shield must paint at the vertical center of the title area")
            XCTAssertTrue(hasVisibleInk(in: CGRect(x: frame.maxX - 26, y: frame.midY - 4, width: 16, height: 8)),
                          "The disclosure chevron must paint at the same vertical center")
            let choice = app.buttons[content].firstMatch
            XCTAssertTrue(choice.exists)
            header.tap()
            XCTAssertTrue(choice.waitForNonExistence(timeout: 4))
            header.tap()
            XCTAssertTrue(choice.waitForExistence(timeout: 5))
            app.terminate()
        }
    }

    func testQuestionOpensCustomAnswerAfterSelectingIt() {
        let app = launch("--preview-board-request-question")
        let custom = app.buttons["自定义答案"].firstMatch
        XCTAssertTrue(custom.waitForExistence(timeout: 8))
        XCTAssertFalse(app.textViews["自定义答案内容"].exists)
        custom.tap()
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "自定义答案内容").firstMatch.waitForExistence(timeout: 5))
    }

    func testQuestionCanBeDeferredAndReopenedWithItsAnswer() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-request-question")
        let deferAnswer = app.buttons["暂不选择"].firstMatch
        let submit = app.buttons["提交答案"].firstMatch
        let header = app.buttons["回答问题"].firstMatch
        XCTAssertTrue(submit.waitForExistence(timeout: 8))
        let expandedTop = header.frame.minY
        capture("Board request-question", app: app)
        XCTAssertTrue(deferAnswer.exists)
        XCTAssertFalse(submit.isEnabled)
        let choice = app.buttons["保留现有状态"].firstMatch
        choice.tap()
        XCTAssertTrue(choice.isSelected)
        XCTAssertTrue(submit.isEnabled)
        deferAnswer.tap()
        XCTAssertTrue(submit.waitForNonExistence(timeout: 5))
        XCTAssertTrue(header.exists)
        XCTAssertGreaterThan(header.frame.minY, expandedTop + 20)
        XCTAssertTrue(app.textFields["消息"].firstMatch.exists)
        capture("Deferred question remains pending above the composer", app: app)
        app.buttons["查看请求来源"].firstMatch.tap()
        XCTAssertTrue(submit.waitForExistence(timeout: 5))
        let returnedToExpandedHeight = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            abs(header.frame.minY - expandedTop) < 2
        }, object: header)
        XCTAssertEqual(XCTWaiter.wait(for: [returnedToExpandedHeight], timeout: 4), .completed)
        XCTAssertTrue(choice.isSelected)
        XCTAssertTrue(submit.isEnabled)
        capture("Reopened question retains the answer", app: app)
    }

    func testPlanActivitiesUseTheSameContentInsetAsSteps() {
        let app = launch("--preview-board-plan")
        let steps = app.staticTexts["计划步骤"].firstMatch
        let agent = app.buttons["连接验证"].firstMatch
        let background = app.buttons["重连回归"].firstMatch
        XCTAssertTrue(steps.waitForExistence(timeout: 8))
        XCTAssertTrue(agent.exists)
        XCTAssertTrue(background.exists)
        capture("Board plan", app: app)
        XCTAssertEqual(agent.frame.minX, steps.frame.minX, accuracy: 1)
        XCTAssertEqual(background.frame.minX, steps.frame.minX, accuracy: 1)
        agent.tap()
        XCTAssertTrue(app.navigationBars["连接验证"].waitForExistence(timeout: 5))
    }

    private func openChangesInReviewFixture() -> XCUIApplication {
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons["比较范围验收"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 15))
        session.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        activity.tap()
        let changes = app.buttons["工作区改动"].firstMatch
        XCTAssertTrue(changes.waitForExistence(timeout: 8))
        changes.tap()
        return app
    }

    func testReviewComparisonKeepsConfirmedFilesAndRetriesTheFailedRange() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_REVIEW_RANGE_RECOVERY_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the isolated Git comparison worker")
        }
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        let app = openChangesInReviewFixture()
        let scratch = app.buttons["scratch.txt"].firstMatch
        let history = app.buttons["history.txt"].firstMatch
        XCTAssertTrue(scratch.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse(history.exists)
        app.buttons["选择比较范围"].firstMatch.tap()
        let base = app.buttons["基准"].firstMatch
        XCTAssertTrue(base.waitForExistence(timeout: 6))
        base.tap()
        let branch = app.descendants(matching: .any)["retry-base"].firstMatch
        XCTAssertTrue(branch.waitForExistence(timeout: 8), app.debugDescription)
        branch.tap()

        let retry = app.buttons["重试比较"].firstMatch
        let anyRetry = app.buttons.matching(NSPredicate(format: "label IN %@", ["重试比较", "重试"])).firstMatch
        XCTAssertTrue(anyRetry.waitForExistence(timeout: 8), app.debugDescription)
        capture("Failed comparison while the previous range is still selected", app: app)
        XCTAssertTrue(scratch.exists, "A failed comparison must preserve the previously confirmed file list")
        XCTAssertEqual(base.value as? String, "HEAD")
        XCTAssertFalse(history.exists)
        XCTAssertTrue(retry.exists)
        scratch.tap()
        let scratchDiff = app.descendants(matching: .any)["文件差异：scratch.txt"].firstMatch
        XCTAssertTrue(scratchDiff.waitForExistence(timeout: 8), app.debugDescription)
        if tablet && app.buttons["返回文件列表"].exists {
            app.buttons["返回文件列表"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(retry.waitForExistence(timeout: 6))
        retry.tap()
        XCTAssertTrue(history.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(base.value as? String, "retry-base")
        XCTAssertTrue(scratch.exists)
        XCTAssertFalse(retry.exists)
        capture("Retry applied the requested comparison", app: app)
        history.tap()
        let historyDiff = app.descendants(matching: .any)["文件差异：history.txt"].firstMatch
        XCTAssertTrue(historyDiff.waitForExistence(timeout: 8), app.debugDescription)
        capture("File detail loaded from the confirmed comparison", app: app)
    }

    func testReviewRangeSearchUsesRemoteAndLocalRefs() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_REVIEW_RANGE_RECOVERY_FIXTURE"] == "search" else {
            throw XCTSkip("Requires the Git fixture with local and remote comparison refs")
        }
        let app = openChangesInReviewFixture()
        let scratch = app.buttons["scratch.txt"].firstMatch
        XCTAssertTrue(scratch.waitForExistence(timeout: 10), app.debugDescription)
        app.buttons["选择比较范围"].firstMatch.tap()
        let base = app.buttons["基准"].firstMatch
        let target = app.buttons["目标"].firstMatch
        XCTAssertTrue(base.waitForExistence(timeout: 6))
        base.tap()
        capture("Opened the comparison version selector", app: app)
        let search = app.textFields["搜索比较分支"].firstMatch
        guard search.waitForExistence(timeout: 6) else {
            return XCTFail("A large repository needs search inside the comparison selector")
        }
        let anyText = app.descendants(matching: .any)
        XCTAssertTrue(anyText["当前仓库"].firstMatch.exists)
        search.tap()
        app.typeText("no-matching-ref")
        XCTAssertTrue(anyText["没有匹配的分支"].firstMatch.waitForExistence(timeout: 8))
        app.buttons["清除搜索"].firstMatch.tap()
        app.typeText("ORIGIN/REVIEW")
        let remote = app.buttons["origin/review-base"].firstMatch
        XCTAssertTrue(remote.waitForExistence(timeout: 8))
        XCTAssertTrue(anyText["远程分支"].firstMatch.exists)
        XCTAssertFalse(anyText["本地分支"].firstMatch.exists)
        capture("Search isolates a remote comparison branch", app: app)
        remote.tap()
        XCTAssertTrue(search.waitForNonExistence(timeout: 6))
        let history = app.buttons["history.txt"].firstMatch
        XCTAssertTrue(history.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(base.value as? String, "origin/review-base")
        XCTAssertTrue(scratch.exists)

        target.tap()
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        search.tap()
        app.typeText("review-target")
        let local = app.buttons["review-target"].firstMatch
        XCTAssertTrue(local.waitForExistence(timeout: 8))
        XCTAssertTrue(anyText["本地分支"].firstMatch.exists)
        XCTAssertFalse(anyText["远程分支"].firstMatch.exists)
        capture("Search isolates a local comparison branch", app: app)
        local.tap()
        XCTAssertTrue(search.waitForNonExistence(timeout: 6))
        XCTAssertTrue(scratch.waitForNonExistence(timeout: 8))
        XCTAssertTrue(history.exists)
        XCTAssertEqual(base.value as? String, "origin/review-base")
        XCTAssertEqual(target.value as? String, "review-target")
        capture("The result uses the selected remote base and local target", app: app)

        base.tap()
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        search.tap()
        app.typeText("origin/review-base")
        XCTAssertTrue(remote.waitForExistence(timeout: 8))
        XCTAssertTrue(remote.isSelected)
        app.buttons["关闭分支选择"].firstMatch.tap()
        XCTAssertTrue(search.waitForNonExistence(timeout: 6))
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 6))
        XCTAssertEqual(base.value as? String, "origin/review-base")
        XCTAssertTrue(history.exists)
    }

    func testReviewRefreshReopensTheCurrentDiffAfterRemoteCheckoutChanges() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_REVIEW_RANGE_RECOVERY_FIXTURE"] == "refresh" else {
            throw XCTSkip("Requires the Git fixture that switches the remote checkout")
        }
        let app = openChangesInReviewFixture()
        let scratch = app.buttons["scratch.txt"].firstMatch
        XCTAssertTrue(scratch.waitForExistence(timeout: 10), app.debugDescription)
        app.buttons["选择比较范围"].firstMatch.tap()
        let base = app.buttons["基准"].firstMatch
        XCTAssertTrue(base.waitForExistence(timeout: 6))
        base.tap()
        let branch = app.descendants(matching: .any)["retry-base"].firstMatch
        XCTAssertTrue(branch.waitForExistence(timeout: 8))
        branch.tap()
        XCTAssertTrue(app.buttons["history.txt"].firstMatch.waitForExistence(timeout: 8))
        XCTAssertEqual(base.value as? String, "retry-base")
        scratch.tap()

        let stale = app.staticTexts["工作目录分支已变化，请刷新比较"].firstMatch
        XCTAssertTrue(stale.waitForExistence(timeout: 8), app.debugDescription)
        capture("Open file invalidated by a remote checkout change", app: app)
        let refresh = app.buttons["刷新改动"].firstMatch
        guard refresh.waitForExistence(timeout: 5) else {
            return XCTFail("A stale comparison needs a refresh action on the current file")
        }
        refresh.tap()
        let diff = app.descendants(matching: .any)["文件差异：scratch.txt"].firstMatch
        XCTAssertTrue(diff.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(stale.exists)
        capture("Refresh restored the diff in the current file", app: app)

        refresh.tap()
        let unchanged = app.descendants(matching: .any)["此文件在当前比较中没有差异"].firstMatch
        XCTAssertTrue(unchanged.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(diff.exists)
        capture("The current file is still open when it no longer has a diff", app: app)
        if app.buttons["返回文件列表"].exists {
            app.buttons["返回文件列表"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(app.buttons["new-on-disk.txt"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.buttons["history.txt"].firstMatch.exists)
        XCTAssertFalse(scratch.exists)
        XCTAssertEqual(base.value as? String, "retry-base")
        capture("The updated list uses the same comparison range", app: app)
        app.buttons["刷新改动"].firstMatch.tap()
        XCTAssertTrue(app.buttons["added-from-list.txt"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(base.value as? String, "retry-base")
    }

    func testLegacyWorkerReviewStillShowsWorkingDirectoryChanges() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_REVIEW_RANGE_RECOVERY_FIXTURE"] == "32" else {
            throw XCTSkip("Requires the Git fixture with a legacy protocol handshake")
        }
        let app = openChangesInReviewFixture()
        let scratch = app.buttons["scratch.txt"].firstMatch
        guard scratch.waitForExistence(timeout: 6) else {
            capture("Legacy worker changes failed to load", app: app)
            return XCTFail("A worker on protocol 2.32 still supplies working-directory changes")
        }
        scratch.tap()
        let file = app.descendants(matching: .any)["文件差异：scratch.txt"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 8), app.debugDescription)
        capture("Legacy worker changes and file diff", app: app)
        app.buttons["刷新改动"].firstMatch.tap()
        let unchanged = app.descendants(matching: .any)["此文件没有文本差异"].firstMatch
        XCTAssertTrue(unchanged.waitForExistence(timeout: 8), app.debugDescription)
        if app.buttons["返回文件列表"].exists {
            app.buttons["返回文件列表"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(app.buttons["legacy-new.txt"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
    }

    func testReviewRangeAndCommentStayOnTheirReviewScreens() {
        var app = launch("--preview-board-diff-compare")
        XCTAssertTrue(app.buttons["交换基准与目标"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.navigationBars["改动"].exists)
        app.buttons["交换基准与目标"].tap()
        XCTAssertTrue(app.buttons["internal/transport/connection.go"].exists)
        app.terminate()
        app = launch("--preview-board-diff-annotation")
        XCTAssertTrue(app.buttons["加入当前草稿"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.navigationBars["connection.go"].exists)
        print("SHIOJI-MOTION review begin \(Date().timeIntervalSince1970)")
        app.buttons["取消代码评论"].tap()
        XCTAssertTrue(app.buttons["评论选区"].waitForExistence(timeout: 4))
        XCTAssertTrue(app.navigationBars["connection.go"].exists)
        app.buttons["评论选区"].tap()
        let editor = app.descendants(matching: .any).matching(identifier: "代码选区评论").firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 4))
        editor.tap()
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 4))
        if UIDevice.current.userInterfaceIdiom == .pad {
            app.typeText("Review the selection")
            XCTAssertTrue((editor.value as? String)?.contains("Review the selection") == true)
        } else {
            let keyboardIntro = keyboard.buttons["Continue"].firstMatch
            if keyboardIntro.exists {
                keyboardIntro.tap()
            }
            keyboard.keys["n"].tap()
            keyboard.keys["i"].tap()
            let candidate = app.staticTexts["你"].firstMatch
            XCTAssertTrue(candidate.waitForExistence(timeout: 4))
            candidate.tap()
            XCTAssertTrue((editor.value as? String)?.contains("你") == true)
        }
        app.buttons["加入当前草稿"].tap()
        XCTAssertTrue(app.staticTexts["已加入当前会话草稿"].waitForExistence(timeout: 5))
        print("SHIOJI-MOTION review end \(Date().timeIntervalSince1970)")
    }

    func testHostVerificationShowsEvidenceBeforeTrustInput() {
        let app = launch("--preview-board-verify")
        let trust = app.buttons["信任主机，继续配置 SSH"]
        XCTAssertTrue(trust.waitForExistence(timeout: 8))
        XCTAssertFalse(app.textFields["设备上核实的指纹"].exists)
        trust.tap()
        XCTAssertTrue(app.textFields["设备上核实的指纹"].waitForExistence(timeout: 5))
        app.buttons["连接"].tap()
        XCTAssertTrue(app.staticTexts["请核实设备上的完整 SHA256 指纹"].waitForExistence(timeout: 5))
    }

    func testRemoteWorkerLoginItemUsesOnlyTheSelectedDevice() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_BACKGROUND_FIXTURE"] == "1",
                          "Requires two isolated execution devices and signed runtime helpers")
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        let toggle = app.buttons["登录后启动服务"].firstMatch
        func waitForState(_ enabled: Bool) {
            let state = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                toggle.exists && toggle.isEnabled && toggle.isSelected == enabled
            }, object: nil)
            XCTAssertEqual(XCTWaiter.wait(for: [state], timeout: 12), .completed, app.debugDescription)
        }
        func openWorker(_ name: String) {
            let device = app.buttons[name].firstMatch
            XCTAssertTrue(device.waitForExistence(timeout: 8))
            device.tap()
            let worker = app.buttons["Shioji Worker"].firstMatch
            XCTAssertTrue(worker.waitForExistence(timeout: 8), app.debugDescription)
            worker.tap()
            XCTAssertTrue(app.navigationBars["执行服务"].firstMatch.waitForExistence(timeout: 8))
        }
        app.launch()
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        devices.tap()
        openWorker("构建设备")
        waitForState(false)
        capture("Remote execution service starts with login item off", app: app)
        toggle.tap()
        waitForState(true)
        capture("Owning device confirms login item enabled", app: app)
        toggle.tap()
        waitForState(false)
        app.navigationBars["执行服务"].buttons["BackButton"].firstMatch.tap()
        app.navigationBars["构建设备"].buttons["BackButton"].firstMatch.tap()
        openWorker("主设备")
        waitForState(true)
        capture("Another device keeps its separate login item", app: app)
    }

    func testWorkerUpgradeCanWaitCancelAndResumeFromItsReceipt() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-worker-upgrade")
        let upgrade = app.buttons["准备并升级"]
        XCTAssertTrue(upgrade.waitForExistence(timeout: 8))
        capture("Worker update available", app: app)
        upgrade.tap()
        let cancel = app.buttons["取消升级"]
        XCTAssertTrue(cancel.waitForExistence(timeout: 5))
        let status = app.buttons["检查升级状态"]
        XCTAssertTrue(status.exists)
        capture("Worker update waiting", app: app)
        cancel.tap()
        XCTAssertTrue(upgrade.waitForExistence(timeout: 5))
        upgrade.tap()
        XCTAssertTrue(status.waitForExistence(timeout: 5))
        status.tap()
        XCTAssertTrue(app.buttons["配置远程 Agent"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["升级已完成"].exists)
        capture("Worker update completed", app: app)
    }

    func testWorkerUpgradeConfirmsStoppingAndMigratingWithSystemAlerts() {
        let app = launch("--preview-board-worker-upgrade")
        XCTAssertTrue(app.buttons["准备并升级"].waitForExistence(timeout: 8))
        app.buttons["准备并升级"].tap()
        let stop = app.buttons["停止任务并升级"]
        XCTAssertTrue(stop.waitForExistence(timeout: 5))
        stop.tap()
        let stopAlert = app.alerts["停止任务并升级？"]
        XCTAssertTrue(stopAlert.waitForExistence(timeout: 5))
        stopAlert.buttons["取消"].tap()
        XCTAssertTrue(app.staticTexts["等待空闲"].exists)
        stop.tap()
        XCTAssertTrue(stopAlert.waitForExistence(timeout: 5))
        stopAlert.buttons["停止任务并升级"].tap()
        XCTAssertTrue(app.staticTexts["升级已完成"].waitForExistence(timeout: 5))
        app.terminate()

        let migrate = launch("--preview-board-worker-schema-upgrade")
        XCTAssertTrue(migrate.buttons["准备并升级"].waitForExistence(timeout: 8))
        migrate.buttons["准备并升级"].tap()
        let migrateAlert = migrate.alerts["备份并迁移设备数据库？"]
        XCTAssertTrue(migrateAlert.waitForExistence(timeout: 5))
        capture("Worker migration confirmation", app: migrate)
        migrateAlert.buttons["备份并迁移"].tap()
        XCTAssertTrue(migrate.staticTexts["等待空闲"].waitForExistence(timeout: 5))
        XCTAssertFalse(migrate.buttons["停止任务并升级"].exists)
    }

    func testNewDeviceInstallsOnlyAfterSSHCredentialsAreVerified() {
        let app = launch("--preview-board-verify")
        let trust = app.buttons["信任主机，继续配置 SSH"]
        XCTAssertTrue(trust.waitForExistence(timeout: 8))
        trust.tap()
        let fingerprint = app.textFields["设备上核实的指纹"]
        XCTAssertTrue(fingerprint.waitForExistence(timeout: 5))
        fingerprint.tap()
        app.typeText("SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8")
        if UIDevice.current.userInterfaceIdiom == .pad {
            app.typeText("\n")
        } else {
            app.buttons["连接"].tap()
        }
        let verify = app.buttons["验证 SSH 登录"]
        XCTAssertTrue(verify.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["安装执行服务"].exists)
        verify.tap()
        let install = app.buttons["安装执行服务"]
        XCTAssertTrue(install.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["配置远程 Agent"].exists)
        capture("Missing worker after SSH verification", app: app)
        install.tap()
        XCTAssertTrue(app.buttons["配置远程 Agent"].waitForExistence(timeout: 5))
        capture("Installed worker is ready", app: app)
    }

    func testLegacyServiceMigrationReconnectsToTheOriginalDevice() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_LEGACY_MIGRATION_FIXTURE"] == "1")
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        let worker = app.buttons["Shioji Worker"].firstMatch
        let env = ProcessInfo.processInfo.environment
        if let port = env["SHIOJI_LEGACY_SSH_PORT"], let user = env["SHIOJI_LEGACY_SSH_USER"], let fingerprint = env["SHIOJI_LEGACY_SSH_FINGERPRINT"] {
            let add = app.buttons["添加设备"].firstMatch
            XCTAssertTrue(add.waitForExistence(timeout: 8))
            add.tap()
            let host = app.textFields["主机"].firstMatch
            XCTAssertTrue(host.waitForExistence(timeout: 6))
            host.tap()
            app.typeText("127.0.0.1")
            let portField = app.textFields["端口"].firstMatch
            portField.tap()
            app.typeKey("a", modifierFlags: .command)
            app.typeText(port)
            XCTAssertEqual(portField.value as? String, port)
            app.textFields["用户名"].firstMatch.tap()
            app.typeText(user + "\n")
            let trust = app.buttons["信任主机，继续配置 SSH"].firstMatch
            XCTAssertTrue(trust.waitForExistence(timeout: 12))
            trust.tap()
            let field = app.textFields["设备上核实的指纹"].firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 6))
            field.tap()
            app.typeText(fingerprint + "\n")
            let verify = app.buttons["验证 SSH 登录"].firstMatch
            XCTAssertTrue(verify.waitForExistence(timeout: 8))
            verify.tap()
        } else {
            let device = app.buttons["旧版迁移验收设备"].firstMatch
            XCTAssertTrue(device.waitForExistence(timeout: 8))
            device.tap()
            XCTAssertTrue(worker.waitForExistence(timeout: 8))
            worker.tap()
            let check = app.buttons["检查连接"].firstMatch
            XCTAssertTrue(check.waitForExistence(timeout: 8))
            check.tap()
        }
        let inspect = app.buttons["检查旧版服务"].firstMatch
        capture("Legacy SSH endpoint checked", app: app)
        XCTAssertTrue(inspect.waitForExistence(timeout: 12), "Legacy service should offer the migration flow")
        inspect.tap()
        let migrate = app.buttons["停止旧版并迁移"].firstMatch
        XCTAssertTrue(migrate.waitForExistence(timeout: 30))
        migrate.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 6))
        capture("Confirming migration of the verified legacy worker", app: app)
        alert.buttons["取消"].tap()
        XCTAssertTrue(migrate.waitForExistence(timeout: 5))
        XCTAssertTrue(migrate.isEnabled)
        migrate.tap()
        XCTAssertTrue(alert.waitForExistence(timeout: 6))
        alert.buttons["开始迁移"].tap()
        app.terminate()

        app.launch()
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        let receipt = app.buttons["执行服务迁移"].firstMatch
        XCTAssertTrue(receipt.waitForExistence(timeout: 8), "The migration must keep a restartable receipt")
        receipt.tap()
        let connect = app.buttons["连接设备"].firstMatch
        let recheck = app.buttons["重新检查"].firstMatch
        let recovered = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            connect.exists || recheck.exists
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [recovered], timeout: 35), .completed, app.debugDescription)
        if recheck.exists {
            // The app may close before the SSH start is accepted. The same
            // receipt is queried, then a fresh start needs explicit approval.
            XCTAssertTrue(app.staticTexts["迁移状态待核对"].firstMatch.exists)
            recheck.tap()
            XCTAssertTrue(migrate.waitForExistence(timeout: 12))
            migrate.tap()
            XCTAssertTrue(alert.waitForExistence(timeout: 6))
            alert.buttons["开始迁移"].tap()
        }
        XCTAssertTrue(connect.waitForExistence(timeout: 35), app.debugDescription)
        XCTAssertTrue(app.staticTexts["迁移已完成"].firstMatch.exists)
        capture("The original device is verified after relaunch", app: app)
        connect.tap()
        if env["SHIOJI_LEGACY_SSH_PORT"] != nil {
            XCTAssertTrue(app.navigationBars["远程 Agent"].firstMatch.waitForExistence(timeout: 12))
            app.navigationBars["远程 Agent"].buttons["BackButton"].firstMatch.tap()
        } else {
            XCTAssertTrue(worker.waitForExistence(timeout: 12))
            app.navigationBars["旧版迁移验收设备"].buttons["BackButton"].firstMatch.tap()
        }
        let sessions = app.buttons["会话"].firstMatch
        XCTAssertTrue(sessions.waitForExistence(timeout: 8))
        sessions.tap()
        let preserved = app.buttons["保留旧版会话"].firstMatch
        XCTAssertTrue(preserved.waitForExistence(timeout: 15), app.debugDescription)
        preserved.tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Legacy reply retained.")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12))
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "Unsent draft retained.")
        capture("Reading the original conversation on the migrated worker", app: app)
    }

    func testNotificationSettingsUseSystemPermissionAndSendATest() throws {
        let decision = ProcessInfo.processInfo.environment["SHIOJI_MOBILE_NOTIFICATION_AUTH_TEST"]
        try XCTSkipUnless(decision == "allow" || decision == "deny", "Requires an isolated simulator's notification permission")
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        let app = launch("")
        let settings = app.buttons["设置"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        settings.tap()
        let entry = app.buttons["通知偏好"].firstMatch
        XCTAssertTrue(entry.waitForExistence(timeout: 8))
        entry.tap()
        let permission = app.buttons["允许通知"].firstMatch
        XCTAssertTrue(permission.waitForExistence(timeout: 8))
        capture("Notification settings before authorization", app: app)
        let testNotice = app.buttons["发送测试通知"].firstMatch
        let systemSettings = app.buttons["打开系统通知设置"].firstMatch
        XCTAssertTrue(testNotice.exists)
        XCTAssertFalse(testNotice.isEnabled)
        XCTAssertTrue(systemSettings.isEnabled)
        let denied = app.descendants(matching: .any)["请在系统设置中允许 Shioji 通知"].firstMatch
        if decision == "allow" || !denied.exists {
            permission.tap()
            let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
            let titles = decision == "allow" ? ["Allow", "允许"] : ["Don’t Allow", "Don't Allow", "不允许"]
            let choice = springboard.alerts.buttons.matching(NSPredicate(format: "label IN %@", titles)).firstMatch
            XCTAssertTrue(choice.waitForExistence(timeout: 8))
            let promptDismissed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in !choice.exists }, object: nil)
            promptDismissed.isInverted = true
            XCTAssertEqual(XCTWaiter.wait(for: [promptDismissed], timeout: 6), .completed)
            choice.tap()
        }

        if decision == "deny" {
            XCTAssertTrue(denied.waitForExistence(timeout: 8))
            XCTAssertFalse(testNotice.isEnabled)
            capture("Notifications are denied by the system", app: app)
            systemSettings.tap()
            let system = XCUIApplication(bundleIdentifier: "com.apple.Preferences")
            XCTAssertTrue(system.wait(for: .runningForeground, timeout: 10))
            let systemPermission = system.switches.matching(NSPredicate(format: "label IN %@", ["Allow Notifications", "允许通知"])).firstMatch
            if !systemPermission.waitForExistence(timeout: 4) {
                // Some iPad simulator builds handle the URL by opening the Settings sidebar.
                let categories = system.collectionViews["com.apple.settings.sidebar.collectionView"].firstMatch
                let apps = categories.buttons["com.apple.settings.apps"].firstMatch
                XCTAssertTrue(categories.exists, system.debugDescription)
                for _ in 0..<4 where !apps.isHittable { categories.swipeUp() }
                XCTAssertTrue(apps.isHittable, system.debugDescription)
                apps.tap()
                let shioji = system.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Shioji")).firstMatch
                let bottom = system.coordinate(withNormalizedOffset: CGVector(dx: 0.72, dy: 0.76))
                let middle = system.coordinate(withNormalizedOffset: CGVector(dx: 0.72, dy: 0.43))
                for _ in 0..<6 where !shioji.isHittable { bottom.press(forDuration: 0.03, thenDragTo: middle) }
                XCTAssertTrue(shioji.isHittable, system.debugDescription)
                shioji.tap()
                let notifications = system.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@", "Notifications", "通知")).firstMatch
                XCTAssertTrue(notifications.waitForExistence(timeout: 8), system.debugDescription)
                notifications.tap()
            }
            XCTAssertTrue(systemPermission.waitForExistence(timeout: 8), system.debugDescription)
            capture("Shioji in iPad system notification settings", app: system)
            XCTAssertEqual(systemPermission.value as? String, "0")
            systemPermission.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
            let grantedInSettings = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                systemPermission.value as? String == "1"
            }, object: nil)
            XCTAssertEqual(XCTWaiter.wait(for: [grantedInSettings], timeout: 5), .completed)
            app.activate()
            XCTAssertTrue(app.navigationBars["通知"].waitForExistence(timeout: 8))
            XCTAssertTrue(denied.waitForNonExistence(timeout: 8))
            XCTAssertFalse(testNotice.isEnabled)
            permission.tap()
        }
        let available = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in testNotice.isEnabled }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [available], timeout: 6), .completed)
        let foreground = app.buttons["前台也通知"].firstMatch
        XCTAssertTrue(foreground.isSelected)
        foreground.tap()
        XCTAssertFalse(foreground.isSelected)
        testNotice.tap()
        XCTAssertTrue(app.descendants(matching: .any)["测试通知已发送"].firstMatch.waitForExistence(timeout: 8))
        capture("System accepted the test notification", app: app)
        let sound = app.buttons["提示音"].firstMatch
        XCTAssertTrue(sound.exists)
        let originalSound = sound.isSelected
        sound.tap()
        XCTAssertNotEqual(sound.isSelected, originalSound)
        permission.tap()
        XCTAssertFalse(testNotice.isEnabled)
    }

    func testOlderSafeWorkerFinishesPreparationBeforeAgentSetup() throws {
        let env = ProcessInfo.processInfo.environment
        try XCTSkipUnless(env["SHIOJI_SERVICE_PREPARATION_FIXTURE"] == "1")
        let port = try XCTUnwrap(env["SHIOJI_LEGACY_SSH_PORT"])
        let user = try XCTUnwrap(env["SHIOJI_LEGACY_SSH_USER"])
        let fingerprint = try XCTUnwrap(env["SHIOJI_LEGACY_SSH_FINGERPRINT"])
        XCUIDevice.shared.orientation = UIDevice.current.userInterfaceIdiom == .pad ? .landscapeLeft : .portrait
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        let add = app.buttons["添加设备"].firstMatch
        XCTAssertTrue(add.waitForExistence(timeout: 8))
        add.tap()
        let host = app.textFields["主机"].firstMatch
        XCTAssertTrue(host.waitForExistence(timeout: 6))
        host.tap()
        app.typeText("127.0.0.1")
        let portField = app.textFields["端口"].firstMatch
        let defaultPort = try XCTUnwrap(portField.value as? String)
        portField.coordinate(withNormalizedOffset: CGVector(dx: 0.99, dy: 0.5)).tap()
        app.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: defaultPort.count) + port)
        XCTAssertEqual(portField.value as? String, port)
        app.textFields["用户名"].firstMatch.tap()
        app.typeText(user + "\n")
        let trust = app.buttons["信任主机，继续配置 SSH"].firstMatch
        XCTAssertTrue(trust.waitForExistence(timeout: 12))
        trust.tap()
        let field = app.textFields["设备上核实的指纹"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 6))
        field.tap()
        app.typeText(fingerprint + "\n")
        let verify = app.buttons["验证 SSH 登录"].firstMatch
        XCTAssertTrue(verify.waitForExistence(timeout: 8))
        verify.tap()
        let prepare = app.buttons["准备更新"].firstMatch
        let configure = app.buttons["配置远程 Agent"].firstMatch
        let recognized = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            prepare.exists || configure.exists
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [recognized], timeout: 12), .completed, app.debugDescription)
        capture("Older safe service before preparation", app: app)
        XCTAssertTrue(prepare.exists, "A service without Agent configuration must be prepared first")
        XCTAssertFalse(configure.exists)
        prepare.tap()
        if env["SHIOJI_PREPARATION_BUSY"] == "1" {
            XCTAssertTrue(app.staticTexts["等待设备空闲"].firstMatch.waitForExistence(timeout: 15))
            XCTAssertFalse(configure.exists)
            capture("Waiting for the owning worker to finish its work", app: app)
            app.terminate()
            app.launch()
            XCTAssertTrue(devices.waitForExistence(timeout: 8))
            devices.tap()
            let receipt = app.buttons["执行服务准备"].firstMatch
            XCTAssertTrue(receipt.waitForExistence(timeout: 8))
            receipt.tap()
        }
        XCTAssertTrue(configure.waitForExistence(timeout: 45), app.debugDescription)
        capture("Agent configuration is available after protocol verification", app: app)
        configure.tap()
        let agents = app.navigationBars["远程 Agent"].firstMatch
        XCTAssertTrue(agents.waitForExistence(timeout: 12))
        agents.buttons["BackButton"].firstMatch.tap()
        let conversations = app.buttons["会话"].firstMatch
        XCTAssertTrue(conversations.waitForExistence(timeout: 8))
        conversations.tap()
        let conversation = app.buttons["保留旧版会话"].firstMatch
        XCTAssertTrue(conversation.waitForExistence(timeout: 15))
        conversation.tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Legacy reply retained.")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12))
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "Unsent draft retained.")
        capture("Opening the original conversation after service preparation", app: app)
    }

    func testCommandApprovalStaysInConversation() {
        let app = launch("--preview-board-approval")
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "命令审批").firstMatch.waitForExistence(timeout: 8))
        capture("Command approval state", app: app)
        XCTAssertTrue(app.buttons["会话活动"].exists)
        let disclosure = app.buttons["命令审批"]
        let command = app.staticTexts["go test ./internal/transport -run TestReconnect"]
        XCTAssertTrue(command.exists)
        XCTAssertTrue(app.buttons["允许这一次"].exists)
        disclosure.tap()
        XCTAssertTrue(command.waitForNonExistence(timeout: 4))
        disclosure.tap()
        XCTAssertTrue(command.waitForExistence(timeout: 4))
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "消息").firstMatch.exists)
    }

    func testAttentionOpensQuestionInOwningConversation() {
        let app = launch("--preview-board-attention")
        let question = app.buttons["回答问题"].firstMatch
        XCTAssertTrue(question.waitForExistence(timeout: 8))
        question.tap()
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["提交答案"].exists)
        XCTAssertFalse(app.navigationBars["回答问题"].exists)
    }

    func testAttentionShortcutOpensRequestAheadOfUnreadReply() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-attention-shortcut")
        let unread = app.buttons["整理键盘交互和阅读位置"].firstMatch
        XCTAssertTrue(unread.waitForExistence(timeout: 8))
        app.typeKey("j", modifierFlags: [.command, .shift])
        XCTAssertTrue(app.buttons["提交答案"].waitForExistence(timeout: 6), "Command-Shift-J reveals an actionable question in its session")
        XCTAssertFalse(app.navigationBars["待我处理"].exists)
        XCTAssertFalse(app.navigationBars["回答问题"].exists)
    }

    func testAttentionShortcutShowsUnreadReply() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-attention-shortcut-unread")
        let unread = app.buttons["整理键盘交互和阅读位置"].firstMatch
        XCTAssertTrue(unread.waitForExistence(timeout: 8))
        app.typeKey("j", modifierFlags: [.command, .shift])
        XCTAssertTrue(app.staticTexts["输入与阅读位置已校对。"].waitForExistence(timeout: 6))
        XCTAssertFalse(app.navigationBars["待我处理"].exists)
    }

    func testAttentionShortcutKeepsCurrentDraftWhenEmpty() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-session")
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 8))
        draft.tap()
        app.typeText("Keep this draft")
        XCTAssertEqual(draft.value as? String, "Keep this draft")
        app.typeKey("j", modifierFlags: [.command, .shift])
        let empty = app.staticTexts["当前没有待处理事项"].firstMatch
        XCTAssertTrue(empty.waitForExistence(timeout: 5))
        XCTAssertEqual(draft.value as? String, "Keep this draft")
        XCTAssertTrue(app.buttons["会话活动"].exists)
        XCTAssertTrue(empty.waitForNonExistence(timeout: 7))
    }

    func testAttentionShortcutPreservesNewSessionDraftWhileOpeningQuestion() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-attention-shortcut-draft")
        let draftScreen = app.navigationBars["新建会话"]
        XCTAssertTrue(draftScreen.waitForExistence(timeout: 8))
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 5))
        editor.tap()
        app.typeText("Keep the new session draft")
        XCTAssertEqual(editor.value as? String, "Keep the new session draft")
        app.typeKey("j", modifierFlags: [.command, .shift])
        XCTAssertTrue(app.buttons["提交答案"].waitForExistence(timeout: 8))
        XCTAssertFalse(draftScreen.exists)
        app.typeKey("n", modifierFlags: .command)
        XCTAssertTrue(draftScreen.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "Keep the new session draft")
    }

    func testAttentionShortcutOpensRemoteRequestDirectly() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_ATTENTION_SHORTCUT_FIXTURE"] == "1" else {
            throw XCTSkip("Requires an isolated Worker with a remote-only request")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let current = app.buttons["当前设备的会话"].firstMatch
        XCTAssertTrue(current.waitForExistence(timeout: 15), app.debugDescription)
        current.tap()
        XCTAssertTrue(app.buttons["会话活动"].firstMatch.waitForExistence(timeout: 10))
        app.typeKey("j", modifierFlags: [.command, .shift])
        let decline = app.buttons["拒绝远端请求"].firstMatch
        XCTAssertTrue(decline.waitForExistence(timeout: 22), app.debugDescription)
        XCTAssertFalse(app.navigationBars["待我处理"].exists)
        decline.tap()
        XCTAssertTrue(app.staticTexts["远端 Worker 已记录拒绝"].firstMatch.waitForExistence(timeout: 9))
    }

    func testAttentionOpensAndAnswersOnlyTheOwningDevicesRequest() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_ATTENTION_FIXTURE"] == "1" else {
            throw XCTSkip("Requires two isolated Workers with separate requests")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        XCTAssertTrue(app.buttons["当前设备的会话"].firstMatch.waitForExistence(timeout: 20), app.debugDescription)
        app.buttons["会话视图"].firstMatch.tap()
        // The session list can also have a collapsible group named 待我处理.
        let menu = app.otherElements.matching(NSPredicate(format: "label == %@", "待我处理")).firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 6))
        menu.tap()
        let attention = app.navigationBars["待我处理"]
        XCTAssertTrue(attention.waitForExistence(timeout: 8))
        let local = app.buttons["当前设备的确认"].firstMatch
        XCTAssertTrue(local.waitForExistence(timeout: 8), app.debugDescription)
        capture("Attention shows the current worker", app: app)
        let remote = app.buttons["远端设备的确认"].firstMatch
        XCTAssertTrue(remote.waitForExistence(timeout: 13), "Attention must also show a request from another enrolled Worker")
        let partial = app.descendants(matching: .any).matching(
            NSPredicate(format: "label == %@", "部分设备暂不可用，列表可能不完整")
        ).firstMatch
        XCTAssertTrue(partial.waitForExistence(timeout: 15))
        capture("Attention also lists the remote worker", app: app)
        remote.tap()
        let decline = app.buttons["拒绝远端请求"].firstMatch
        XCTAssertTrue(decline.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertFalse(app.buttons["拒绝当前请求"].exists)
        XCTAssertFalse(app.navigationBars["远端设备的确认"].exists)
        capture("Remote approval is actionable in its own conversation", app: app)
        decline.tap()
        XCTAssertTrue(app.staticTexts["远端 Worker 已记录拒绝"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        capture("The owning worker's receipt appears in the conversation", app: app)
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(attention.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(local.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(remote.exists)
        capture("The other device still has its own pending request", app: app)
        let refresh = attention.buttons["刷新待处理请求"].firstMatch
        XCTAssertTrue(refresh.isEnabled)
        refresh.tap()
        XCTAssertTrue(partial.waitForExistence(timeout: 10))
        XCTAssertTrue(local.exists)
        XCTAssertFalse(remote.exists)
    }

    func testAttentionReadsOnlyTheViewedWorkersNewReply() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_COMPLETION_ATTENTION_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the isolated completion attention Workers")
        }
        let control = try replyFixture()
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        XCTAssertTrue(app.buttons["当前设备的校对"].firstMatch.waitForExistence(timeout: 20), app.debugDescription)
        wait(for: [publishReplyFixture("complete", at: control)], timeout: 8)

        app.buttons["会话视图"].firstMatch.tap()
        let menu = app.otherElements.matching(NSPredicate(format: "label == %@", "待我处理")).firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 6))
        menu.tap()
        let attention = app.navigationBars["待我处理"]
        XCTAssertTrue(attention.waitForExistence(timeout: 8))
        let empty = app.staticTexts["当前没有待处理请求"].firstMatch
        if empty.waitForExistence(timeout: 2) {
            capture("Completed replies were missing from attention", app: app)
        }

        let local = app.buttons["查看未读回复：当前设备的校对"].firstMatch
        let remote = app.buttons["查看未读回复：远端设备的校对"].firstMatch
        XCTAssertTrue(remote.waitForExistence(timeout: 13), "A reply completed since launch must appear in Attention")
        XCTAssertTrue(local.waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["查看未读回复：启动前的校对"].exists)
        capture("Both new replies are listed across devices", app: app)
        remote.tap()
        XCTAssertTrue(app.staticTexts["远端校对完成，三项记录均已确认。"].firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        capture("The selected reply is visible in the owning session", app: app)
        wait(for: [publishReplyFixture("assert-read", at: control)], timeout: 8)
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(attention.waitForExistence(timeout: 8))
        XCTAssertTrue(local.waitForExistence(timeout: 8))
        XCTAssertFalse(remote.exists)
        capture("Reading one reply leaves the other device unread", app: app)
    }

    func testConversationActivityKeepsWorkDetailsOffTranscript() {
        let app = launch("--preview-session")
        let more = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 8))
        capture("Conversation before opening activity", app: app)
        XCTAssertFalse(app.buttons["计划与任务"].exists)
        XCTAssertFalse(app.buttons["子 Agent"].exists)
        XCTAssertFalse(app.buttons["后台任务"].exists)
        more.tap()
        let plan = app.buttons["计划与任务"].firstMatch
        XCTAssertTrue(plan.waitForExistence(timeout: 5))
        plan.tap()
        XCTAssertTrue(app.staticTexts["核对原生动画"].waitForExistence(timeout: 5))
        capture("Native plan", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        print("SHIOJI-MOTION child begin \(Date().timeIntervalSince1970)")
        app.buttons["检查 iPad 分栏"].firstMatch.tap()
        let child = app.navigationBars["检查 iPad 分栏"]
        XCTAssertTrue(child.waitForExistence(timeout: 5))
        capture("Native child agent", app: app)
        child.buttons["BackButton"].tap()
        let activity = app.navigationBars["会话活动"]
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
        app.buttons["编译模拟器构建"].firstMatch.tap()
        let background = app.navigationBars["编译模拟器构建"]
        XCTAssertTrue(background.waitForExistence(timeout: 5))
        background.buttons["BackButton"].tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
        activity.buttons["BackButton"].tap()
        XCTAssertTrue(more.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["关闭子 Agent"].exists)
        XCTAssertFalse(app.staticTexts["输出"].exists)
        XCTAssertFalse(app.staticTexts["检查 iPad 分栏"].exists)
        XCTAssertFalse(app.staticTexts["编译模拟器构建"].exists)
        print("SHIOJI-MOTION child end \(Date().timeIntervalSince1970)")
    }

    func testChildRequestSourceOpensTheMatchingInlineFileApproval() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_CHILD_APPROVAL_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the isolated child approval worker fixture")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        let parent = app.buttons["子 Agent 审批验收"].firstMatch
        XCTAssertTrue(parent.waitForExistence(timeout: 16))
        parent.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["允许修改这些文件？"].exists)
        activity.tap()
        app.buttons["审查子任务补丁"].firstMatch.tap()
        let child = app.navigationBars["审查子任务补丁"]
        XCTAssertTrue(child.waitForExistence(timeout: 8))
        let question = app.buttons["回答问题"].firstMatch
        XCTAssertTrue(question.waitForExistence(timeout: 8))
        question.tap()
        XCTAssertTrue(app.buttons["提交答案"].waitForNonExistence(timeout: 5))

        let title = "允许修改这些文件？"
        let source = app.buttons.matching(NSPredicate(format: "label == %@ AND value == %@", "查看请求来源", title)).firstMatch
        let plainSource = app.staticTexts[title].firstMatch
        for _ in 0..<8 where !source.exists && !plainSource.exists { app.swipeDown(velocity: .fast) }
        let canOpen = source.waitForExistence(timeout: 4)
        capture(canOpen ? "Child request source can open its approval" : "Child request source is plain text", app: app)
        XCTAssertTrue(canOpen)
        source.tap()

        let patch = app.buttons["child-proposal.go"].firstMatch
        let revealedPatch = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in patch.isHittable }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [revealedPatch], timeout: 8), .completed)
        XCTAssertFalse(app.buttons["parent-other.go"].exists)
        XCTAssertTrue(child.exists)
        patch.tap()
        let diff = app.descendants(matching: .any).matching(identifier: "文件补丁：Sources/child-proposal.go").firstMatch
        XCTAssertTrue(diff.waitForExistence(timeout: 5))
        capture("Inline preview uses the child proposal", app: app)
        patch.tap()

        let decline = app.buttons["拒绝本次修改"].firstMatch
        if !decline.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(decline.isHittable)
        decline.tap()
        let result = app.staticTexts["本次文件修改已拒绝"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 8))
        XCTAssertFalse(decline.exists)
        XCTAssertTrue(question.exists)
        capture("The child receipt leaves the other request pending", app: app)
        let handled = app.staticTexts["允许修改这些文件？ · 已处理"].firstMatch
        for _ in 0..<8 where !handled.exists { app.swipeDown(velocity: .fast) }
        XCTAssertTrue(handled.waitForExistence(timeout: 5))
        XCTAssertFalse(source.exists)
        child.buttons["BackButton"].tap()
        app.navigationBars["会话活动"].buttons["BackButton"].tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
        let draft = app.descendants(matching: .any).matching(identifier: "消息").firstMatch
        XCTAssertEqual(draft.value as? String, "保留主会话草稿")
        XCTAssertFalse(result.exists)
    }

    private func replyFixture() throws -> URL {
        guard let address = ProcessInfo.processInfo.environment["SHIOJI_REPLY_FIXTURE"],
              let control = URL(string: address) else {
            throw XCTSkip("Requires the isolated streaming reply worker fixture")
        }
        return control
    }

    private func openReplyFixture() -> XCUIApplication {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let conversation = app.buttons["实时回复验收"].firstMatch
        XCTAssertTrue(conversation.waitForExistence(timeout: 16))
        if !app.buttons["会话活动"].firstMatch.exists { conversation.tap() }
        return app
    }

    private func publishReplyFixture(_ step: String, at control: URL) -> XCTestExpectation {
        let event = expectation(description: step)
        var request = URLRequest(url: control.appendingPathComponent(step))
        request.httpMethod = "POST"
        URLSession.shared.dataTask(with: request) { _, response, _ in
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
            event.fulfill()
        }.resume()
        return event
    }

    func testLiveReplyRevealsOnlyNewMarkdown() throws {
        let control = try replyFixture()
        let reduceMotion = ProcessInfo.processInfo.environment["SHIOJI_REPLY_REDUCE_MOTION"] == "1"
        let app = openReplyFixture()
        let prefix = app.staticTexts["已显示的回复前缀。"].firstMatch
        XCTAssertTrue(prefix.waitForExistence(timeout: 8), app.debugDescription)
        capture("The existing reply prefix", app: app)

        func assertProgressiveUpdate(_ step: String, opening: String, ending: String) {
            let body = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", opening)).firstMatch
            let published = publishReplyFixture(step, at: control)
            var middle = false
            let deadline = Date().addingTimeInterval(5)
            while Date() < deadline {
                guard body.exists else { continue }
                if body.label.contains(ending) { break }
                if !middle { capture("An intermediate Markdown prefix - " + step, app: app) }
                middle = true
            }
            wait(for: [published], timeout: 4)
            XCTAssertTrue(body.exists && body.label.contains(ending))
            capture(middle ? "New text after its progressive reveal - " + step : "New text appeared in a single update - " + step, app: app)
            XCTAssertEqual(middle, !reduceMotion, "Unexpected pacing for " + step)
        }
        assertProgressiveUpdate("append", opening: "新增回复", ending: "流式结束。")
        XCTAssertTrue(prefix.exists)

        wait(for: [publishReplyFixture("finish", at: control)], timeout: 4)
        let completed = app.staticTexts["保留完成事件的尾句。"].firstMatch
        XCTAssertTrue(completed.waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["复制回复"].firstMatch.waitForExistence(timeout: 4))
        capture("The final reply and its footer", app: app)

        assertProgressiveUpdate("new", opening: "新到的完整回复", ending: "新回复结尾。")
    }

    func testReplyMarkdownAnchorScrollsToItsHeading() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_ANCHOR_FIXTURE"] == "1" else {
            throw XCTSkip("Requires an isolated Worker with a long Markdown reply")
        }
        let app = openReplyFixture()
        let link = app.links["跳到处理结果"].firstMatch
        let heading = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Target section")).firstMatch
        let sectionText = app.staticTexts["此处是工具检查完成后的结果。"].firstMatch
        func inReadingArea(_ element: XCUIElement) -> Bool {
            element.exists && element.frame.midY > app.frame.minY + 130 && element.frame.midY < app.frame.maxY - 130
        }
        func revealLink() {
            for _ in 0..<10 where !inReadingArea(link) {
                let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.67, dy: 0.23))
                let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.67, dy: 0.72))
                from.press(forDuration: 0.04, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.18)
            }
            XCTAssertTrue(inReadingArea(link) && link.isHittable)
            XCTAssertFalse(inReadingArea(heading))
        }
        for attempt in 1...2 {
            revealLink()
            link.tap()
            let atHeading = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                inReadingArea(heading) && inReadingArea(sectionText)
            }, object: nil)
            let reached = XCTWaiter.wait(for: [atHeading], timeout: 5) == .completed
            capture(reached ? "Heading reached on activation \(attempt)" : "The internal link left the heading outside the reading area", app: app)
            XCTAssertTrue(reached, "A reply’s internal Markdown link must bring its heading and opening paragraph into view. " + app.debugDescription)
        }
    }

    func testToolFileSnapshotOpensTheNativeReader() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_TOOL_SNAPSHOT_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the owning Worker with a saved tool file snapshot")
        }
        let app = openReplyFixture()
        let group = app.buttons["1 次工具调用"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 10))
        group.tap()
        let tool = app.buttons["生成报告文件"].firstMatch
        XCTAssertTrue(tool.waitForExistence(timeout: 6))
        tool.tap()
        let file = app.buttons["预览 audit_snapshot.go"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 6))
        file.tap()
        let save = app.buttons["另存为…"].firstMatch
        XCTAssertTrue(save.waitForExistence(timeout: 10))
        let reader = app.descendants(matching: .any).matching(identifier: "文件内容：audit_snapshot.go").firstMatch
        capture("Tool snapshot preview after download", app: app)
        XCTAssertTrue(reader.exists, "A saved tool file should use the selectable, syntax-aware native reader")
        XCTAssertEqual(reader.value as? String,
                       "package audit\n\n// Saved result from the owning Worker.\nconst Preserved = \"snapshot-source-42\"\n")
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCTAssertTrue(app.buttons["实时回复验收"].firstMatch.exists)
            app.buttons["关闭文件预览"].firstMatch.tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(file.waitForExistence(timeout: 6))
    }

    func testToolResultUpdatesKeepTheSurvivingSnapshot() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_TOOL_RESULTS_FIXTURE"] == "1" else {
            throw XCTSkip("Requires an isolated Worker with staged tool results")
        }
        let control = try replyFixture()
        continueAfterFailure = false
        let app = openReplyFixture()
        let group = app.buttons["1 次工具调用"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 10))
        group.tap()
        let tool = app.buttons["结果更新"].firstMatch
        XCTAssertTrue(tool.waitForExistence(timeout: 6))
        tool.tap()
        let first = app.buttons["预览 initial.txt"]
        let retained = app.buttons["预览 retained.txt"]
        let following = app.staticTexts["NEXT REPLY ANCHOR"]
        XCTAssertTrue(first.waitForExistence(timeout: 6))
        XCTAssertTrue(retained.exists)
        XCTAssertTrue(following.exists)
        capture("Initial tool results", app: app)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 1))

        wait(for: [publishReplyFixture("add", at: control)], timeout: 4)
        XCTAssertTrue(app.staticTexts["新增结构化说明"].waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["解析 Markdown 结果"].exists)
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "文件补丁：result.go").firstMatch.exists)
        XCTAssertTrue(app.staticTexts["code: ready"].exists)
        capture("Tool result includes the new blocks", app: app)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 1.5))

        wait(for: [publishReplyFixture("remove", at: control)], timeout: 4)
        XCTAssertTrue(first.waitForNonExistence(timeout: 6))
        XCTAssertTrue(retained.exists)
        XCTAssertTrue(following.exists)
        capture("Only the surviving tool result remains", app: app)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 1))
        retained.tap()
        let reader = app.descendants(matching: .any).matching(identifier: "文件内容：retained.txt").firstMatch
        XCTAssertTrue(reader.waitForExistence(timeout: 10))
        XCTAssertEqual(reader.value as? String, "retained snapshot\n")
        capture("Surviving file still opens its own snapshot", app: app)
    }

    func testCompletionFooterFollowsTheLastReplyText() throws {
        let control = try replyFixture()
        let reduceMotion = ProcessInfo.processInfo.environment["SHIOJI_REPLY_REDUCE_MOTION"] == "1"
        let app = openReplyFixture()
        XCTAssertTrue(app.staticTexts["已显示的回复前缀。"].firstMatch.waitForExistence(timeout: 8))
        let ending = "完整的末句。"
        let body = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "完成时收到的末段")).firstMatch
        let receipt = app.staticTexts["用时 4分13秒"].firstMatch
        let previousReceipt = app.buttons["复制回复"].firstMatch
        XCTAssertTrue(previousReceipt.exists)
        capture("The running reply before completion", app: app)

        let published = publishReplyFixture("complete-reply", at: control)
        let deadline = Date().addingTimeInterval(5)
        var sawPartial = false
        var earlyReceipt = false
        while Date() < deadline {
            guard body.exists else { continue }
            if body.label.contains(ending) { break }
            if receipt.exists && !body.label.contains(ending) {
                capture("Completion footer beside unfinished text", app: app)
                earlyReceipt = true
                break
            }
            if !sawPartial { capture("The final paragraph is still appearing", app: app) }
            sawPartial = true
        }
        wait(for: [published], timeout: 4)
        XCTAssertFalse(earlyReceipt, "Completion controls became available before the final text")
        XCTAssertEqual(sawPartial, !reduceMotion)
        XCTAssertTrue(body.exists && body.label.contains(ending))
        XCTAssertTrue(receipt.waitForExistence(timeout: 4))
        XCTAssertTrue(app.buttons.matching(identifier: "复制回复").element(boundBy: 1).exists)
        capture("The full final paragraph and completion footer", app: app)
    }

    func testReplyUpdatesPreserveTheHistoryReadingPosition() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_REPLY_HISTORY"] == "1" else {
            throw XCTSkip("Requires the streaming fixture with earlier messages")
        }
        let control = try replyFixture()
        let app = openReplyFixture()
        XCTAssertTrue(app.staticTexts["已显示的回复前缀。"].firstMatch.waitForExistence(timeout: 8))
        let latest = app.buttons["回到最新消息"].firstMatch
        let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.68, dy: 0.31))
        let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.68, dy: 0.76))
        from.press(forDuration: 0.05, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.35)
        XCTAssertTrue(latest.waitForExistence(timeout: 6))
        let earlier = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "先前的回复 "))
        let readingRange = app.frame.minY + 160 ... app.frame.midY
        guard let anchor = earlier.allElementsBoundByIndex.first(where: { readingRange.contains($0.frame.midY) }) else {
            return XCTFail("No historical reply is in the reading area")
        }
        let message = app.staticTexts[anchor.label].firstMatch
        let position = message.frame.midY
        capture("The earlier reply before the Worker sends more text", app: app)
        wait(for: [publishReplyFixture("append", at: control)], timeout: 4)
        wait(for: [publishReplyFixture("finish", at: control)], timeout: 4)
        let received = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "最新消息"), object: latest)
        wait(for: [received], timeout: 8)
        XCTAssertEqual(message.frame.midY, position, accuracy: 1)
        capture("The same earlier reply after the Worker finishes", app: app)

        latest.tap()
        XCTAssertTrue(app.staticTexts["保留完成事件的尾句。"].firstMatch.waitForExistence(timeout: 6))
        XCTAssertTrue(latest.waitForNonExistence(timeout: 6))
        capture("Return to the completed reply", app: app)
    }

    func testChildReceiptFromAnotherClientPreservesAnIdenticalLocalDraft() throws {
        guard let address = ProcessInfo.processInfo.environment["SHIOJI_CHILD_RECEIPT_FIXTURE"],
              let releaseURL = URL(string: address + "/confirm") else {
            throw XCTSkip("Requires the isolated child input worker fixture")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        let parent = app.buttons["子 Agent 回执验收"].firstMatch
        let activity = app.buttons["会话活动"].firstMatch
        func openChild() {
            XCTAssertTrue(parent.waitForExistence(timeout: 16))
            if !activity.exists { parent.tap() }
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
            app.buttons["核对接纳状态"].firstMatch.tap()
            XCTAssertTrue(app.staticTexts["子 Agent 正在核对日志"].firstMatch.waitForExistence(timeout: 8))
        }
        openChild()

        let check = app.buttons["核对子 Agent 输入回执"].firstMatch
        let canCheck = check.waitForExistence(timeout: 5)
        capture(canCheck ? "Pending input restored from the owning worker" : "Another client's pending input is missing", app: app)
        XCTAssertTrue(canCheck)
        XCTAssertFalse(app.buttons["介入子 Agent"].exists)
        let message = "继续检查最后一项"
        XCTAssertTrue(app.staticTexts[message].exists)
        check.tap()
        let unknown = app.staticTexts["尚未确认是否接收"].firstMatch
        XCTAssertTrue(unknown.waitForExistence(timeout: 8))
        XCTAssertTrue(check.exists)

        let confirmed = expectation(description: "The isolated harness can now prove receipt")
        var request = URLRequest(url: releaseURL)
        request.httpMethod = "POST"
        URLSession.shared.dataTask(with: request) { _, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
            confirmed.fulfill()
        }.resume()
        wait(for: [confirmed], timeout: 6)
        app.terminate()
        app.launch()
        openChild()
        XCTAssertTrue(check.waitForExistence(timeout: 8))
        capture("The pending child input survives restarting the app", app: app)
        check.tap()
        let send = app.buttons["介入子 Agent"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 8))
        XCTAssertFalse(check.exists)
        let draft = app.descendants(matching: .any).matching(identifier: "补充给子 Agent").firstMatch
        XCTAssertEqual(draft.value as? String, message)
        XCTAssertTrue(app.staticTexts[message].waitForExistence(timeout: 5))
        capture("Confirmed child message and independent local draft", app: app)
        app.navigationBars["核对接纳状态"].buttons["BackButton"].tap()
        app.navigationBars["会话活动"].buttons["BackButton"].tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts[message].exists)
    }

    func testWorkDetailsCanCopyFollowAndLocateOlderParentMessages() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["SHIOJI_WORK_ACTIONS_FIXTURE"] == "1",
                          "Requires an isolated worker with older parent turns and two kinds of work")
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        let conversation = app.buttons["子任务历史验收"].firstMatch
        let activity = app.buttons["会话活动"].firstMatch
        let original = app.staticTexts["启动审阅的原始消息"].firstMatch
        let locate = app.buttons["定位来源"].firstMatch
        let follow = app.buttons["跟随最新输出"].firstMatch
        XCTAssertTrue(conversation.waitForExistence(timeout: 16))
        conversation.tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertFalse(original.exists)
        activity.tap()
        let background = app.buttons["后台构建验收"].firstMatch
        XCTAssertTrue(background.waitForExistence(timeout: 8))
        background.tap()
        let copy = app.buttons["复制后台输出"].firstMatch
        XCTAssertTrue(copy.waitForExistence(timeout: 8), app.debugDescription)
        let terminal = app.descendants(matching: .any)["终端输出"].firstMatch
        XCTAssertTrue(terminal.exists)
        let bottom = terminal.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.72))
        let top = terminal.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.42))
        top.press(forDuration: 0.05, thenDragTo: bottom)
        XCTAssertTrue(follow.waitForExistence(timeout: 8), app.debugDescription)
        capture("Background output actions while reading earlier lines", app: app)
        follow.tap()
        XCTAssertTrue(follow.waitForNonExistence(timeout: 8), app.debugDescription)
        copy.tap()
        XCTAssertTrue(locate.waitForExistence(timeout: 6))
        locate.tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertTrue(original.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(app.staticTexts["隐藏的后台来源"].exists)
        XCTAssertFalse(app.staticTexts["隐藏的子 Agent 来源"].exists)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "主会话草稿")
        capture("Located an older visible parent message without adding work rows", app: app)

        activity.tap()
        let child = app.buttons["长记录 Agent"].firstMatch
        XCTAssertTrue(child.waitForExistence(timeout: 8))
        child.tap()
        XCTAssertTrue(app.staticTexts["最新子任务回执"].firstMatch.waitForExistence(timeout: 8))
        XCTAssertFalse(copy.exists)
        let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.64, dy: 0.30))
        let end = app.coordinate(withNormalizedOffset: CGVector(dx: 0.64, dy: 0.67))
        for _ in 0..<3 where !follow.exists { start.press(forDuration: 0.05, thenDragTo: end) }
        XCTAssertTrue(follow.waitForExistence(timeout: 6), app.debugDescription)
        capture("Child history can follow its own latest reply", app: app)
        follow.tap()
        XCTAssertTrue(follow.waitForNonExistence(timeout: 6))
        locate.tap()
        XCTAssertTrue(original.waitForExistence(timeout: 8))
        XCTAssertFalse(app.staticTexts["最新子任务回执"].exists)
    }

    func testEarlierChildConversationLoadsWithoutMovingTheCurrentMessage() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_WORK_HISTORY_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the isolated child history worker fixture")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        let parent = app.buttons["子任务历史验收"].firstMatch
        XCTAssertTrue(parent.waitForExistence(timeout: 16))
        parent.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertFalse(app.staticTexts["子任务最早的正文"].exists)
        activity.tap()
        app.buttons["历史审阅 Agent"].firstMatch.tap()
        let latest = app.staticTexts["最新子任务回执"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 8))
        capture("Latest child records on open", app: app)

        let more = app.buttons["加载更早消息"].firstMatch
        for _ in 0..<9 where !more.isHittable { app.swipeDown(velocity: .fast) }
        if !more.isHittable {
            capture("No way to read the earlier child conversation", app: app)
        }
        XCTAssertTrue(more.isHittable)
        let anchor = app.staticTexts["中段子任务问题"].firstMatch
        XCTAssertTrue(anchor.exists)
        let before = anchor.frame.minY
        more.tap()
        XCTAssertTrue(more.waitForNonExistence(timeout: 8))
        XCTAssertEqual(anchor.frame.minY, before, accuracy: 4)
        capture("Current child message stays in place after paging", app: app)

        let early = app.staticTexts["子任务最早的正文"].firstMatch
        for _ in 0..<9 where !early.exists { app.swipeDown(velocity: .fast) }
        XCTAssertTrue(early.waitForExistence(timeout: 5))
        let inspect = app.buttons["核查旧文件"].firstMatch
        if !inspect.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(inspect.isHittable)
        inspect.tap()
        XCTAssertTrue(app.staticTexts["早期工具结构化内容"].waitForExistence(timeout: 5))
        capture("Older child message and expandable tool output", app: app)
        app.navigationBars["历史审阅 Agent"].buttons["BackButton"].tap()
        app.navigationBars["会话活动"].buttons["BackButton"].tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
        XCTAssertFalse(early.exists)
    }

    func testWorkspaceHooksPreservesScopeAndSeparatesEnablementFromTrust() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-workspace-mcp")
        XCTAssertTrue(app.navigationBars["工作区设置"].waitForExistence(timeout: 8))
        app.descendants(matching: .any).matching(identifier: "Hooks").firstMatch.tap()
        capture("Workspace Hooks before source management", app: app)
        guard app.textFields["搜索 Hooks"].waitForExistence(timeout: 5) else {
            return XCTFail("Workspace Hooks has no source list or search")
        }
        let project = app.buttons["查看 Hook：PostToolUse"]
        let inherited = app.buttons["查看 Hook：Stop"]
        XCTAssertTrue(project.waitForExistence(timeout: 5))
        XCTAssertTrue(inherited.exists)
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "启停 Hook：Stop").firstMatch.exists)
        capture("Workspace Hook sources", app: app)

        project.tap()
        let toggle = app.descendants(matching: .any).matching(identifier: "启停 Hook：PostToolUse").firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        XCTAssertTrue(toggle.isSelected)
        toggle.tap()
        let disabled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in toggle.exists && !toggle.isSelected }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [disabled], timeout: 5), .completed)
        app.buttons["信任此版本"].tap()
        XCTAssertTrue(app.staticTexts["已信任"].waitForExistence(timeout: 5))
        XCTAssertFalse(toggle.isSelected)
        capture("Trust version without enabling Hook", app: app)
        app.buttons["返回 Hooks 列表"].tap()
        XCTAssertTrue(inherited.waitForExistence(timeout: 5))
        inherited.tap()
        app.buttons["管理设备 Hooks"].tap()
        XCTAssertTrue(app.navigationBars["Codex"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["信任此版本"].waitForExistence(timeout: 5))
        XCTAssertFalse(project.exists)
        app.buttons["信任此版本"].tap()
        XCTAssertTrue(app.staticTexts["已信任"].waitForExistence(timeout: 5))
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(app.buttons["管理设备 Hooks"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["已信任"].waitForExistence(timeout: 5))
        capture("Inherited Hook after device management", app: app)
    }

    func testHookBatchReviewLooksUpTheOriginalReceiptAndKeepsOtherDefinitions() {
        let app = launch("--preview-board-workspace-hooks-receipt")
        XCTAssertTrue(app.navigationBars["工作区设置"].waitForExistence(timeout: 8))
        let tab = app.descendants(matching: .any).matching(identifier: "Hooks").firstMatch
        tab.tap()
        let review = app.buttons["审核待审批 Hooks"]
        XCTAssertTrue(review.waitForExistence(timeout: 5))
        review.tap()
        let format = app.buttons["选择 Hook：PostToolUse"]
        let validate = app.buttons["选择 Hook：PreToolUse"]
        XCTAssertTrue(format.waitForExistence(timeout: 5))
        XCTAssertTrue(validate.exists)
        XCTAssertFalse(app.buttons["选择 Hook：Stop"].exists)
        capture("Batch Hook review", app: app)
        format.tap()
        let selected = app.buttons["信任所选"]
        XCTAssertTrue(selected.isEnabled)
        selected.tap()
        let lookup = app.buttons["查询操作回执"]
        XCTAssertTrue(lookup.waitForExistence(timeout: 5))
        XCTAssertTrue(format.exists)
        XCTAssertFalse(selected.isEnabled)
        capture("Hook save receipt unconfirmed", app: app)
        lookup.tap()
        XCTAssertTrue(format.waitForNonExistence(timeout: 5))
        XCTAssertTrue(validate.exists)
        XCTAssertFalse(selected.isEnabled)
        app.buttons["信任全部"].tap()
        XCTAssertTrue(app.staticTexts["没有待审核的 Hooks"].waitForExistence(timeout: 5))
        app.buttons["返回 Hooks 列表"].tap()
        let inherited = app.buttons["查看 Hook：Stop"]
        XCTAssertTrue(inherited.waitForExistence(timeout: 5))
        XCTAssertEqual(inherited.value as? String, "待审核 · 已启用")
    }

    func testArchivedSessionHooksUsesAcceptedDirectoryAndRemainsReadOnly() {
        let app = launch("--preview-board-hook-session-archived")
        XCTAssertTrue(app.navigationBars["会话 Hooks"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["会话加载状态未知"].exists)
        XCTAssertFalse(app.buttons["审核待审批 Hooks"].exists)
        app.buttons["查看 Hook：PostToolUse"].tap()
        XCTAssertTrue(app.buttons["复制 Hook 来源路径"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["信任此版本"].exists)
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "启停 Hook：PostToolUse").firstMatch.exists)
        app.buttons["Hook 详细信息"].tap()
        XCTAssertTrue(app.staticTexts["/Users/developer/Shioji-worktrees/review"].waitForExistence(timeout: 5))
        capture("Archived session Hook scope", app: app)
    }

    func testWorkspaceSkillsOnlyChangesOwnedConfigurationAndKeepsInheritedDetail() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-workspace-mcp")
        XCTAssertTrue(app.navigationBars["工作区设置"].waitForExistence(timeout: 8))
        capture("Workspace settings before Skills", app: app)
        let tab = app.descendants(matching: .any).matching(identifier: "Skills").firstMatch
        guard tab.waitForExistence(timeout: 4) else { return XCTFail("Workspace settings have no Skills tab") }
        tab.tap()
        let project = app.buttons["查看 Skill：build-check"]
        XCTAssertTrue(project.waitForExistence(timeout: 5))
        capture("Workspace Skills sources", app: app)
        let projectSwitch = app.descendants(matching: .any).matching(identifier: "启停 Skill：build-check").firstMatch
        XCTAssertTrue(projectSwitch.exists)
        XCTAssertTrue(projectSwitch.isSelected)
        projectSwitch.tap()
        let disabled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            projectSwitch.exists && !projectSwitch.isSelected && (project.value as? String) == "已停用"
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [disabled], timeout: 5), .completed)
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "启停 Skill：release-notes").firstMatch.exists)

        let inherited = app.buttons["查看 Skill：release-notes"]
        inherited.tap()
        let copy = app.buttons["复制 Skill 路径"]
        XCTAssertTrue(copy.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["复制 Skill 说明"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["整理发布说明前先核对变更范围。"].exists)
        capture("Workspace inherited Skill Markdown", app: app)
        app.buttons["管理设备 Skills"].tap()
        XCTAssertTrue(app.navigationBars["Codex"].waitForExistence(timeout: 5))
        let userSwitch = app.descendants(matching: .any).matching(identifier: "启停 Skill：release-notes").firstMatch
        XCTAssertTrue(userSwitch.waitForExistence(timeout: 5))
        userSwitch.tap()
        let userDisabled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            userSwitch.exists && !userSwitch.isSelected
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [userDisabled], timeout: 5), .completed)
        capture("Device Skills management", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(copy.waitForExistence(timeout: 5))
        app.buttons["返回 Skills 列表"].tap()
        let refreshed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            inherited.exists && (inherited.value as? String) == "已停用"
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [refreshed], timeout: 5), .completed)
    }

    func testWorkspaceSkillsRequiresRefreshAfterAnUnconfirmedSave() {
        let app = launch("--preview-board-workspace-skills-uncertain")
        let tab = app.descendants(matching: .any).matching(identifier: "Skills").firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 8))
        tab.tap()
        let toggle = app.descendants(matching: .any).matching(identifier: "启停 Skill：build-check").firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        XCTAssertTrue(toggle.isSelected)
        toggle.tap()
        XCTAssertTrue(app.staticTexts["保存未确认，显示上次状态。请刷新核对"].waitForExistence(timeout: 5))
        XCTAssertTrue(toggle.isSelected)
        XCTAssertFalse(toggle.isEnabled)
        capture("Workspace Skill unconfirmed save", app: app)
        app.buttons["刷新 Skills"].tap()
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            toggle.exists && toggle.isEnabled && toggle.isSelected
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 5), .completed)
        toggle.tap()
        let changed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            toggle.exists && !toggle.isSelected
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [changed], timeout: 5), .completed)
    }

    func testWorkspaceMCPShowsConfigurationSourcesAndKeepsItsDetailAfterManagingAnInheritedService() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-workspace-mcp")
        XCTAssertTrue(app.navigationBars["工作区设置"].waitForExistence(timeout: 8))
        capture("Workspace settings before opening MCP", app: app)
        let tab = app.descendants(matching: .any).matching(identifier: "MCP").firstMatch
        guard tab.waitForExistence(timeout: 4) else { return XCTFail("Workspace settings have no MCP source inspector") }
        let name = app.textFields["名称"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap()
        app.typeText(" Draft")
        let draft = name.value as? String
        XCTAssertTrue(draft?.contains("Draft") == true)
        tab.tap()
        let project = app.buttons["查看 MCP：build-tools"]
        XCTAssertTrue(project.waitForExistence(timeout: 5))
        XCTAssertEqual(project.value as? String, "已配置")
        capture("Workspace MCP configuration sources", app: app)
        project.tap()
        XCTAssertTrue(app.buttons["复制配置路径"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["管理设备 MCP"].exists)
        capture("Read-only project MCP source", app: app)
        app.buttons["返回 MCP 列表"].tap()
        let search = app.textFields["搜索 MCP"]
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        app.typeText("Documentation")
        XCTAssertTrue(project.waitForNonExistence(timeout: 5))
        app.buttons["查看 MCP：Documentation"].tap()
        app.buttons["管理设备 MCP"].tap()
        XCTAssertTrue(app.navigationBars["Codex"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["编辑 MCP：Documentation"].waitForExistence(timeout: 5))
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(app.buttons["管理设备 MCP"].waitForExistence(timeout: 5))
        capture("Inherited workspace MCP after returning", app: app)
        app.buttons["返回 MCP 列表"].tap()
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        XCTAssertEqual(search.value as? String, "Documentation")
        app.descendants(matching: .any).matching(identifier: "常规").firstMatch.tap()
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        XCTAssertEqual(name.value as? String, draft)
    }

    func testWorkspaceMCPKeepsFoldersSeparateAndShowsOnlyTheSelectedSessionObservation() {
        let app = launch("--preview-board-workspace-mcp")
        let tab = app.descendants(matching: .any).matching(identifier: "MCP").firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 8))
        tab.tap()
        let folder = app.buttons["MCP 查询目录"]
        XCTAssertTrue(folder.waitForExistence(timeout: 5))
        folder.tap()
        app.descendants(matching: .any).matching(identifier: "/Users/developer/Shioji/docs").firstMatch.tap()
        let documentation = app.buttons["查看 MCP：Documentation"]
        XCTAssertTrue(documentation.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["查看 MCP：build-tools"].waitForNonExistence(timeout: 5))

        let scope = app.buttons["MCP 查看范围"]
        let scopeFrame = scope.frame
        scope.tap()
        app.descendants(matching: .any).matching(identifier: "检查移动端会话体验").firstMatch.tap()
        let connected = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            documentation.value as? String == "已连接"
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [connected], timeout: 5), .completed)
        documentation.tap()
        XCTAssertTrue(app.staticTexts["find_docs"].waitForExistence(timeout: 5))
        capture("Live workspace MCP tools", app: app)
        app.buttons["返回 MCP 列表"].tap()

        let scopeSettled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            scope.exists && scope.isHittable && abs(scope.frame.minX - scopeFrame.minX) < 1
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [scopeSettled], timeout: 5), .completed)
        scope.tap()
        app.descendants(matching: .any).matching(identifier: "整理键盘交互和阅读位置").firstMatch.tap()
        let unknown = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            documentation.value as? String == "状态未知"
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [unknown], timeout: 5), .completed)
        capture("Workspace MCP without a live session", app: app)
        scope.tap()
        app.descendants(matching: .any).matching(identifier: "配置来源").firstMatch.tap()
        XCTAssertTrue(folder.waitForExistence(timeout: 5))
        XCTAssertEqual(folder.value as? String, "/Users/developer/Shioji/docs")
    }

    func testDeviceScheduleAndWorkspaceNavigation() {
        let app = launch("--preview")
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        app.buttons["Studio Display Mac"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["Studio Display Mac"].waitForExistence(timeout: 5))
        capture("Native device detail", app: app)
        app.buttons["Shioji iOS"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["工作区设置"].waitForExistence(timeout: 5))
        capture("Native workspace settings", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        app.navigationBars.buttons["BackButton"].tap()
        app.buttons["定时任务"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["定时任务"].waitForExistence(timeout: 5))
        capture("Native schedule list", app: app)
        app.buttons["每日构建检查"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["任务详情"].waitForExistence(timeout: 5))
        capture("Native schedule detail", app: app)
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@","11月19日")).firstMatch.tap()
        XCTAssertTrue(app.navigationBars["运行回执"].waitForExistence(timeout: 5))
        capture("Native schedule receipt", app: app)
    }

    func testWorkspaceDirectoryMenuPromotesAndReplacesThePrimaryFolder() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-workspace-config")
        let primary = app.buttons["主目录"].firstMatch
        XCTAssertTrue(primary.waitForExistence(timeout: 8), app.debugDescription)
        let add = app.buttons["添加附加目录"].firstMatch
        add.tap()
        app.buttons["Design tools"].firstMatch.tap()
        let choose = app.buttons["选择此目录"].firstMatch
        XCTAssertTrue(choose.waitForExistence(timeout: 6))
        choose.tap()
        let additional = app.buttons["附加目录"].firstMatch
        XCTAssertTrue(additional.waitForExistence(timeout: 5))
        additional.press(forDuration: 0.8)
        let promote = app.descendants(matching: .any)["设为主目录"].firstMatch
        XCTAssertTrue(promote.waitForExistence(timeout: 5), app.debugDescription)
        capture("Native directory context menu", app: app)
        promote.tap()
        let promoted = NSPredicate(format: "value ENDSWITH %@", "/Design tools")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: promoted, object: primary)], timeout: 5), .completed)
        XCTAssertEqual(additional.value as? String, "/Users/developer/Shioji")

        primary.press(forDuration: 0.8)
        app.descendants(matching: .any)["移除目录"].firstMatch.tap()
        let restored = NSPredicate(format: "value == %@", "/Users/developer/Shioji")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: restored, object: primary)], timeout: 5), .completed)
        XCTAssertTrue(additional.waitForNonExistence(timeout: 5))
        primary.press(forDuration: 0.8)
        XCTAssertFalse(app.descendants(matching: .any)["移除目录"].firstMatch.isHittable,
                       "The last directory must stay in the draft")
    }

    func testWorkspacePrimaryDirectoryPersistsOnItsWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let original = environment["SHIOJI_WORKSPACE_PRIMARY_ORIGINAL"],
              let promoted = environment["SHIOJI_WORKSPACE_PRIMARY_PROMOTED"] else {
            throw XCTSkip("Requires a disposable Worker with two workspace folders")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 10), app.debugDescription)
        devices.tap()
        let worker = app.buttons["Workspace fixture Mac"].firstMatch
        XCTAssertTrue(worker.waitForExistence(timeout: 8), app.debugDescription)
        worker.tap()
        let workspace = app.buttons["test"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 8), app.debugDescription)
        workspace.tap()
        let primary = app.buttons["主目录"].firstMatch
        XCTAssertTrue(primary.waitForExistence(timeout: 8))
        XCTAssertEqual(primary.value as? String, original)
        app.buttons["附加目录"].firstMatch.press(forDuration: 0.8)
        let promote = app.descendants(matching: .any)["设为主目录"].firstMatch
        XCTAssertTrue(promote.waitForExistence(timeout: 5), app.debugDescription)
        promote.tap()
        let updated = NSPredicate(format: "value == %@", promoted)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: updated, object: primary)], timeout: 5), .completed)
        let settings = app.navigationBars["工作区设置"]
        settings.buttons["保存工作区"].firstMatch.tap()
        XCTAssertTrue(settings.waitForNonExistence(timeout: 12), app.debugDescription)
        workspace.tap()
        XCTAssertTrue(primary.waitForExistence(timeout: 8))
        XCTAssertEqual(primary.value as? String, promoted)
        XCTAssertEqual(app.buttons["附加目录"].firstMatch.value as? String, original)
        capture("The owning Worker persists the reordered workspace folders", app: app)
    }

    private func openScheduleCatalog(_ app: XCUIApplication) {
        let view = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(view.waitForExistence(timeout: 8), app.debugDescription)
        view.tap()
        let schedules = app.descendants(matching: .any)["定时任务"].firstMatch
        XCTAssertTrue(schedules.waitForExistence(timeout: 5), app.debugDescription)
        schedules.tap()
        XCTAssertTrue(app.navigationBars["定时任务"].waitForExistence(timeout: 5), app.debugDescription)
    }

    func testScheduleCatalogOpensFromSessionView() {
        let app = launch("--preview-board-inbox")
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 8))
        devices.tap()
        XCTAssertTrue(app.buttons["Tailnet"].waitForExistence(timeout: 5))
        capture("Device network entries", app: app)
        XCTAssertFalse(app.buttons["定时任务"].exists)
        app.buttons["会话"].firstMatch.tap()
        openScheduleCatalog(app)
        let task = app.buttons["每日构建检查"].firstMatch
        XCTAssertTrue(task.waitForExistence(timeout: 5))
        capture("Schedule catalog from session view", app: app)
        task.tap()
        XCTAssertTrue(app.navigationBars["任务详情"].waitForExistence(timeout: 5))
        capture("Schedule detail", app: app)
    }

    func testOfflineScheduleOpensDetailBeforeConnecting() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_OFFLINE_SCHEDULE_FIXTURE"] == "1" else {
            throw XCTSkip("Requires a cached schedule on a disposable offline device")
        }
        let app = launch("")
        openScheduleCatalog(app)
        XCTAssertFalse(app.buttons["重新读取"].exists)
        let task = app.buttons["离线构建检查"].firstMatch
        XCTAssertTrue(task.waitForExistence(timeout: 8), app.debugDescription)
        task.tap()
        XCTAssertTrue(app.navigationBars["任务详情"].waitForExistence(timeout: 4), app.debugDescription)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "离线构建检查")).firstMatch.exists)
        XCTAssertFalse(app.staticTexts["正在连接任务所属设备…"].exists)
        capture("Offline schedule connects in its detail", app: app)
        XCTAssertTrue(app.buttons["重试"].waitForExistence(timeout: 18), app.debugDescription)
        app.navigationBars["任务详情"].buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(task.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["重新读取"].exists)
    }

    func testScheduledTaskCanBeDeletedAfterNativeConfirmation() {
        let app = launch("--preview-board-schedule-list")
        let task = app.buttons["每日构建检查"].firstMatch
        XCTAssertTrue(task.waitForExistence(timeout: 8))
        task.tap()
        let remove = app.buttons["删除任务"].firstMatch
        XCTAssertTrue(remove.waitForExistence(timeout: 5))
        remove.tap()
        let confirmation = app.alerts["删除定时任务？"]
        XCTAssertTrue(confirmation.waitForExistence(timeout: 5))
        confirmation.buttons["取消"].tap()
        XCTAssertTrue(remove.exists)
        remove.tap()
        XCTAssertTrue(confirmation.waitForExistence(timeout: 5))
        capture("Delete schedule confirmation", app: app)
        confirmation.buttons["删除任务"].tap()
        XCTAssertTrue(app.staticTexts["还没有定时任务"].waitForExistence(timeout: 5))
        XCTAssertFalse(task.exists)
    }

    func testScheduleEditorShowsOwningTimeZoneAndExecutionOptions() {
        let app = launch("--preview-board-schedule-editor")
        XCTAssertTrue(app.navigationBars["编辑定时任务"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["运行预览"].waitForExistence(timeout: 5))
        let zone = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Asia/Taipei")).firstMatch
        XCTAssertTrue(zone.exists)
        let worktree = app.descendants(matching: .any)["新建 worktree"].firstMatch
        for _ in 0..<3 where !worktree.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(worktree.isHittable)
        let effort = app.buttons["推理强度"].firstMatch
        XCTAssertTrue(effort.exists)
        capture("Schedule editor execution options", app: app)
    }

    func testScheduleDeletionOnTheOwningWorker() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_SCHEDULE_FIXTURE"] == "1" else {
            throw XCTSkip("Requires a disposable Worker with a saved schedule")
        }
        let app = launch("")
        let devices = app.buttons["设备"].firstMatch
        XCTAssertTrue(devices.waitForExistence(timeout: 10))
        devices.tap()
        app.buttons["会话"].firstMatch.tap()
        openScheduleCatalog(app)
        let task = app.buttons["隔离定时任务"].firstMatch
        XCTAssertTrue(task.waitForExistence(timeout: 20), app.debugDescription)
        task.tap()
        let zone = app.staticTexts["执行设备时区 · America/Los_Angeles"]
        XCTAssertTrue(zone.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(app.staticTexts["下次运行"].exists)
        app.buttons["删除任务"].firstMatch.tap()
        let alert = app.alerts["删除定时任务？"]
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "隔离定时任务")).firstMatch.exists)
        alert.buttons["删除任务"].tap()
        XCTAssertTrue(app.staticTexts["还没有定时任务"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse(task.exists)
        capture("Owning Worker confirmed schedule deletion", app: app)
    }

    func testScheduleCatalogAcrossOwningWorkers() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_SCHEDULE_CATALOG_FIXTURE"] == "1" else {
            throw XCTSkip("Requires two disposable Workers with scheduled tasks")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        openScheduleCatalog(app)
        let first = app.buttons["前端定时任务"].firstMatch
        let second = app.buttons["构建定时任务"].firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(second.waitForExistence(timeout: 10), app.debugDescription)
        capture("Schedule catalog shows both owning Workers", app: app)

        let workspaceFilter = app.buttons["工作区筛选"].firstMatch
        let statusFilter = app.buttons["状态筛选"].firstMatch
        statusFilter.tap()
        let paused = app.descendants(matching: .any)["已暂停"].firstMatch
        XCTAssertTrue(paused.waitForExistence(timeout: 5))
        paused.tap()
        XCTAssertTrue(first.waitForNonExistence(timeout: 5))
        XCTAssertTrue(second.exists)

        workspaceFilter.tap()
        let front = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "前端工作区 · ")).firstMatch
        XCTAssertTrue(front.waitForExistence(timeout: 5))
        front.tap()
        XCTAssertTrue(app.staticTexts["此筛选下没有定时任务"].waitForExistence(timeout: 5))
        workspaceFilter.tap()
        app.descendants(matching: .any)["全部工作区"].firstMatch.tap()
        XCTAssertTrue(second.waitForExistence(timeout: 5))
        statusFilter.tap()
        app.descendants(matching: .any)["全部状态"].firstMatch.tap()
        XCTAssertTrue(first.waitForExistence(timeout: 5))

        second.tap()
        let detail = app.navigationBars["任务详情"]
        XCTAssertTrue(detail.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts["暂无运行记录"].waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(app.buttons["刷新记录"].exists)
        let remove = detail.buttons["删除任务"].firstMatch
        XCTAssertTrue(remove.waitForExistence(timeout: 8))
        remove.tap()
        let alert = app.alerts["删除定时任务？"]
        XCTAssertTrue(alert.waitForExistence(timeout: 6), app.debugDescription)
        alert.buttons["删除任务"].tap()
        XCTAssertTrue(app.navigationBars["定时任务"].waitForExistence(timeout: 8))
        XCTAssertTrue(first.waitForExistence(timeout: 8))
        XCTAssertFalse(second.exists)

        app.navigationBars["定时任务"].buttons["新建定时任务"].tap()
        let picker = app.navigationBars["选择工作区"]
        XCTAssertTrue(picker.waitForExistence(timeout: 8), app.debugDescription)
        let workspace = app.buttons["前端工作区"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        workspace.tap()
        let editor = app.navigationBars["新建定时任务"]
        XCTAssertTrue(editor.waitForExistence(timeout: 10), app.debugDescription)
        let name = app.textFields["名称"].firstMatch
        XCTAssertTrue(name.waitForExistence(timeout: 6))
        name.tap()
        app.typeText("新建远端任务")
        let prompt = app.descendants(matching: .any)["提示词"].firstMatch
        XCTAssertTrue(prompt.exists)
        prompt.tap()
        app.typeText("Verify the owning Worker")
        editor.buttons["保存任务"].tap()
        let created = app.buttons["新建远端任务"].firstMatch
        XCTAssertTrue(created.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(first.exists)
        capture("New scheduled task appears on the owning Worker", app: app)
    }

    func testScheduleCatalogRefreshesWhileVisible() throws {
        guard let control = ProcessInfo.processInfo.environment["SHIOJI_SCHEDULE_CATALOG_CONTROL"] else {
            throw XCTSkip("Requires a live schedule catalog fixture")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        openScheduleCatalog(app)
        let original = app.buttons["构建定时任务"].firstMatch
        XCTAssertTrue(original.waitForExistence(timeout: 10), app.debugDescription)
        try Data().write(to: URL(fileURLWithPath: control), options: .atomic)
        let renamed = app.buttons["从另一端改名的任务"].firstMatch
        XCTAssertTrue(renamed.waitForExistence(timeout: 21), app.debugDescription)
        XCTAssertFalse(original.exists)
        XCTAssertTrue(app.buttons["前端定时任务"].exists)
        capture("Visible schedule catalog automatically adopts the owning Worker's update", app: app)
    }

    func testScheduleDetailAndReceiptUpdateWhileVisible() throws {
        guard let control = ProcessInfo.processInfo.environment["SHIOJI_SCHEDULE_CATALOG_CONTROL"] else {
            throw XCTSkip("Requires two isolated Workers and a controlled schedule run")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        openScheduleCatalog(app)
        let task = app.buttons["构建定时任务"].firstMatch
        guard task.waitForExistence(timeout: 10) else { return XCTFail("The remote task did not load") }
        task.tap()
        XCTAssertTrue(app.staticTexts["暂无运行记录"].waitForExistence(timeout: 12), app.debugDescription)
        capture("Schedule detail before an external edit", app: app)
        try Data().write(to: URL(fileURLWithPath: control), options: .atomic)
        let renamed = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "另一客户端更新的构建任务")).firstMatch
        guard renamed.waitForExistence(timeout: 12) else {
            capture("Schedule detail stays stale after the Worker committed a rename", app: app)
            return XCTFail("The visible schedule detail did not adopt the remote revision")
        }
        capture("Schedule detail adopts the new owning-Worker revision", app: app)

        app.buttons["立即运行"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["运行回执"].waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.staticTexts["正在运行"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(app.buttons["打开对应会话"].waitForExistence(timeout: 8), app.debugDescription)
        capture("A live schedule receipt can open its session", app: app)

        try Data().write(to: URL(fileURLWithPath: control + ".complete"), options: .atomic)
        XCTAssertTrue(app.staticTexts["已完成"].waitForExistence(timeout: 12), app.debugDescription)
        try Data().write(to: URL(fileURLWithPath: control + ".archive"), options: .atomic)
        XCTAssertTrue(app.staticTexts["会话已归档"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(app.buttons["打开对应会话"].exists)
        capture("The same schedule receipt reflects completion and archival", app: app)
        app.buttons["打开对应会话"].firstMatch.tap()
        let archived = app.navigationBars["Original prompt"]
        XCTAssertTrue(archived.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(app.textFields["消息"].exists)
        archived.buttons["BackButton"].tap()
        XCTAssertTrue(app.navigationBars["运行回执"].waitForExistence(timeout: 8))
        app.navigationBars["运行回执"].buttons["BackButton"].tap()
        XCTAssertTrue(app.navigationBars["任务详情"].waitForExistence(timeout: 8))
        let completedRun = app.buttons.matching(NSPredicate(
            format: "label CONTAINS %@ AND value == %@", "手动运行", "已完成 · 会话已归档"
        )).firstMatch
        XCTAssertTrue(completedRun.waitForExistence(timeout: 12), app.debugDescription)
        capture("Refreshed schedule history announces the completed archived run", app: app)
    }

    private func openRemoteRecoveryFixture(_ mode: String) throws -> XCUIApplication {
        guard ProcessInfo.processInfo.environment["SHIOJI_RECOVERY_FIXTURE"] == mode else {
            throw XCTSkip("Requires an isolated Worker with the matching recovery state")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("")
        let source = app.buttons["远程恢复验收"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 15), app.debugDescription)
        if !app.buttons["会话活动"].firstMatch.exists { source.tap() }
        return app
    }

    func testRemoteRecoveryContinuesSameSessionAndPreservesDraft() throws {
        let app = try openRemoteRecoveryFixture("continue")
        let resume = app.buttons["继续原会话"].firstMatch
        guard resume.waitForExistence(timeout: 10) else {
            XCTFail("The Worker marked this interrupted session ready to continue")
            capture("Recovery controls missing from the interrupted session", app: app)
            return
        }
        let draft = app.textFields["消息"].firstMatch
        XCTAssertEqual(draft.value as? String, "原会话未发送草稿")
        capture("Interrupted session offers a scoped continuation", app: app)
        resume.tap()
        let reply = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "from alpha: 继续上一轮")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 14), app.debugDescription)
        XCTAssertEqual(draft.value as? String, "原会话未发送草稿")
        XCTAssertFalse(resume.exists)
        capture("The same conversation continues with its ordinary draft intact", app: app)
    }

    func testRemoteRecoveryPreparesAnEditableDraftAndReturnsToSource() throws {
        let app = try openRemoteRecoveryFixture("draft")
        let more = app.buttons["更多恢复操作"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 10), app.debugDescription)
        more.tap()
        let new = app.descendants(matching: .any)["在新会话接续"].firstMatch
        XCTAssertTrue(new.waitForExistence(timeout: 5))
        new.tap()
        let editor = app.textFields["消息"].firstMatch
        let recovered = NSPredicate(format: "value CONTAINS %@", "接续会话「远程恢复验收」")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: recovered, object: editor)], timeout: 8), .completed)
        let value = editor.value as? String ?? ""
        XCTAssertTrue(value.contains("上次发送的原文"))
        XCTAssertTrue(value.contains("原会话未发送草稿"))
        XCTAssertTrue(value.contains("原有新会话草稿内容"))
        XCTAssertFalse(value.contains("旧会话队列保留"))
        capture("A new recovery draft includes the source transcript and unsent drafts", app: app)
        if UIDevice.current.userInterfaceIdiom == .pad {
            app.navigationBars["新建会话"].buttons["取消"].tap()
        } else {
            app.navigationBars["新建会话"].buttons["BackButton"].tap()
        }
        XCTAssertTrue(app.buttons["继续原会话"].firstMatch.waitForExistence(timeout: 8))
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "原会话未发送草稿")
    }

    func testRemoteRecoveryRechecksBeforeEndingTheOldTurn() throws {
        let app = try openRemoteRecoveryFixture("end")
        let check = app.buttons["重试恢复"].firstMatch
        XCTAssertTrue(check.waitForExistence(timeout: 12), app.debugDescription)
        check.tap()
        let end = app.buttons["结束旧轮记录…"].firstMatch
        XCTAssertTrue(end.waitForExistence(timeout: 10), app.debugDescription)
        end.tap()
        let alert = app.alerts["结束旧轮记录？"]
        XCTAssertTrue(alert.waitForExistence(timeout: 5), app.debugDescription)
        capture("Ending a record requires explicit confirmation", app: app)
        alert.buttons["取消"].tap()
        XCTAssertTrue(end.waitForExistence(timeout: 5))
        end.tap()
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        alert.buttons["结束旧轮记录"].tap()
        XCTAssertTrue(app.buttons["在新会话接续"].firstMatch.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "原会话未发送草稿")
        capture("The old turn is closed and offers a new-session draft", app: app)
    }

    func testPhoneConversationTitleMatchesDesignColumn() {
        let app = launch("--preview-session")
        let title = app.navigationBars.staticTexts["conversation-title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 8))
        let scope = app.navigationBars.staticTexts["Shioji iOS · Studio Display Mac"].firstMatch
        XCTAssertTrue(scope.exists)
        capture("Two-line conversation title", app: app)
        XCTAssertEqual(title.frame.minX, 76, accuracy: 1)
        XCTAssertEqual(scope.frame.minY - title.frame.maxY, 4, accuracy: 2)
        XCTAssertGreaterThanOrEqual(scope.frame.minX, title.frame.minX)
        XCTAssertLessThan(scope.frame.minX - title.frame.minX, 24)
        XCTAssertLessThanOrEqual(scope.frame.maxY, app.navigationBars.firstMatch.frame.maxY)
    }

    func testWorkPagesUseConversationHeaders() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        for (route, name, subtitle) in [
            ("child-agent", "连接验证", "Mac mini · 测试断线重连"),
            ("background-task", "重连回归", "Mac mini · Shioji · feat/reconnect"),
        ] {
            let app = launch("--preview-board-" + route)
            let title = app.navigationBars.staticTexts["conversation-title"].firstMatch
            XCTAssertTrue(title.waitForExistence(timeout: 8), route)
            XCTAssertEqual(title.label, name)
            let back = app.navigationBars.buttons["BackButton"].firstMatch
            XCTAssertTrue(back.exists)
            if tablet {
                let sidebar = app.buttons["会话视图"].firstMatch
                XCTAssertTrue(sidebar.exists)
                XCTAssertLessThan(sidebar.frame.maxX, back.frame.minX)
            }
            XCTAssertGreaterThanOrEqual(title.frame.minX - back.frame.maxX, 8)
            XCTAssertLessThan(title.frame.minX - back.frame.maxX, 30)
            let scope = app.navigationBars.staticTexts[subtitle].firstMatch
            XCTAssertTrue(scope.exists)
            XCTAssertEqual(scope.frame.minY - title.frame.maxY, 4, accuracy: 1)
            let barBottom = app.navigationBars.firstMatch.frame.maxY
            let repeatedTitle = app.staticTexts.matching(NSPredicate(format: "label == %@", name)).allElementsBoundByIndex
                .contains { $0.isHittable && $0.frame.minY >= barBottom }
            XCTAssertFalse(repeatedTitle)
            XCTAssertTrue(app.staticTexts["运行中"].exists)
            if route == "background-task" {
                XCTAssertTrue(app.staticTexts["42 秒"].exists)
            }
            capture("Work header " + route, app: app)
            app.terminate()
        }
    }

    func testConversationFootersFollowApprovedGeometry() {
        let app = launch("--preview-board-chat")
        let copyReply = app.buttons["复制回复"].firstMatch
        XCTAssertTrue(copyReply.waitForExistence(timeout: 8))
        let replyTime = app.staticTexts["09:38"].firstMatch
        let duration = app.staticTexts["用时 2分18秒"].firstMatch
        let copyUser = app.buttons["复制消息"].firstMatch
        let userTime = app.staticTexts["09:36"].firstMatch
        XCTAssertTrue(replyTime.exists)
        XCTAssertTrue(userTime.exists)
        XCTAssertEqual(copyReply.frame.width, 44, accuracy: 1)
        XCTAssertEqual(copyReply.frame.height, 44, accuracy: 1)
        let fork = app.buttons["从这里分叉"].firstMatch
        let details = app.buttons["回复详情"].firstMatch
        XCTAssertEqual(fork.frame.midX - copyReply.frame.midX, details.frame.midX - fork.frame.midX, accuracy: 1)
        XCTAssertEqual(replyTime.frame.minX - (details.frame.midX - 5 + 7.5), 24, accuracy: 1)
        XCTAssertTrue(duration.exists)
        XCTAssertEqual(duration.frame.minX - replyTime.frame.maxX, 24, accuracy: 1)
        XCTAssertEqual(replyTime.frame.midY - copyReply.frame.minY, 12, accuracy: 1)
        let replyBody = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "接下来验证恢复连接后的状态交接")).firstMatch
        XCTAssertTrue(replyBody.exists)
        XCTAssertEqual(copyReply.frame.midX + 5 - 7.5, replyBody.frame.minX, accuracy: 1)
        XCTAssertEqual(copyReply.frame.minY - replyBody.frame.maxY, 4, accuracy: 1)
        let edit = app.buttons["编辑消息"].firstMatch
        XCTAssertEqual(edit.frame.midX - copyUser.frame.midX, 44, accuracy: 1)
        XCTAssertEqual(userTime.frame.minX - (edit.frame.midX - 2.5 + 7.5), 24, accuracy: 1)
        XCTAssertEqual(copyUser.frame.width, 44, accuracy: 1)
        XCTAssertEqual(copyUser.frame.height, 44, accuracy: 1)
        XCTAssertEqual(userTime.frame.midY - copyUser.frame.minY, 12, accuracy: 1)
        let userBody = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "帮我测试 Worker 断线重连")).firstMatch
        XCTAssertTrue(userBody.exists)
        XCTAssertEqual(copyUser.frame.minY - userBody.frame.maxY, 13, accuracy: 1)
        XCTAssertTrue(app.staticTexts["进行中"].exists)
        capture("Conversation footers", app: app)
        details.coordinate(withNormalizedOffset: CGVector(dx: 17.0 / 44, dy: 12.0 / 44)).tap()
        let cached = app.descendants(matching: .any).matching(identifier: "缓存命中 1,024").firstMatch
        XCTAssertTrue(cached.waitForExistence(timeout: 4), app.debugDescription)
    }

    func testRequestDisclosuresShareTranscriptSpacing() {
        for route in ["request-question", "request-access"] {
            let app = launch("--preview-board-" + route)
            let tools = app.buttons["1 次工具调用"].firstMatch
            XCTAssertTrue(tools.waitForExistence(timeout: 8), route)
            let source = app.buttons["查看请求来源"].firstMatch
            XCTAssertTrue(source.exists)
            XCTAssertEqual(tools.frame.height, 24, accuracy: 1)
            XCTAssertEqual(source.frame.height, tools.frame.height, accuracy: 1)
            XCTAssertEqual(source.frame.minY - tools.frame.maxY, 4, accuracy: 1)
            let pending = app.staticTexts["等待处理"].firstMatch
            XCTAssertTrue(pending.exists)
            XCTAssertEqual(source.frame.midY - tools.frame.midY, pending.frame.midY - source.frame.midY, accuracy: 1)
            XCTAssertEqual(source.frame.width, tools.frame.width, accuracy: 1)
            capture("Compact transcript " + route, app: app)
            app.terminate()
        }
    }

    func testWorkspaceChangesAreAvailableFromConversationActivity() {
        let app = launch("--preview-board-chat")
        let activity = app.navigationBars.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["工作区改动"].exists)
        XCTAssertFalse(app.buttons["文件改动"].exists)
        capture("Conversation with its compact editor", app: app)
        activity.tap()
        let changes = app.buttons["工作区改动"].firstMatch
        XCTAssertTrue(changes.waitForExistence(timeout: 5))
        capture("Workspace changes in conversation activity", app: app)
        changes.tap()
        XCTAssertTrue(app.navigationBars["改动"].waitForExistence(timeout: 5))
    }

    func testCurrentGoalKeyboardHasClearance() {
        let app = launch("--preview-board-session-goal")
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        composer.tap()
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
        let docked = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            keyboard.frame.height > 200 && keyboard.frame.minY < app.frame.maxY - 180
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [docked], timeout: 5), .completed)
        let send = app.buttons["加入待发队列"].firstMatch
        XCTAssertTrue(send.exists)
        XCTAssertGreaterThanOrEqual(keyboard.frame.minY - send.frame.maxY, 8)
        capture("Docked software keyboard and composer clearance", app: app)
    }

    func testAppearancePreferencesUpdateTheTranscriptAndSurviveRelaunch() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        func appearance() {
            let settings = app.buttons["设置"].firstMatch
            XCTAssertTrue(settings.waitForExistence(timeout: 8))
            settings.tap()
            let entry = app.buttons["输入与外观"].firstMatch
            XCTAssertTrue(entry.waitForExistence(timeout: 6))
            entry.tap()
            XCTAssertTrue(app.navigationBars["输入与外观"].waitForExistence(timeout: 6))
        }
        func choose(_ name: String) {
            let option = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", name)).firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 5), name)
            option.tap()
        }
        let size = app.buttons["回复字号"].firstMatch
        let font = app.buttons["代码字体"].firstMatch
        let motion = app.buttons["减少动态效果"].firstMatch
        let preview = app.buttons["对话预览"].firstMatch
        let sample = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "已保存改动。下次运行会采用新配置。")).firstMatch
        app.launch()
        appearance()
        XCTAssertTrue(font.waitForExistence(timeout: 6), "The code font must be configurable")
        XCTAssertTrue(motion.exists)
        XCTAssertEqual(size.value as? String, "14 pt")
        XCTAssertEqual(font.value as? String, "系统等宽字体")
        XCTAssertEqual(motion.value as? String, "跟随系统")
        preview.tap()
        XCTAssertTrue(sample.waitForExistence(timeout: 6))
        let original = sample.frame.height
        capture("Appearance preview using system type", app: app)
        size.tap()
        choose("19 pt")
        XCTAssertEqual(size.value as? String, "19 pt")
        XCTAssertGreaterThan(sample.frame.height, original + 3)
        font.tap()
        choose("Courier New")
        XCTAssertEqual(font.value as? String, "Courier New")
        motion.tap()
        choose("开启")
        XCTAssertEqual(motion.value as? String, "开启")
        capture("Appearance preview with custom size and code font", app: app)
        app.terminate()
        app.launch()
        appearance()
        XCTAssertEqual(size.value as? String, "19 pt")
        XCTAssertEqual(font.value as? String, "Courier New")
        XCTAssertEqual(motion.value as? String, "开启")
        preview.tap()
        XCTAssertTrue(sample.waitForExistence(timeout: 6))
        XCTAssertGreaterThan(sample.frame.height, original + 3)
    }

    func testInterfaceZoomSupportsTouchAndPersists() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("")
        func openAppearance() {
            let settings = app.buttons["设置"].firstMatch
            XCTAssertTrue(settings.waitForExistence(timeout: 8))
            settings.tap()
            let row = app.buttons["输入与外观"].firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 6))
            row.tap()
        }
        openAppearance()
        let settings = app.navigationBars["输入与外观"].firstMatch
        let selection = app.buttons["界面缩放"].firstMatch
        capture("Appearance before interface zoom", app: app)
        XCTAssertTrue(selection.waitForExistence(timeout: 5))
        let original = selection.value as? String
        let defaultFrame = settings.frame
        func choose(_ percentage: String) {
            selection.tap()
            let option = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", percentage)).firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 5), percentage)
            option.tap()
            XCTAssertEqual(selection.value as? String, percentage)
        }
        if original != "100%" { choose("100%") }
        let theme = app.buttons["主题"].firstMatch
        let height = theme.frame.height
        choose("125%")
        XCTAssertGreaterThan(theme.frame.height, height + 7)
        XCTAssertEqual(settings.frame.height, defaultFrame.height, accuracy: 1)
        capture("Appearance at 125 percent zoom", app: app)
        app.terminate()
        app.launch()
        openAppearance()
        XCTAssertTrue(selection.waitForExistence(timeout: 5))
        XCTAssertEqual(selection.value as? String, "125%")
        choose("75%")
        XCTAssertGreaterThanOrEqual(selection.frame.height, 43.5)
        capture("Appearance at minimum zoom", app: app)
        choose("150%")
        XCTAssertGreaterThanOrEqual(theme.frame.height, height * 1.49)
        XCTAssertEqual(settings.frame.height, defaultFrame.height, accuracy: 1)
        capture("Appearance at maximum zoom", app: app)
        if let original { choose(original) }
    }

    func testInterfaceZoomShortcutsKeepDraftsAndNativeContextMenus() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch(tablet ? "--preview-board-pad-parallel" : "--preview-session")
        let primary = app.textFields["消息"].firstMatch
        XCTAssertTrue(primary.waitForExistence(timeout: 8))
        primary.tap()
        app.typeText("Keep left draft")
        let editing = tablet ? app.textFields["右侧会话草稿"].firstMatch : primary
        if tablet {
            XCTAssertTrue(editing.exists)
            editing.tap()
            app.typeText("Keep right draft")
        }
        let add = app.buttons[tablet ? "添加右侧附件" : "添加附件"].firstMatch
        XCTAssertTrue(add.exists)
        let before = add.frame.height
        app.typeKey("=", modifierFlags: .command)
        let zoomed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in add.frame.height > before + 1 }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [zoomed], timeout: 5), .completed)
        app.typeText(" after zoom")
        let original = tablet ? "Keep right draft" : "Keep left draft"
        XCTAssertEqual(editing.value as? String, original + " after zoom")
        if tablet { XCTAssertEqual(primary.value as? String, "Keep left draft") }
        capture("Conversation after keyboard zoom", app: app)
        add.tap()
        XCTAssertTrue(app.buttons["从文件选择附件"].firstMatch.waitForExistence(timeout: 6), app.debugDescription)
    }

    func testInterfaceZoomKeepsTheArchivePreviewBoundToItsRow() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-archive")
        let row = app.buttons["旧版 SSH 配置迁移"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 8))
        let height = row.frame.height
        app.typeKey("=", modifierFlags: .command)
        app.typeKey("=", modifierFlags: .command)
        let enlarged = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in row.frame.height > height + 3 }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [enlarged], timeout: 5), .completed)
        row.press(forDuration: 0.8)
        XCTAssertTrue(app.staticTexts["已保留旧配置的备份。"].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any)["恢复会话"].firstMatch.exists)
        capture("Archived conversation preview at 110 percent zoom", app: app)
    }

    func testInterfaceZoomKeepsTheReadingPosition() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-transcript-history")
        XCTAssertTrue(app.staticTexts["稍后的消息 25"].waitForExistence(timeout: 8))
        let top = app.coordinate(withNormalizedOffset: CGVector(dx: 0.64, dy: 0.24))
        let bottom = app.coordinate(withNormalizedOffset: CGVector(dx: 0.64, dy: 0.69))
        top.press(forDuration: 0.05, thenDragTo: bottom, withVelocity: .slow, thenHoldForDuration: 0.35)
        let latest = app.buttons["回到最新消息"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 6))
        let messages = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "稍后的消息 "))
        let readingArea = app.frame.minY + 155 ... app.frame.midY
        guard let item = messages.allElementsBoundByIndex.first(where: { readingArea.contains($0.frame.midY) }) else {
            return XCTFail("No historical message in the visible reading area")
        }
        let anchor = app.staticTexts[item.label].firstMatch
        let before = anchor.frame
        app.typeKey("=", modifierFlags: .command)
        let enlarged = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in anchor.frame.height > before.height + 0.4 }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [enlarged], timeout: 5), .completed)
        XCTAssertTrue(latest.exists)
        XCTAssertEqual(anchor.frame.midY, before.midY, accuracy: 30)
        app.typeKey("-", modifierFlags: .command)
        XCTAssertEqual(anchor.frame.height, before.height, accuracy: 1)
        app.typeKey("=", modifierFlags: .command)
        app.typeKey("0", modifierFlags: .command)
        XCTAssertEqual(anchor.frame.height, before.height, accuracy: 1)
        XCTAssertTrue(latest.exists)
    }

    func testInterfaceFontUpdatesConversationPreviewAndPersists() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        func openAppearance() {
            let settings = app.buttons["设置"].firstMatch
            XCTAssertTrue(settings.waitForExistence(timeout: 8))
            settings.tap()
            let appearance = app.buttons["输入与外观"].firstMatch
            XCTAssertTrue(appearance.waitForExistence(timeout: 8))
            appearance.tap()
        }
        func openDraft() -> XCUIElement {
            let sessions = app.buttons["会话"].firstMatch
            XCTAssertTrue(sessions.waitForExistence(timeout: 8))
            sessions.tap()
            let compose = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(compose.waitForExistence(timeout: 8))
            compose.tap()
            let message = app.textFields["消息"].firstMatch
            XCTAssertTrue(message.waitForExistence(timeout: 8))
            return message
        }
        func closeDraft() {
            let bar = app.navigationBars["新建会话"].firstMatch
            let back = bar.buttons[UIDevice.current.userInterfaceIdiom == .pad ? "取消" : "BackButton"].firstMatch
            XCTAssertTrue(back.waitForExistence(timeout: 5))
            back.tap()
        }
        let font = app.buttons["界面字体"].firstMatch
        let codeFont = app.buttons["代码字体"].firstMatch
        let preview = app.buttons["对话预览"].firstMatch
        let sample = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "下次运行会采用新配置。")).firstMatch
        app.launch()
        let draft = openDraft()
        draft.tap()
        let draftText = "Review the remaining changes."
        app.typeText(draftText)
        XCTAssertEqual(draft.value as? String, draftText)
        closeDraft()
        openAppearance()
        capture("Appearance with system font", app: app)
        XCTAssertTrue(font.waitForExistence(timeout: 5), "Appearance must offer an interface font picker")
        XCTAssertEqual(font.value as? String, "系统字体")
        let originalCodeFont = codeFont.value as? String
        preview.tap()
        XCTAssertTrue(sample.waitForExistence(timeout: 6))
        let systemSample = sample.screenshot().pngRepresentation

        font.tap()
        let search = app.textFields["搜索字体"].firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        app.typeText("georg")
        let georgia = app.buttons["Georgia"].firstMatch
        XCTAssertTrue(georgia.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["Courier New"].exists)
        capture("Filtering installed interface fonts", app: app)
        georgia.tap()
        XCTAssertEqual(font.value as? String, "Georgia")
        XCTAssertEqual(codeFont.value as? String, originalCodeFont)
        XCTAssertTrue(sample.waitForExistence(timeout: 5))
        XCTAssertNotEqual(sample.screenshot().pngRepresentation, systemSample, "The preview must render in the selected family")
        capture("Conversation preview in Georgia", app: app)

        app.navigationBars["输入与外观"].buttons["BackButton"].firstMatch.tap()
        XCTAssertEqual(openDraft().value as? String, draftText)
        capture("The existing draft with the new interface font", app: app)
        closeDraft()
        app.terminate()
        app.launch()
        openAppearance()
        XCTAssertEqual(font.value as? String, "Georgia")
        font.tap()
        let system = app.buttons["系统字体"].firstMatch
        XCTAssertTrue(system.waitForExistence(timeout: 5))
        system.tap()
        XCTAssertEqual(font.value as? String, "系统字体")
        XCTAssertEqual(codeFont.value as? String, originalCodeFont)
    }

    func testSettingsSearchOpensTheMatchingControlAndKeepsTheQuery() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview")
        app.buttons["设置"].firstMatch.tap()
        let search = app.textFields["搜索设置"].firstMatch
        capture("Settings before searching", app: app)
        XCTAssertTrue(search.waitForExistence(timeout: 8))
        XCTAssertFalse(app.keyboards.firstMatch.exists)
        search.tap()
        app.typeText("介入")
        let match = app.buttons["立即介入当前轮"].firstMatch
        XCTAssertTrue(match.waitForExistence(timeout: 6))
        XCTAssertFalse(app.buttons["通知偏好"].exists)
        capture("Settings matching the current query", app: app)
        match.tap()

        let appearance = app.navigationBars["输入与外观"].firstMatch
        XCTAssertTrue(appearance.waitForExistence(timeout: 6))
        XCTAssertTrue(match.isHittable)
        XCTAssertLessThan(match.frame.maxY, app.frame.maxY - 30)
        XCTAssertNotNil(match.value as? String)
        XCTAssertFalse(app.keyboards.firstMatch.exists)
        capture("Directly at the matching keyboard setting", app: app)
        appearance.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        XCTAssertEqual(search.value as? String, "介入")
        XCTAssertTrue(match.exists)

        let clear = app.buttons["清除设置搜索"].firstMatch
        clear.tap()
        XCTAssertTrue(app.buttons["通知偏好"].firstMatch.exists)
        search.tap()
        app.typeText("没有这个设置")
        XCTAssertTrue(app.descendants(matching: .any)["没有匹配的设置"].firstMatch.waitForExistence(timeout: 6))
        clear.tap()
        app.typeText("测试通知")
        let notify = app.buttons["发送测试通知"].firstMatch
        XCTAssertTrue(notify.waitForExistence(timeout: 6))
        notify.tap()
        XCTAssertTrue(app.navigationBars["通知"].firstMatch.waitForExistence(timeout: 6))
        XCTAssertTrue(notify.isHittable)
        XCTAssertFalse(notify.isEnabled)
    }

    func testSettingsSearchOpensPerDeviceArchiveRetentionAndLicenses() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-archive")
        let archive = app.navigationBars["已归档"].firstMatch
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        archive.buttons["BackButton"].firstMatch.tap()
        app.buttons["设置"].firstMatch.tap()
        let search = app.textFields["搜索设置"].firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        search.tap()
        app.typeText("保留期")
        let match = app.buttons["归档保留期"].firstMatch
        XCTAssertTrue(match.waitForExistence(timeout: 6))
        match.tap()
        XCTAssertTrue(archive.waitForExistence(timeout: 6))
        let policy = app.buttons["Mac mini"].firstMatch
        XCTAssertTrue(policy.isHittable, app.debugDescription)
        XCTAssertEqual(policy.value as? String, "30 天")
        capture("Settings search reveals per-device archive retention", app: app)
        policy.tap()
        app.descendants(matching: .any)["14 天"].firstMatch.tap()
        XCTAssertEqual(policy.value as? String, "14 天")
        archive.buttons["BackButton"].firstMatch.tap()
        XCTAssertEqual(search.value as? String, "保留期")
        app.buttons["清除设置搜索"].firstMatch.tap()
        search.tap()
        app.typeText("第三方")
        app.buttons["第三方许可"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["第三方许可"].firstMatch.waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["github.com/egoist/mygo"].firstMatch.exists)
    }

    func testSettingsShowsHardwareShortcutsAndLinksToMessageBindings() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview")
        app.buttons["设置"].firstMatch.tap()
        capture("Settings before opening hardware shortcuts", app: app)
        let entry = app.buttons["快捷键"].firstMatch
        guard entry.waitForExistence(timeout: 5) else { return XCTFail("Settings must offer hardware shortcuts") }
        entry.tap()
        let page = app.navigationBars["快捷键"].firstMatch
        XCTAssertTrue(page.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["⌘ K"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["⌘ ⇧ J"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["⌘ ⇧ H"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts["⌘ F"].firstMatch.exists)
        capture("Hardware shortcuts reference", app: app)

        let edit = app.buttons["设置消息输入按键"].firstMatch
        XCTAssertTrue(edit.isHittable)
        edit.tap()
        let input = app.navigationBars["输入与外观"].firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["发送或加入队列"].firstMatch.isHittable)
        input.buttons["BackButton"].firstMatch.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 6))
        page.buttons["BackButton"].firstMatch.tap()

        let search = app.textFields["搜索设置"].firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 6))
        search.tap()
        app.typeText("搜索会话")
        let matching = app.buttons["搜索会话"].firstMatch
        XCTAssertTrue(matching.waitForExistence(timeout: 6))
        XCTAssertEqual(matching.value as? String, "⌘ K")
        matching.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["⌘ K"].firstMatch.isHittable)
        XCTAssertFalse(app.keyboards.firstMatch.exists)
        page.buttons["BackButton"].firstMatch.tap()
        XCTAssertEqual(search.value as? String, "搜索会话")
    }

    func testCurrentSettingsRoot() {
        let app = launch("--preview-board-inbox")
        let settings = app.buttons["设置"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        settings.tap()
        XCTAssertTrue(app.buttons["输入与外观"].waitForExistence(timeout: 8))
        capture("Settings landing page", app: app)
    }

    func testSettingsShowsBundledThirdPartyLicenses() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-inbox")
        let settings = app.buttons["设置"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        settings.tap()
        let licenses = app.buttons["第三方许可"].firstMatch
        XCTAssertTrue(licenses.waitForExistence(timeout: 6))
        let version = ProcessInfo.processInfo.environment["SHIOJI_TEST_MOBILE_VERSION"] ?? "0.1.0"
        XCTAssertTrue(app.staticTexts[version].firstMatch.exists)
        capture("Third-party licenses entry in Settings", app: app)

        licenses.tap()
        let index = app.navigationBars["第三方许可"].firstMatch
        XCTAssertTrue(index.waitForExistence(timeout: 8))
        let first = app.buttons["filippo.io/edwards25519"].firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 6))
        capture("Bundled license list", app: app)
        first.tap()
        let notice = app.navigationBars["filippo.io/edwards25519"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["v1.2.0"].firstMatch.exists)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Copyright (c) 2009 The Go Authors.")).firstMatch.exists)
        capture("Full bundled notice", app: app)
        notice.buttons["BackButton"].tap()

        let longNotice = app.buttons["modernc.org/sqlite"].firstMatch
        for _ in 0..<10 {
            if longNotice.isHittable { break }
            app.swipeUp()
        }
        XCTAssertTrue(longNotice.isHittable, "The list must scroll to modules outside the first screen")
        longNotice.tap()
        XCTAssertTrue(app.navigationBars["modernc.org/sqlite"].waitForExistence(timeout: 8))
        let copy = app.buttons["复制许可"].firstMatch
        XCTAssertTrue(copy.waitForExistence(timeout: 6))
        capture("Long bundled SQLite notice", app: app)
        copy.tap()
    }

    func testFinishedReplyKeepsFooterWhenHistoryLacksMetadata() {
        let app = launch("--preview-session")
        XCTAssertTrue(app.buttons["复制回复"].firstMatch.waitForExistence(timeout: 8))
        let edit = app.buttons["编辑消息"].firstMatch
        XCTAssertTrue(edit.exists)
        XCTAssertFalse(edit.isEnabled)
        let fork = app.buttons["从这里分叉"].firstMatch
        XCTAssertTrue(fork.exists)
        XCTAssertFalse(fork.isEnabled)
        XCTAssertTrue(app.staticTexts["完成时间未知"].exists)
        XCTAssertTrue(app.staticTexts["用时 —"].exists)
        app.buttons["回复详情"].firstMatch.tap()
        for text in ["输入 —", "输出 —", "缓存命中 —"] {
            let row = app.descendants(matching: .any).matching(identifier: text).firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 4), app.debugDescription)
        }
    }

    func testContextCompactionPreservesDraftAndRetriesTheIndependentStatus() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-context-compaction")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        capture("The conversation before manual compaction", app: app)
        let context = app.buttons["上下文用量"]
        guard context.waitForExistence(timeout: 4) else { return XCTFail("A session has no context usage or compaction entry") }
        let editCount = app.buttons.matching(identifier: "编辑消息").count
        editor.tap()
        app.typeText("keep this draft")
        context.tap()
        capture("Context usage and manual action", app: app)
        app.descendants(matching: .any).matching(identifier: "压缩上下文").firstMatch.tap()
        let retry = app.buttons["重试压缩"]
        XCTAssertTrue(retry.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "keep this draft")
        XCTAssertFalse(app.staticTexts["/compact"].exists)
        capture("Independent compaction failure", app: app)

        retry.tap()
        let completed = app.staticTexts["上下文已压缩"]
        XCTAssertTrue(completed.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "keep this draft")
        XCTAssertLessThanOrEqual(app.buttons.matching(identifier: "编辑消息").count, editCount)
        capture("Independent compaction completion", app: app)
    }

    func testCompactCommandChecksTheOriginalReceiptAndPreservesNewTyping() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-context-compaction-receipt")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("/comp")
        let command = app.buttons["压缩上下文命令"]
        XCTAssertTrue(command.waitForExistence(timeout: 5))
        capture("Built-in compaction command candidate", app: app)
        command.tap()
        let verify = app.buttons["核对压缩结果"]
        XCTAssertTrue(verify.waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, "/compact")
        XCTAssertFalse(command.exists)
        capture("Unknown compaction needs the original receipt", app: app)
        let work = app.buttons["会话活动"]
        XCTAssertTrue(work.exists)
        editor.tap()
        app.typeText(" later")
        let newerDraft = editor.value as? String
        XCTAssertNotEqual(newerDraft, "/compact")
        verify.tap()
        XCTAssertTrue(app.staticTexts["上下文已压缩"].waitForExistence(timeout: 8))
        XCTAssertEqual(editor.value as? String, newerDraft)
        XCTAssertFalse(verify.exists)
    }

    func testHistoricalCompactionDoesNotReappearAtTheLatestPage() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-context-compaction-history")
        let reply = app.staticTexts["后续回复留在原来的位置。"]
        XCTAssertTrue(reply.waitForExistence(timeout: 8), app.debugDescription)
        capture("Latest conversation page with an older compaction", app: app)
        XCTAssertFalse(app.staticTexts["上下文已压缩"].exists)
    }

    func testCompactionStatusFollowsItsOwnCommandBeforeLaterReply() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-context-compaction-order")
        let command = app.staticTexts["压缩上下文"]
        let completed = app.staticTexts["上下文已压缩"]
        let reply = app.staticTexts["后续回复留在原来的位置。"]
        XCTAssertTrue(reply.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(command.exists)
        XCTAssertEqual(app.staticTexts.matching(identifier: "上下文已压缩").count, 1)
        capture("Compaction command and completion around a later reply", app: app)
        XCTAssertLessThan(command.frame.midY, completed.frame.midY)
        XCTAssertLessThan(completed.frame.midY, reply.frame.midY)
    }

    func testAutomaticCompactionHasOneLiveStatusWithoutAnExtraReceipt() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-context-compaction-auto")
        let status = app.staticTexts["正在压缩上下文"]
        XCTAssertTrue(status.waitForExistence(timeout: 8))
        XCTAssertEqual(app.staticTexts.matching(identifier: "正在压缩上下文").count, 1)
        XCTAssertFalse(app.staticTexts["进行中"].exists)
        XCTAssertEqual(app.buttons.matching(identifier: "回复详情").count, 1)
        capture("Live automatic context compaction", app: app)
    }

    func testWaitToolKeepsDurationAndResultOnTheSameRow() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-wait")
        let group = app.buttons["2 次工具调用"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 8))
        group.tap()
        let duration = app.staticTexts["45.0 秒"]
        XCTAssertTrue(duration.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["30.0 秒"].exists)
        capture("Wait tools with elapsed time and result", app: app)
        let completed = app.staticTexts["已完成"]
        let interrupted = app.staticTexts["已中断"]
        XCTAssertTrue(completed.exists)
        XCTAssertTrue(interrupted.exists)
        XCTAssertLessThan(duration.frame.maxX, completed.frame.minX)
        XCTAssertEqual(duration.frame.midY, completed.frame.midY, accuracy: 1)
        XCTAssertEqual(app.staticTexts["30.0 秒"].frame.midY, interrupted.frame.midY, accuracy: 1)
        XCTAssertFalse(app.buttons["等待"].exists)
    }

    func testToolOnlyTurnsHaveIndependentReceipts() {
        let app = launch("--preview-board-transcript-turns")
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 8))
        capture("Tool-only turns", app: app)
        let completed = app.staticTexts["本轮完成"]
        XCTAssertTrue(completed.waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["用时 18秒"].exists)
        let failure = app.staticTexts["本轮失败"]
        XCTAssertTrue(failure.exists)
        XCTAssertTrue(app.staticTexts["工作区校验失败。"].exists)
        XCTAssertLessThan(completed.frame.minY, failure.frame.minY)
        XCTAssertFalse(app.buttons["复制回复"].exists)
        let copyOutput = app.buttons["复制工具输出"].firstMatch
        XCTAssertTrue(copyOutput.exists)
        XCTAssertTrue(copyOutput.isEnabled)
        let fork = app.buttons["从这里分叉"].firstMatch
        let info = app.buttons["回复详情"].firstMatch
        let duration = app.staticTexts["用时 18秒"].firstMatch
        // The submitted message and the reply can display the same minute.
        let times = app.staticTexts.matching(identifier: "09:36").allElementsBoundByIndex
        guard let time = times.first(where: { abs($0.frame.midY - duration.frame.midY) <= 1 }) else {
            return XCTFail("The tool receipt should show its completion time beside its duration")
        }
        XCTAssertLessThan(copyOutput.frame.midX, fork.frame.midX)
        XCTAssertLessThan(fork.frame.midX, info.frame.midX)
        XCTAssertLessThan(info.frame.midX, time.frame.midX)
        XCTAssertLessThan(time.frame.midX, duration.frame.midX)
        let group = app.buttons["1 次工具调用"].firstMatch
        group.tap()
        XCTAssertTrue(app.buttons["核对重连逻辑"].exists)
        XCTAssertEqual(app.staticTexts.matching(identifier: "本轮完成").count, 1)
        app.buttons["回复详情"].firstMatch.tap()
        for text in ["输入 560", "输出 0", "缓存命中 —"] {
            XCTAssertTrue(app.descendants(matching: .any).matching(identifier: text).firstMatch.waitForExistence(timeout: 5))
        }
    }

    func testSentReferencesKeepInlinePlacementAndOpenTheirSnapshot() {
        let app = launch("--preview-board-transcript-turns")
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 8))
        capture("Sent file and Skill references", app: app)
        let body = app.staticTexts["用 review-check 检查 connection.go。"].firstMatch
        let file = app.buttons["预览引用：connection.go"]
        XCTAssertTrue(body.waitForExistence(timeout: 5))
        XCTAssertTrue(file.exists)
        XCTAssertGreaterThanOrEqual(file.frame.minX, body.frame.minX - 1)
        XCTAssertGreaterThanOrEqual(file.frame.minY, body.frame.minY - 2)
        XCTAssertLessThanOrEqual(file.frame.maxY, body.frame.maxY + 2)
        XCTAssertTrue(file.frame.minX > body.frame.minX + 1 || file.frame.minY > body.frame.minY + 2)
        file.tap()
        XCTAssertTrue(app.navigationBars["附件预览"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["connection.go"].exists)
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(file.waitForExistence(timeout: 5))
        app.buttons["复制消息"].firstMatch.tap()
    }

    func testComposerInsertsReferencesAtTheCaretAndDeletesThemAsObjects() {
        let app = launch("--preview-board-draft-references")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("check @conn")
        capture("Composer before file reference", app: app)
        let file = app.buttons["引用文件 connection.go"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 5))
        file.tap()
        XCTAssertEqual(editor.value as? String, "check \u{fffc}")
        app.typeText(" /review")
        let skill = app.buttons["引用 Skill review-check"].firstMatch
        XCTAssertTrue(skill.waitForExistence(timeout: 5))
        skill.tap()
        XCTAssertEqual(editor.value as? String, "check \u{fffc} \u{fffc}")
        XCTAssertFalse(app.buttons["Cut"].exists || app.menuItems["Cut"].exists)
        capture("Composer inline file and Skill", app: app)
        app.typeText(XCUIKeyboardKey.delete.rawValue)
        XCTAssertEqual(editor.value as? String, "check \u{fffc} ")
        app.typeText("again")
        XCTAssertEqual(editor.value as? String, "check \u{fffc} again")
    }

    func testReferenceKeyboardScrollsTheCandidateAboveTheComposer() {
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-draft-references-keyboard")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("check @ref")
        let one = app.buttons.matching(NSPredicate(format: "label == %@ AND value == %@", "引用文件 reference.go", "Shioji/one")).firstMatch
        let two = app.buttons.matching(NSPredicate(format: "label == %@ AND value == %@", "引用文件 reference.go", "Shioji/two")).firstMatch
        XCTAssertTrue(one.waitForExistence(timeout: 5))
        XCTAssertTrue(two.isHittable)
        let fifth = app.buttons["引用文件 reference_five.go"].firstMatch
        for _ in 0..<4 {
            app.typeKey(XCUIKeyboardKey.downArrow.rawValue, modifierFlags: [])
        }
        XCTAssertTrue(fifth.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(fifth.isSelected)
        let revealed = NSPredicate { _, _ in fifth.isHittable && fifth.frame.maxY <= editor.frame.minY }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: revealed, object: nil)], timeout: 4), .completed,
                       "The selected row must scroll fully into view above the composer")
        capture("Arrow selection reveals a file below the initial viewport", app: app)
        fifth.tap()
        XCTAssertEqual(editor.value as? String, "check \u{fffc}")
        app.typeText(" next")
        XCTAssertEqual(editor.value as? String, "check \u{fffc} next")
    }

    func testComposerSendStaysAboveTheKeyboardWithQueueAndReferences() {
        composerKeyboardClearance(board: "--preview-board-draft-references-keyboard", checkQueueScrolling: false)
    }

    func testComposerSendStaysAboveTheKeyboardWithLongQueueAndReferences() {
        composerKeyboardClearance(board: "--preview-board-draft-references-keyboard-long-queue", checkQueueScrolling: true)
    }

    private func composerKeyboardClearance(board: String, checkQueueScrolling: Bool) {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch(board)
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("check @ref")
        let candidate = app.buttons["引用文件 reference.go"].firstMatch
        XCTAssertTrue(candidate.waitForExistence(timeout: 5))
        let queue = app.buttons["待发消息操作"].firstMatch
        XCTAssertTrue(queue.exists)
        let keyboard = app.keyboards.firstMatch
        let docked = NSPredicate { _, _ in
            keyboard.exists && keyboard.frame.height > 200 && keyboard.frame.minY < app.frame.maxY - 180
        }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: docked, object: nil)], timeout: 5), .completed)
        for _ in 0..<4 {
            app.typeKey(XCUIKeyboardKey.downArrow.rawValue, modifierFlags: [])
        }
        let selected = app.buttons["引用文件 reference_five.go"].firstMatch
        XCTAssertTrue(selected.waitForExistence(timeout: 5))
        XCTAssertTrue(selected.isSelected)
        let inView = NSPredicate { _, _ in selected.isHittable && selected.frame.maxY <= editor.frame.minY }
        let visible = XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: inView, object: nil)], timeout: 4)
        let send = app.buttons.matching(NSPredicate(format: "label IN %@", ["发送选项", "加入待发队列", "发送"])).firstMatch
        XCTAssertTrue(send.exists)
        capture("Send control with a docked keyboard, queue and file candidates", app: app)
        if UIDevice.current.userInterfaceIdiom == .pad {
            // The iPad AX keyboard frame excludes the candidate bar. The
            // native tab bar already stops at the actual occluding edge.
            let composeTab = app.buttons["新建会话"].firstMatch
            XCTAssertTrue(composeTab.isHittable)
            XCTAssertLessThanOrEqual(send.frame.maxY, composeTab.frame.maxY,
                                     "send: \(send.frame), sidebar compose button: \(composeTab.frame), AX keyboard: \(keyboard.frame)")
        }
        XCTAssertGreaterThanOrEqual(keyboard.frame.minY - send.frame.maxY, 8,
                                    "keyboard: \(keyboard.frame), send: \(send.frame), editor: \(editor.frame), selected: \(selected.frame), queue: \(queue.frame)")
        XCTAssertEqual(visible, .completed, "The selected reference \(selected.frame) must fit above the editor \(editor.frame)")
        XCTAssertTrue(selected.isHittable, "Selected reference \(selected.frame); editor \(editor.frame); send \(send.frame)")
        if checkQueueScrolling {
            XCTAssertTrue(queue.isHittable, "The queue must remain accessible while editing")
            let viewport = app.descendants(matching: .any)["待发消息列表"].firstMatch
            let last = app.buttons["介入当前轮，第 4 条待发消息"].firstMatch
            XCTAssertTrue(viewport.exists)
            XCTAssertTrue(last.exists)
            func tailVisible() -> Bool {
                last.frame.midY >= viewport.frame.minY && last.frame.midY <= viewport.frame.maxY
            }
            XCTAssertFalse(tailVisible())
            let start = viewport.coordinate(withNormalizedOffset: CGVector(dx: 0.45, dy: 0.85))
            let end = viewport.coordinate(withNormalizedOffset: CGVector(dx: 0.45, dy: 0.12))
            for _ in 0..<6 where !tailVisible() {
                start.press(forDuration: 0.04, thenDragTo: end, withVelocity: .slow, thenHoldForDuration: 0)
            }
            capture("The shorter queue still scrolls with the keyboard onscreen", app: app)
            XCTAssertTrue(tailVisible(), "Viewport \(viewport.frame) must be able to scroll to the last item \(last.frame)")
        }
        XCTAssertTrue(send.isHittable)
        send.tap()
        XCTAssertTrue(app.descendants(matching: .any)["加入待发队列"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(editor.value as? String, "check @ref")
    }

    func testReferenceSearchMatchesSkillDescription() {
        continueAfterFailure = false
        let app = launch("--preview-board-draft-references-keyboard")
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("/deployment")
        let skill = app.buttons["引用 Skill release-check"].firstMatch
        XCTAssertTrue(skill.waitForExistence(timeout: 5))
        XCTAssertEqual(skill.value as? String, "Review a deployment plan")
        app.typeKey(XCUIKeyboardKey.tab.rawValue, modifierFlags: [])
        XCTAssertEqual(editor.value as? String, "\u{fffc}")
        app.typeText(" next")
        XCTAssertEqual(editor.value as? String, "\u{fffc} next")
    }

    func testQueuedReferencesReopenInTheEditorWithoutReplacingTheOrdinaryDraft() {
        let app = launch("--preview-board-draft-references")
        let queue = app.staticTexts["按 review-check 检查 connection.go 的边界"]
        XCTAssertTrue(app.buttons["待发消息操作"].firstMatch.waitForExistence(timeout: 8))
        capture("Queued reference preview", app: app)
        XCTAssertTrue(queue.exists)
        let editor = app.textFields["消息"].firstMatch
        editor.tap()
        app.typeText("ordinary draft")
        let actions = app.buttons["待发消息操作"].firstMatch
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
        let keyboardLayout = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            actions.isHittable && actions.frame.maxY < keyboard.frame.minY
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [keyboardLayout], timeout: 5), .completed)
        actions.tap()
        let edit = app.descendants(matching: .any).matching(identifier: "编辑").firstMatch
        capture("Queue actions", app: app)
        XCTAssertTrue(edit.waitForExistence(timeout: 4), app.debugDescription)
        edit.tap()
        XCTAssertTrue(app.buttons["保存待发消息"].waitForExistence(timeout: 5))
        XCTAssertEqual(editor.value as? String, "按 \u{fffc} 检查 \u{fffc} 的边界")
        capture("Queued references in the native editor", app: app)
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(app.buttons["保存待发消息"].waitForNonExistence(timeout: 4))
        XCTAssertEqual(editor.value as? String, "ordinary draft")
        XCTAssertTrue(queue.exists)
    }

    func testWorkerQueueSaveReleasesComposerAndCanRecoverAnEdit() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_QUEUE_EDIT_SESSION"] else {
            throw XCTSkip("Requires the isolated Worker with a delayed queue edit")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 16), app.debugDescription)
        session.tap()
        let composer = app.textFields["消息"].firstMatch
        let save = app.buttons["保存待发消息"]
        let queued = app.staticTexts["Queued draft"].firstMatch
        XCTAssertTrue(queued.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(composer.value as? String, "Ordinary draft")
        app.buttons["待发消息操作"].firstMatch.tap()
        let edit = app.descendants(matching: .any).matching(identifier: "编辑").firstMatch
        XCTAssertTrue(edit.waitForExistence(timeout: 5), app.debugDescription)
        edit.tap()
        XCTAssertTrue(save.waitForExistence(timeout: 5))
        XCTAssertEqual(composer.value as? String, "Queued draft")
        composer.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.35)).tap()
        app.typeText(" revised")
        XCTAssertEqual(composer.value as? String, "Queued draft revised")
        save.tap()
        capture("Immediately after saving a queued edit", app: app)
        XCTAssertTrue(save.waitForNonExistence(timeout: 2), "The ordinary composer should be available before the Worker replies")
        XCTAssertEqual(composer.value as? String, "Ordinary draft")
        composer.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.35)).tap()
        app.typeText(" continued")
        let restore = app.buttons["恢复待发编辑"]
        XCTAssertTrue(restore.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(composer.value as? String, "Ordinary draft continued")
        XCTAssertTrue(queued.exists)
        capture("A rejected edit can be resumed beside the ordinary draft", app: app)

        app.terminate()
        app.launch()
        XCTAssertTrue(session.waitForExistence(timeout: 12), app.debugDescription)
        session.tap()
        XCTAssertTrue(restore.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(composer.value as? String, "Ordinary draft continued")
        restore.tap()
        XCTAssertTrue(save.waitForExistence(timeout: 5))
        XCTAssertEqual(composer.value as? String, "Queued draft revised")
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 2))
        if !tablet {
            let back = app.navigationBars[title].buttons["BackButton"].firstMatch
            XCTAssertTrue(back.waitForExistence(timeout: 4), app.debugDescription)
            back.tap()
        }
        let other = app.buttons["另一个队列会话"].firstMatch
        XCTAssertTrue(other.waitForExistence(timeout: 6), app.debugDescription)
        other.tap()
        let otherHistory = app.staticTexts["Unrelated conversation prompt"].firstMatch
        XCTAssertTrue(otherHistory.waitForExistence(timeout: 8), app.debugDescription)
        if !tablet {
            app.navigationBars["另一个队列会话"].buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(session.waitForExistence(timeout: 6), app.debugDescription)
        session.tap()
        let revised = app.staticTexts["Queued draft revised"].firstMatch
        XCTAssertTrue(revised.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(composer.value as? String, "Ordinary draft continued")
        XCTAssertFalse(restore.exists)
        capture("The Worker saved the queued text without disturbing ordinary input", app: app)
    }

    func testWorkerHistoryReplacementKeepsAnAccessibleLocalEdit() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_HISTORY_EDIT_SESSION"] else {
            throw XCTSkip("Requires the isolated Worker that replaces the edited message")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 16), app.debugDescription)
        session.tap()
        let edit = app.buttons["编辑消息"].firstMatch
        let ordinary = app.textFields["消息"].firstMatch
        let history = app.textFields["编辑已发送消息"].firstMatch
        XCTAssertTrue(edit.waitForExistence(timeout: 10) && edit.isEnabled, app.debugDescription)
        XCTAssertEqual(ordinary.value as? String, "Ordinary draft")
        edit.tap()
        XCTAssertTrue(history.waitForExistence(timeout: 6))
        history.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.35)).tap()
        app.typeText(" local revision")
        XCTAssertTrue(history.waitForNonExistence(timeout: 15), "The owning Worker should replace the source message")
        XCTAssertTrue(app.staticTexts["Another client revised this prompt."].firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(ordinary.value as? String, "Ordinary draft")
        capture("History replaced by a second client while a revision was open", app: app)

        let copy = app.buttons["复制修改稿"]
        XCTAssertTrue(copy.exists, "The saved edit needs a recovery action after its original message disappears")
        copy.tap()
        let discard = app.buttons["放弃历史编辑"]
        XCTAssertTrue(discard.exists)
        discard.tap()
        XCTAssertTrue(copy.waitForNonExistence(timeout: 5))
        XCTAssertEqual(ordinary.value as? String, "Ordinary draft")
        XCTAssertTrue(app.staticTexts["Another client revised this prompt."].exists)

        ordinary.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.35)).tap()
        let paste = app.descendants(matching: .any)["composerClipboardPaste"].firstMatch
        app.buttons["添加附件"].firstMatch.tap()
        XCTAssertTrue(paste.waitForExistence(timeout: 5) && paste.isEnabled, app.debugDescription)
        paste.tap()
        let pasted = NSPredicate(format: "value == %@", "Ordinary draftinspect local revision")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: pasted, object: ordinary)], timeout: 5), .completed)
        capture("Recovered revision pasted independently of the Worker history", app: app)
    }

    func testHistoryReferencesUseAnIndependentNativeEditor() {
        let app = launch("--preview-board-draft-references-history")
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, "ordinary draft")
        let edit = app.buttons["编辑消息"].firstMatch
        XCTAssertTrue(edit.exists && edit.isEnabled, app.debugDescription)
        edit.tap()
        let history = app.textFields["编辑已发送消息"].firstMatch
        XCTAssertTrue(history.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertEqual(history.value as? String, "按 \u{fffc} 检查 \u{fffc} 的边界")
        XCTAssertGreaterThanOrEqual(history.frame.width, composer.frame.width * 0.7)
        capture("History edit with inline references", app: app)
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(history.waitForNonExistence(timeout: 4))
        XCTAssertEqual(composer.value as? String, "ordinary draft")
    }

    func testCompletedToolGroupAlignsCommandAndOutputInTheInsetColumn() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-turns")
        let group = app.buttons.matching(identifier: "1 次工具调用").firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 8))
        XCTAssertTrue((group.value as? String ?? "").contains("已完成"))
        capture("Completed tool group folded", app: app)
        group.tap()
        let command = app.buttons["核对重连逻辑"]
        XCTAssertTrue(command.waitForExistence(timeout: 4))
        capture("Completed tool group unfolded", app: app)
        command.tap()
        let output = app.otherElements["命令输出：核对重连逻辑"].firstMatch
        XCTAssertTrue(output.waitForExistence(timeout: 4))
        capture("Completed tool output in the text column", app: app)
        XCTAssertEqual(command.frame.minX - group.frame.minX, 34, accuracy: 1)
        XCTAssertEqual(output.frame.minX, command.frame.minX, accuracy: 1)
        let firstGlyphs = CGRect(x: command.frame.minX, y: command.frame.minY, width: 36, height: command.frame.height)
        XCTAssertTrue(hasVisibleInk(in: firstGlyphs), "The command must paint in the text column")
    }

    func testActiveToolGroupKeepsItsDisclosureAndOneLiveStatus() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-offline")
        XCTAssertTrue(app.buttons["重连"].waitForExistence(timeout: 8))
        app.buttons["重连"].tap()
        let summaries = app.buttons.matching(identifier: "1 次工具调用")
        XCTAssertTrue(summaries.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(summaries.count, 2)
        let summary = summaries.element(boundBy: 1)
        let headingPosition = summary.frame.minY
        let phase = app.staticTexts["正在执行命令"]
        summary.tap()
        let command = app.buttons["运行完整校验"]
        XCTAssertTrue(command.waitForExistence(timeout: 4))
        XCTAssertTrue(summary.exists)
        XCTAssertEqual(summary.frame.minY, headingPosition, accuracy: 1)
        XCTAssertTrue(phase.waitForExistence(timeout: 4))
        let phaseSize = phase.frame.size
        let clock = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "本轮计时，开始于")).firstMatch
        XCTAssertTrue(clock.exists)
        XCTAssertEqual(clock.frame.midY, phase.frame.midY, accuracy: 0.5)
        XCTAssertEqual(clock.frame.minX - phase.frame.maxX, 6, accuracy: 1)
        XCTAssertEqual(command.frame.minX - summary.frame.minX, 34, accuracy: 1)
        XCTAssertTrue(hasVisibleInk(in: phase.frame), "The expanded live caption must be painted")
        capture("Active tool group after disclosure", app: app)
        XCTAssertEqual(app.staticTexts.matching(identifier: "正在执行命令").count, 1)
        XCTAssertEqual(summaries.count, 2)
        XCTAssertTrue(app.staticTexts["本轮完成"].exists)
        let output = app.staticTexts["正在比对重连前后的事件游标"]
        XCTAssertFalse(output.exists)
        command.tap()
        XCTAssertTrue(output.waitForExistence(timeout: 4))
        summary.tap()
        XCTAssertTrue(command.waitForNonExistence(timeout: 4))
        XCTAssertTrue(summary.exists)
        XCTAssertEqual(summary.frame.minY, headingPosition, accuracy: 1)
        XCTAssertTrue((summary.value as? String ?? "").contains("正在执行命令"))
        // The disclosure exposes its phase through the button value when folded.
        let foldedCaption = CGRect(x: summary.frame.minX + 20 + phaseSize.width,
                                   y: summary.frame.midY - phaseSize.height / 2,
                                   width: phaseSize.width * 0.7, height: phaseSize.height)
        XCTAssertTrue(hasVisibleInk(in: foldedCaption), "The folded live caption must be painted")
        capture("One folded tool disclosure with live status", app: app)
        summary.tap()
        XCTAssertTrue(output.waitForExistence(timeout: 4))
    }

    func testActiveToolGroupUsesTheReplyFooterInBothDisclosureStates() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-offline")
        let reconnect = app.buttons["重连"]
        XCTAssertTrue(reconnect.waitForExistence(timeout: 8))
        reconnect.tap()
        let group = app.buttons.matching(identifier: "1 次工具调用").element(boundBy: 1)
        XCTAssertTrue(group.waitForExistence(timeout: 6))
        let command = app.buttons["运行完整校验"]
        if command.exists {
            group.tap()
        }
        let copy = app.buttons.matching(identifier: "复制工具输出").element(boundBy: 1)
        let fork = app.buttons.matching(identifier: "从这里分叉").element(boundBy: 1)
        let info = app.buttons.matching(identifier: "回复详情").element(boundBy: 1)
        let endTime = app.staticTexts["完成时间未知"]
        let duration = app.staticTexts["用时 —"]
        capture("Folded active tool group and footer", app: app)
        guard copy.waitForExistence(timeout: 4) else { return XCTFail("The active group is missing its shared reply footer") }
        XCTAssertTrue(copy.isEnabled)
        XCTAssertTrue(fork.exists)
        XCTAssertFalse(fork.isEnabled)
        XCTAssertTrue(info.isEnabled)
        XCTAssertLessThan(copy.frame.midX, fork.frame.midX)
        XCTAssertLessThan(fork.frame.midX, info.frame.midX)
        XCTAssertLessThan(info.frame.midX, endTime.frame.midX)
        XCTAssertLessThan(endTime.frame.midX, duration.frame.midX)
        XCTAssertFalse(command.exists)
        group.tap()
        XCTAssertTrue(command.waitForExistence(timeout: 4))
        XCTAssertTrue(copy.exists)
        XCTAssertGreaterThan(copy.frame.minY, command.frame.minY)
        XCTAssertEqual(app.staticTexts.matching(identifier: "正在执行命令").count, 1)
        capture("Expanded active tool group and footer", app: app)
    }

    func testLongCommandOutputScrollsIndependentlyAndRevealsSearch() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-command-output")
        let heading = app.buttons["核对归档清理"]
        XCTAssertTrue(heading.waitForExistence(timeout: 8), app.debugDescription)
        heading.tap()
        capture("Command output before measuring its viewport", app: app)
        let output = app.otherElements["命令输出：核对归档清理"].firstMatch
        guard output.waitForExistence(timeout: 5) else { return XCTFail("Command output has no independent viewport") }
        XCTAssertGreaterThan(output.frame.height, 100)
        XCTAssertLessThanOrEqual(output.frame.height, 161)
        capture("Live command output follows its latest line", app: app)
        let final = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "FINAL-TAIL-55")).firstMatch
        XCTAssertTrue(final.waitForExistence(timeout: 10))
        capture("The final command batch remains visible", app: app)
        let headingY = heading.frame.minY
        let from = output.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.25))
        let to = output.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.8))
        from.press(forDuration: 0.04, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.15)
        XCTAssertEqual(heading.frame.minY, headingY, accuracy: 3)
        capture("A swipe moves output within its own viewport", app: app)
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["在会话中查找"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        app.typeText("early-anchor")
        XCTAssertTrue(app.staticTexts["1 / 1"].waitForExistence(timeout: 8), app.debugDescription)
        capture("Find reveals a match inside independently scrolling output", app: app)
        app.buttons["关闭会话查找"].tap()
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "保留这条日志检查草稿")
    }

    func testWorkerFindOpensUnloadedCommandOutput() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_COMMAND_OUTPUT_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated command output worker fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let row = app.buttons[title].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 20))
        row.tap()
        let current = app.links["查看新记录来源"]
        XCTAssertTrue(current.waitForExistence(timeout: 12), app.debugDescription)
        let output = app.otherElements["命令输出：读取远程清理日志"].firstMatch
        XCTAssertFalse(output.exists)
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["在会话中查找"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        app.typeText("archived-early-token")
        XCTAssertTrue(app.staticTexts["1 / 1"].waitForExistence(timeout: 12))
        XCTAssertTrue(output.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertLessThanOrEqual(output.frame.height, 161)
        capture("Worker hydrates and highlights a long archived command", app: app)
        app.buttons["关闭会话查找"].tap()
        let latest = app.buttons["回到最新消息"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 6))
        latest.tap()
        XCTAssertTrue(current.waitForExistence(timeout: 10))
    }

    func testReturnToLatestCarriesToolActivityWhileReadingHistory() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-tool-tail")
        let heading = app.buttons.matching(NSPredicate(format: "label ENDSWITH %@", "次工具调用")).firstMatch
        XCTAssertTrue(heading.waitForExistence(timeout: 8))
        let latest = app.buttons["回到最新消息"]
        XCTAssertFalse(latest.exists)
        capture("Latest live tool group", app: app)
        app.swipeDown()
        XCTAssertTrue(latest.waitForExistence(timeout: 5))
        XCTAssertEqual(latest.value as? String, "正在执行命令")
        capture("Reading earlier messages with live status in return control", app: app)
        latest.tap()
        XCTAssertTrue(latest.waitForNonExistence(timeout: 5))
        XCTAssertTrue(heading.waitForExistence(timeout: 5))
    }

    func testLiveTurnClockRetainsComposerAndEarlierReceiptOnReconnect() {
        let app = launch("--preview-board-transcript-offline")
        XCTAssertTrue(app.staticTexts["执行状态待核对"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["本轮完成"].exists)
        app.buttons["重连"].tap()
        let summaries = app.buttons.matching(identifier: "1 次工具调用")
        let active = summaries.element(boundBy: 1)
        XCTAssertTrue(active.waitForExistence(timeout: 5))
        active.tap()
        XCTAssertTrue(app.staticTexts["正在执行命令"].waitForExistence(timeout: 5))
        let clock = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "本轮计时，开始于")).firstMatch
        XCTAssertTrue(clock.exists)
        let clockFrame = clock.frame
        func clockPixels() -> Data? {
            let screen = XCUIScreen.main.screenshot().image
            let renderer = UIGraphicsImageRenderer(size: clockFrame.size)
            return renderer.image { _ in
                screen.draw(at: CGPoint(x: -clockFrame.minX, y: -clockFrame.minY))
            }.pngData()
        }
        let initial = clockPixels()
        XCTAssertNotNil(initial)
        expectation(for: NSPredicate { _, _ in clockPixels() != initial }, evaluatedWith: nil)
        waitForExpectations(timeout: 4)
        let editor = app.descendants(matching: .any).matching(identifier: "消息").firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 5))
        editor.tap()
        app.typeText("keep draft")
        XCTAssertTrue((editor.value as? String ?? "").contains("keep draft"))
    }

    func testConversationRendersStructuredResults() {
        let app = launch("--preview-board-transcript-content")
        XCTAssertTrue(app.staticTexts["保留现有缓存"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["采用哪种缓存策略？"].exists)
        app.buttons["思考摘要"].tap()
        XCTAssertTrue(app.staticTexts["对照重连前后的历史事件。"].exists)
        app.buttons["1 次工具调用"].tap()
        let tool = app.buttons["检查工作区"]
        XCTAssertTrue((tool.value as? String ?? "").contains("退出码 3"))
        tool.tap()
        for label in ["reconnect.go", "已核对事件顺序", "worker ping: ok", "内容过长，已截断显示"] {
            let result = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", label)).firstMatch
            for _ in 0..<3 where !result.exists { app.swipeUp() }
            XCTAssertTrue(result.exists, label)
        }
        capture("Structured results", app: app)
        let image = app.buttons["预览 connection.png"]
        let file = app.buttons["预览 diagnostics.txt"]
        XCTAssertTrue(image.exists)
        XCTAssertTrue(file.exists)
        file.tap()
        let reader = app.descendants(matching: .any).matching(identifier: "文件内容：diagnostics.txt").firstMatch
        XCTAssertTrue(reader.waitForExistence(timeout: 5))
        XCTAssertEqual(reader.value as? String, "worker ping: ok\n")
        XCTAssertFalse(app.buttons["选择本机文件"].exists)
    }

    func testConversationDisplaysSentAndToolImagesInline() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-images")
        let group = app.buttons["1 次工具调用"]
        XCTAssertTrue(group.waitForExistence(timeout: 8))
        let sent = app.buttons["预览 reconnect-before.png"]
        let result = app.buttons["预览 reconnect-after.png"]
        XCTAssertFalse(result.exists)
        capture("Sent image in conversation", app: app)
        group.tap()
        app.buttons["生成重连延迟图"].tap()
        XCTAssertTrue(result.waitForExistence(timeout: 5))
        let loaded = NSPredicate(format: "value == %@", "图片已加载")
        capture("Expanded image result in conversation", app: app)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: sent)], timeout: 5), .completed)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: result)], timeout: 5), .completed)
        XCTAssertGreaterThanOrEqual(result.frame.width, 200)
        XCTAssertGreaterThan(result.frame.height, 140)
        capture("Native sent and tool images", app: app)
        result.tap()
        XCTAssertTrue(app.navigationBars["附件预览"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["reconnect-after.png"].exists)
    }

    func testAttachmentImagePreviewRotatesAndZoomsLocally() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        continueAfterFailure = false
        let app = launch("--preview-board-transcript-images")
        let thumbnail = app.buttons["预览 reconnect-before.png"]
        XCTAssertTrue(thumbnail.waitForExistence(timeout: 8))
        thumbnail.tap()
        let bar = app.navigationBars["附件预览"]
        XCTAssertTrue(bar.waitForExistence(timeout: 5))
        capture("Opened the image attachment", app: app)
        let image = app.images["reconnect-before.png"].firstMatch
        let rotate = app.buttons["逆时针旋转 90°"].firstMatch
        let viewport = app.scrollViews["imagePreviewScroll"].firstMatch
        XCTAssertTrue(rotate.waitForExistence(timeout: 5), "The image viewer must expose the same rotation action as Mac")
        func waitForImage(_ predicate: @escaping (XCUIElement) -> Bool) {
            let observed = NSPredicate { _, _ in image.exists && predicate(image) }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: observed, object: image)], timeout: 5), .completed)
        }
        waitForImage { $0.frame.width > $0.frame.height }
        let original = image.screenshot().pngRepresentation
        rotate.tap()
        waitForImage { $0.frame.height > $0.frame.width }
        capture("Rotated 90 degrees counterclockwise", app: app)

        XCTAssertTrue(viewport.isHittable)
        viewport.pinch(withScale: 2, velocity: 1)
        waitForImage { image in
            guard let value = image.value as? String else { return false }
            return !value.hasPrefix("100%")
        }
        capture("Zoomed the image only", app: app)
        let zoomed = viewport.screenshot().pngRepresentation
        viewport.swipeUp()
        XCTAssertNotEqual(viewport.screenshot().pngRepresentation, zoomed, "A zoomed image can be panned to inspect the crop")
        XCTAssertTrue(rotate.isHittable)
        XCTAssertTrue(bar.exists)
        viewport.doubleTap()
        waitForImage { ($0.value as? String)?.hasPrefix("100%") == true }

        bar.buttons.firstMatch.tap()
        XCTAssertTrue(thumbnail.waitForExistence(timeout: 5))
        thumbnail.tap()
        waitForImage { $0.frame.width > $0.frame.height && ($0.value as? String)?.hasPrefix("100%") == true }
        XCTAssertEqual(image.screenshot().pngRepresentation, original, "Each opening starts with the original orientation and fit")
    }

    func testMarkdownImagesReadOwningWorkerAndDocumentPaths() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_MARKDOWN_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated Markdown image worker fixture")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 20), app.debugDescription)
        session.tap()
        let local = app.buttons["预览 工作区延迟图"]
        let loaded = NSPredicate(format: "value == %@", "图片已加载")
        XCTAssertTrue(app.links["查看恢复报告"].waitForExistence(timeout: 15), app.debugDescription)
        capture("Markdown conversation image baseline", app: app)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: local)], timeout: 7), .completed)
        XCTAssertGreaterThan(local.frame.height, 120)
        let mixed = app.buttons["预览 同一份图"]
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: mixed)], timeout: 7), .completed)
        capture("Markdown images fetched from the owning worker", app: app)
        let report = app.links["查看恢复报告"]
        let reportLeadingEdge = report.frame.minX
        local.tap()
        let original = app.buttons["原生预览"]
        XCTAssertTrue(original.waitForExistence(timeout: 6), app.debugDescription)
        let linkedImage = app.images["latency chart.png"].firstMatch
        XCTAssertTrue(linkedImage.waitForExistence(timeout: 6), app.debugDescription)
        let rotateImage = app.buttons["逆时针旋转 90°"].firstMatch
        XCTAssertTrue(rotateImage.isHittable)
        rotateImage.tap()
        let rotated = NSPredicate { _, _ in (linkedImage.value as? String)?.contains("90°") == true }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: rotated, object: linkedImage)], timeout: 5), .completed)
        capture("Linked image preview keeps the loaded file", app: app)
        if tablet {
            app.buttons["关闭文件预览"].tap()
            XCTAssertTrue(original.waitForNonExistence(timeout: 6))
            let conversationRestored = NSPredicate { _, _ in
                report.isHittable && abs(report.frame.minX - reportLeadingEdge) < 2
            }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: conversationRestored, object: report)], timeout: 6), .completed)
        } else {
            app.navigationBars.buttons.firstMatch.tap()
        }
        report.tap()
        let nested = app.buttons["预览 报告目录的图"]
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: nested)], timeout: 7), .completed)
        capture("Markdown preview resolves images from its document directory", app: app)
    }

    private func chooseOutlinePrompt(_ prompt: XCUIElement, in app: XCUIApplication, photo: String) {
        let bar = app.navigationBars["对话导航"]
        func visible() -> Bool {
            prompt.exists && prompt.frame.midY > bar.frame.maxY + 8 && prompt.frame.midY < app.frame.maxY - 20
        }
        for _ in 0..<10 where !visible() {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.60, dy: 0.24))
                .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.60, dy: 0.83)),
                       withVelocity: .slow, thenHoldForDuration: 0.20)
        }
        XCTAssertTrue(visible(), app.debugDescription)
        capture(photo, app: app)
        let row = prompt.frame
        app.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: row.midX - app.frame.minX, dy: row.midY - app.frame.minY)).tap()
    }

    func testConversationOutlineJumpsToOlderPromptAndKeepsDraft() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-outline")
        let activity = app.buttons["会话活动"].firstMatch
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 12))
        XCTAssertEqual(draft.value as? String, "保留这条提问草稿")
        activity.tap()
        XCTAssertTrue(app.navigationBars["会话活动"].waitForExistence(timeout: 6))
        capture("Missing prompt navigation in session activity", app: app)
        let outline = app.buttons["对话导航"]
        guard outline.waitForExistence(timeout: 4) else { return XCTFail("No conversation outline entry") }
        outline.tap()
        XCTAssertTrue(app.navigationBars["对话导航"].waitForExistence(timeout: 8))
        let prompt = app.buttons["排查断线前的记录。"]
        chooseOutlinePrompt(prompt, in: app, photo: "Scrollable conversation prompt outline")
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        XCTAssertFalse(app.navigationBars["对话导航"].exists)
        let older = app.staticTexts["排查断线前的记录。"]
        XCTAssertTrue(older.exists && older.frame.midY < draft.frame.minY)
        XCTAssertEqual(draft.value as? String, "保留这条提问草稿")
        capture("Older prompt located in the active transcript", app: app)
        let latest = app.buttons["回到最新消息"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 5))
        latest.tap()
        XCTAssertTrue(app.staticTexts["稍后的消息 25"].waitForExistence(timeout: 6))
        XCTAssertEqual(draft.value as? String, "保留这条提问草稿")
    }

    func testFindInConversationMovesBetweenRepeatedAndUnloadedMatches() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-find")
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 12))
        activity.tap()
        XCTAssertTrue(app.navigationBars["会话活动"].waitForExistence(timeout: 6))
        capture("Session activity before opening find", app: app)
        let entry = app.buttons["在会话中查找"]
        guard entry.waitForExistence(timeout: 4) else { return XCTFail("No find in conversation entry") }
        entry.tap()
        let field = app.textFields["在会话中查找"]
        XCTAssertTrue(field.waitForExistence(timeout: 6))
        XCTAssertFalse(app.navigationBars["会话活动"].exists)
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        XCTAssertFalse(app.textFields["消息"].firstMatch.exists)
        app.typeText("reconnect")
        XCTAssertTrue(app.staticTexts["1 / 4"].waitForExistence(timeout: 8), app.debugDescription)
        let old = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "断线日志")).firstMatch
        XCTAssertTrue(old.waitForExistence(timeout: 5))
        capture("First highlighted occurrence in unloaded history", app: app)
        app.buttons["下一处匹配"].tap()
        XCTAssertTrue(app.staticTexts["2 / 4"].waitForExistence(timeout: 5))
        capture("Second occurrence in the same reply", app: app)
        app.buttons["下一处匹配"].tap()
        XCTAssertTrue(app.staticTexts["3 / 4"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["工具输出 reconnect"].waitForExistence(timeout: 5), app.debugDescription)
        capture("Find reveals a folded tool output", app: app)
        app.buttons["下一处匹配"].tap()
        XCTAssertTrue(app.staticTexts["4 / 4"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "恢复结果")).firstMatch.waitForExistence(timeout: 5))
        capture("Find returns to the recent history window", app: app)
        app.buttons["关闭会话查找"].tap()
        XCTAssertTrue(field.waitForNonExistence(timeout: 6))
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "保留这条提问草稿")
    }

    func testSessionActionsFromListOpensTheSelectedConversationFinder() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview-board-session-actions")
        let actions = app.navigationBars["会话操作"].firstMatch
        XCTAssertTrue(actions.waitForExistence(timeout: 8), app.debugDescription)
        app.buttons["搜索对话内容"].firstMatch.tap()
        capture("Opened history search from the session's actions", app: app)
        let finder = app.textFields["在会话中查找"].firstMatch
        XCTAssertTrue(finder.waitForExistence(timeout: 6), "Session actions should open the find field in the original conversation")
        XCTAssertFalse(app.navigationBars["全文搜索"].exists)
        XCTAssertTrue(app.buttons["会话活动"].firstMatch.isHittable)
        if tablet {
            let sidebar = app.buttons["会话视图"].firstMatch
            XCTAssertTrue(sidebar.isHittable)
            XCTAssertLessThan(sidebar.frame.maxX, finder.frame.minX)
        }
        app.buttons["关闭会话查找"].firstMatch.tap()
        XCTAssertTrue(finder.waitForNonExistence(timeout: 6))
        XCTAssertTrue(app.textFields["消息"].firstMatch.exists)
    }

    func testImportPreviewShowsMultipleItemsAndRetriesOnlyFailedSelections() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_IMPORT_PREVIEW_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the authenticated import catalog fixture")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        app.buttons["设备"].firstMatch.tap()
        let device = app.buttons[environment["SHIOJI_IMPORT_PREVIEW_DEVICE"] ?? "127.0.0.1"].firstMatch
        XCTAssertTrue(device.waitForExistence(timeout: 10))
        device.tap()
        let workspace = app.buttons["test"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 10))
        workspace.tap()
        let entry = app.buttons["导入会话"]
        XCTAssertTrue(entry.waitForExistence(timeout: 8))
        entry.tap()
        let first = app.buttons["检查导入消息"].firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        first.tap()
        XCTAssertTrue(app.staticTexts["导入前先核对这条提问。"].waitForExistence(timeout: 8))
        let reply = app.staticTexts["这是后面的完整回复。"]
        let visible = reply.waitForExistence(timeout: 5)
        capture(visible ? "Bounded import preview shows several message types" : "Old import preview shows only the first message", app: app)
        XCTAssertTrue(visible, "The preview must render the later reply, not just the first item")
        XCTAssertTrue(app.staticTexts["历史命令"].exists)
        XCTAssertTrue(app.staticTexts["结构化结果可阅读。"].exists)
        XCTAssertTrue(app.staticTexts["导入时完整读取"].exists)
        app.buttons["选择此会话"].tap()
        XCTAssertTrue(app.buttons["导入 1 个"].waitForExistence(timeout: 6))
        app.buttons["更多会话"].tap()
        XCTAssertTrue(app.buttons["备份测试历史"].waitForExistence(timeout: 8))
        let search = app.textFields["搜索导入会话"]
        search.tap()
        app.typeText("备份")
        XCTAssertFalse(first.exists)
        app.buttons["选择会话：备份测试历史"].tap()
        let batch = app.buttons["导入 2 个"]
        XCTAssertTrue(batch.waitForExistence(timeout: 6))
        batch.tap()
        let partial = app.staticTexts["已导入 1 个，1 个未能完成"]
        XCTAssertTrue(partial.waitForExistence(timeout: 12))
        capture("Batch retains only the source that still needs retry", app: app)
        app.buttons["重试 1 个"].tap()
        XCTAssertTrue(app.staticTexts["已导入 2 个会话"].waitForExistence(timeout: 12))
        app.buttons["已导入"].tap()
        let second = app.buttons["备份测试历史"].firstMatch
        XCTAssertTrue(second.waitForExistence(timeout: 6))
        second.tap()
        let open = app.buttons["打开已导入会话"]
        XCTAssertTrue(open.waitForExistence(timeout: 8))
        open.tap()
        XCTAssertTrue(reply.waitForExistence(timeout: 12))
        capture("Previously imported history opens as a normal conversation", app: app)
    }

    func testImportedHistoryCompletionKeepsTheOriginalSessionAndDraft() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_IMPORT_COMPLETION_FIXTURE"] == "1" else {
            throw XCTSkip("Requires an isolated Worker with an incomplete imported session")
        }
        continueAfterFailure = false
        let rightPane = environment["SHIOJI_IMPORT_COMPLETION_RIGHT_PANE"] == "1"
        XCUIDevice.shared.orientation = rightPane ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let locked = app.buttons["仅可查看的远程历史"].firstMatch
        let originalTitle = "需要补全的远程历史"
        let partial = app.buttons[originalTitle].firstMatch
        XCTAssertTrue(locked.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        XCTAssertTrue(partial.waitForExistence(timeout: 12))
        let updateHistory = app.buttons["补全历史"]
        let composer = app.textFields[rightPane ? "右侧会话草稿" : "消息"].firstMatch

        if rightPane {
            locked.tap()
            XCTAssertTrue(app.staticTexts["这段历史可以阅读。"].waitForExistence(timeout: 8))
            XCTAssertTrue(app.staticTexts["来源会话仍在运行，暂时只能查看。"].waitForExistence(timeout: 8))
            XCTAssertFalse(updateHistory.exists)
            app.buttons["会话活动"].firstMatch.tap()
            app.buttons["并排查看会话"].tap()
            app.buttons.matching(identifier: originalTitle).element(boundBy: 1).tap()
        } else {
            partial.tap()
        }
        let excerpt = app.staticTexts["这是一段旧版节选。"]
        XCTAssertTrue(excerpt.waitForExistence(timeout: 10))
        if environment["SHIOJI_IMPORT_COMPLETION_LEGACY_WORKER"] == "1" {
            XCTAssertTrue(app.staticTexts["此会话保留了部分历史。更新所属设备的执行服务后即可补全。"].waitForExistence(timeout: 6))
            XCTAssertFalse(updateHistory.exists)
            XCTAssertFalse(composer.exists)
            capture("Legacy Worker shows its device update entry", app: app)
            app.buttons["设备详情"].tap()
            XCTAssertTrue(app.buttons["Shioji Worker"].waitForExistence(timeout: 8))
            return
        }
        let offered = updateHistory.waitForExistence(timeout: 6)
        capture(offered ? "Imported excerpt with recovery action" : "Imported excerpt with no recovery action", app: app)
        XCTAssertTrue(offered, "The incomplete import needs a completion action")
        XCTAssertFalse(composer.exists)

        updateHistory.tap()
        let error = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "来源暂未准备好，请稍后重试")).firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 10))
        XCTAssertTrue(excerpt.exists)
        XCTAssertFalse(composer.exists)
        capture("Failed completion leaves the excerpt readable", app: app)

        updateHistory.tap()
        XCTAssertTrue(app.staticTexts["设备已读到完整历史。"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(excerpt.exists)
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, "留在这个会话的草稿")
        XCTAssertFalse(updateHistory.exists)
        XCTAssertTrue(partial.exists || app.navigationBars[originalTitle].exists)
        if rightPane {
            XCTAssertTrue(app.staticTexts["这段历史可以阅读。"].exists)
            XCTAssertTrue(app.staticTexts["来源会话仍在运行，暂时只能查看。"].exists)
        }
        capture("Full imported history and the original unsent draft", app: app)

        if !rightPane {
            app.navigationBars[originalTitle].buttons.firstMatch.tap()
            XCTAssertTrue(locked.waitForExistence(timeout: 8))
            locked.tap()
            XCTAssertTrue(app.staticTexts["来源会话仍在运行，暂时只能查看。"].waitForExistence(timeout: 8))
            XCTAssertFalse(updateHistory.exists)
            XCTAssertFalse(composer.exists)
        }
    }

    func testWorkerFindInConversationUsesUnloadedHistory() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_OUTLINE_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated conversation worker fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 20), app.debugDescription)
        session.tap()
        let latest = app.links["查看新记录来源"]
        let oldest = app.links["查看旧记录来源"]
        XCTAssertTrue(latest.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(oldest.exists)
        app.buttons["会话活动"].firstMatch.tap()
        let actions = app.buttons["会话操作"].firstMatch
        XCTAssertTrue(actions.waitForExistence(timeout: 6))
        actions.tap()
        app.buttons["搜索对话内容"].firstMatch.tap()
        capture("Search entered through session actions", app: app)
        let field = app.textFields["在会话中查找"]
        XCTAssertTrue(field.waitForExistence(timeout: 8))
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        app.typeText("cursor-index")
        XCTAssertTrue(app.staticTexts["1 / 2"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(oldest.waitForExistence(timeout: 10), app.debugDescription)
        capture("Worker find loads the older matching reply", app: app)
        app.buttons["下一处匹配"].tap()
        XCTAssertTrue(app.staticTexts["2 / 2"].waitForExistence(timeout: 5))
        XCTAssertTrue(latest.waitForExistence(timeout: 10))
        app.buttons["上一处匹配"].tap()
        XCTAssertTrue(oldest.waitForExistence(timeout: 10))
        app.typeText("-absent")
        XCTAssertTrue(app.staticTexts["没有匹配"].waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["下一处匹配"].isEnabled)
        app.buttons["关闭会话查找"].tap()
        XCTAssertTrue(field.waitForNonExistence(timeout: 6))
        XCTAssertTrue(oldest.exists)
    }

    func testWorkerOutlineJumpsToAnUnloadedPrompt() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_OUTLINE_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated outline worker fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 20), app.debugDescription)
        session.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        let newestLink = app.links["查看新记录来源"]
        XCTAssertTrue(newestLink.waitForExistence(timeout: 12), app.debugDescription)
        let older = app.staticTexts["核对第 1 项。"]
        XCTAssertFalse(older.exists)
        activity.tap()
        let outline = app.buttons["对话导航"]
        XCTAssertTrue(outline.waitForExistence(timeout: 5))
        outline.tap()
        XCTAssertTrue(app.navigationBars["对话导航"].waitForExistence(timeout: 8))
        let prompt = app.buttons["核对第 1 项。"]
        chooseOutlinePrompt(prompt, in: app, photo: "Outline includes an unloaded Worker prompt")
        XCTAssertTrue(older.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertGreaterThan(older.frame.midY, 110)
        XCTAssertFalse(app.navigationBars["对话导航"].exists)
        capture("Jump loaded the original Worker conversation around the prompt", app: app)
        let nextWindow = app.staticTexts["核对第 21 项。"].firstMatch
        XCTAssertFalse(nextWindow.exists)
        let top = app.coordinate(withNormalizedOffset: CGVector(dx: 0.62, dy: 0.24))
        let bottom = app.coordinate(withNormalizedOffset: CGVector(dx: 0.62, dy: 0.74))
        for _ in 0..<12 where !nextWindow.isHittable {
            bottom.press(forDuration: 0.05, thenDragTo: top, withVelocity: .default, thenHoldForDuration: 0.18)
        }
        XCTAssertTrue(nextWindow.waitForExistence(timeout: 8), "Scrolling must load a newer round outside the targeted window")
        XCTAssertFalse(app.buttons["加载较新的消息"].exists)
        capture("Newer rounds fetched while reading from the targeted window", app: app)
        app.buttons["回到最新消息"].firstMatch.tap()
        XCTAssertTrue(newestLink.waitForExistence(timeout: 10), app.debugDescription)
    }

    func testSearchHistoryUsesTheConversationPresentation() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-history-match-content")
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 12), app.debugDescription)
        let attachment = app.buttons["预览 reconnect-before.png"]
        guard attachment.waitForExistence(timeout: 5) else { return XCTFail("The matched prompt lost its image attachment") }
        if tablet {
            let sidebar = app.buttons["会话视图"]
            XCTAssertTrue(sidebar.exists)
            XCTAssertLessThan(sidebar.frame.maxX, attachment.frame.minX)
        }
        XCTAssertTrue(app.buttons["预览引用：index.md"].exists)
        XCTAssertTrue(app.buttons["复制消息"].exists)
        XCTAssertTrue(app.buttons["编辑消息"].exists)
        XCTAssertTrue(app.textFields["消息"].exists)
        XCTAssertFalse(app.buttons["下一条"].exists)
        let loaded = NSPredicate(format: "value == %@", "图片已加载")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: attachment)], timeout: 6), .completed)
        XCTAssertTrue(attachment.isHittable)
        capture("Search opens the original prompt with its image and reference", app: app)
    }

    func testFullSearchKeyboardSelectsAndOpensTheResult() {
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-inbox")
        let filter = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(filter.waitForExistence(timeout: 8), app.debugDescription)
        filter.tap()
        let fullSearch = app.descendants(matching: .any)["全文搜索"].firstMatch
        XCTAssertTrue(fullSearch.waitForExistence(timeout: 6))
        fullSearch.tap()
        let field = app.textFields["搜索会话与对话内容"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 6))
        field.tap()
        app.typeText("Worker")
        let result = app.buttons.matching(NSPredicate(format: "label == %@ AND value == %@",
                                                      "测试断线重连", "Shioji · Mac mini")).firstMatch
        capture("Full search after typing without submitting", app: app)
        XCTAssertTrue(result.waitForExistence(timeout: 6), "Typing must search without a separate submit")

        app.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: "Worker".count) + "iPad")
        let titleResult = app.buttons.matching(NSPredicate(format: "label == %@ AND value == %@",
                                                           "修正 iPad 文件检查器", "Shioji · Mac mini")).firstMatch
        XCTAssertTrue(result.waitForNonExistence(timeout: 5))
        XCTAssertTrue(titleResult.waitForExistence(timeout: 6), app.debugDescription)
        app.typeKey(XCUIKeyboardKey.downArrow.rawValue, modifierFlags: [])
        let selected = NSPredicate(format: "selected == true")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: selected, object: titleResult)], timeout: 4), .completed)
        capture("Keyboard highlights the full-search result", app: app)
        app.typeText("\n")
        XCTAssertTrue(app.buttons["会话活动"].firstMatch.waitForExistence(timeout: 6), app.debugDescription)
    }

    func testFullSearchOpensAHistoryResultOnAnotherDevice() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_MULTI_DEVICE_SEARCH"] == "1" else {
            throw XCTSkip("Requires two enrolled workers and one unavailable device")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let currentTitle = "当前设备的归档校验"
        let remoteTitle = "另一设备的归档校验"
        XCTAssertTrue(app.buttons[currentTitle].waitForExistence(timeout: 20), app.debugDescription)
        if let peerName = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            let devices = app.buttons["设备"].firstMatch
            XCTAssertTrue(devices.waitForExistence(timeout: 8))
            devices.tap()
            let tailnet = app.buttons["Tailnet"].firstMatch
            XCTAssertTrue(tailnet.waitForExistence(timeout: 8))
            tailnet.tap()
            let join = app.buttons["在浏览器授权"]
            XCTAssertTrue(join.waitForExistence(timeout: 8), app.debugDescription)
            join.tap()
            XCTAssertTrue(app.buttons[peerName].waitForExistence(timeout: 30), app.debugDescription)
            capture("Embedded node discovered the search-only Tailnet peer", app: app)
            app.navigationBars["Tailnet"].buttons["BackButton"].firstMatch.tap()
            let sessions = app.buttons["会话"].firstMatch
            XCTAssertTrue(sessions.waitForExistence(timeout: 8))
            sessions.tap()
            XCTAssertTrue(app.buttons[currentTitle].waitForExistence(timeout: 8), app.debugDescription)
        }
        app.buttons["会话视图"].tap()
        let menu = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "全文搜索")).firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 6))
        menu.tap()
        let field = app.textFields["搜索会话与对话内容"]
        XCTAssertTrue(field.waitForExistence(timeout: 8))
        field.tap()
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
        for letter in "cursor" {
            let lower = keyboard.keys[String(letter)]
            (lower.exists ? lower : keyboard.keys[String(letter).uppercased()]).tap()
        }
        if (field.value as? String)?.isEmpty == true {
            let candidate = app.cells["cursor"].firstMatch
            XCTAssertTrue(candidate.waitForExistence(timeout: 5))
            candidate.tap()
        }
        XCTAssertEqual((field.value as? String)?.trimmingCharacters(in: .whitespaces).lowercased(), "cursor")
        func result(_ title: String, timeout: TimeInterval) throws -> XCUIElement {
            let candidates = app.buttons.matching(NSPredicate(format: "label == %@ AND value BEGINSWITH %@", title, "test · "))
            var match: XCUIElement?
            let found = NSPredicate { _, _ in
                if tablet {
                    // The sidebar has independently tappable rows for the same sessions.
                    let sidebarEdge = app.buttons["会话视图"].firstMatch.frame.maxX
                    match = candidates.allElementsBoundByIndex.first { $0.frame.minX > sidebarEdge }
                } else if candidates.firstMatch.exists {
                    match = candidates.firstMatch
                }
                return match != nil
            }
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: found, object: app)], timeout: timeout), .completed)
            return try XCTUnwrap(match, "Missing full-search result for \(title)")
        }
        _ = try result(currentTitle, timeout: 10)
        capture("Full search results from the available workers", app: app)
        let other = try result(remoteTitle, timeout: 15)
        if let remoteAddress = environment["SHIOJI_TAILNET_SEARCH_ADDRESS"] {
            XCTAssertTrue((other.value as? String)?.contains(remoteAddress) == true, app.debugDescription)
        }
        let partial = app.descendants(matching: .any).matching(
            NSPredicate(format: "label == %@", "部分设备搜索失败，结果可能不完整")
        ).firstMatch
        XCTAssertTrue(partial.waitForExistence(timeout: 15))
        capture("Both devices remain listed after a third is unavailable", app: app)
        other.tap()
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 12), app.debugDescription)
        let evidence = app.links["另一设备的完整记录"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(app.links["当前设备的完整记录"].exists)
        capture("Selected response on the matching worker", app: app)
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        _ = try result(currentTitle, timeout: 8)
        try result(remoteTitle, timeout: 8).tap()
        XCTAssertTrue(evidence.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(app.buttons["会话活动"].exists)
    }

    func testComposerShortcutSettingsRejectConflictsAndRestoreDefaults() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview")
        let settings = app.buttons["设置"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        settings.tap()
        let input = app.buttons["输入与外观"].firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 8))
        input.tap()
        XCTAssertTrue(app.navigationBars["输入与外观"].waitForExistence(timeout: 6))
        let send = app.buttons["发送或加入队列"].firstMatch
        let newline = app.buttons["换行"].firstMatch
        let steer = app.buttons["立即介入当前轮"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 6), "All three composer shortcuts must be configurable")
        XCTAssertEqual(send.value as? String, "⌘ Return")
        XCTAssertEqual(newline.value as? String, "Return")
        XCTAssertEqual(steer.value as? String, "⌘ ⇧ Return")
        capture("Three independent mobile composer shortcuts", app: app)
        send.tap()
        app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Return")).firstMatch.tap()
        let conflict = app.descendants(matching: .any).matching(
            NSPredicate(format: "label == %@", "此按键已用于“换行”")
        ).firstMatch
        XCTAssertTrue(conflict.waitForExistence(timeout: 5))
        XCTAssertEqual(send.value as? String, "⌘ Return")
        capture("An assigned combination reports its current action", app: app)

        newline.tap()
        app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "⌥ Return")).firstMatch.tap()
        XCTAssertEqual(newline.value as? String, "⌥ Return")
        send.tap()
        app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Return")).firstMatch.tap()
        XCTAssertEqual(send.value as? String, "Return")
        app.buttons["恢复默认按键"].firstMatch.tap()
        XCTAssertEqual(send.value as? String, "⌘ Return")
        XCTAssertEqual(newline.value as? String, "Return")
        XCTAssertEqual(steer.value as? String, "⌘ ⇧ Return")
    }

    private func pressSimulatorHardwareKey(_ key: String, endpoint: URL) {
        let event = expectation(description: "Simulator keyboard: \(key)")
        URLSession.shared.dataTask(with: endpoint.appendingPathComponent(key)) { _, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
            event.fulfill()
        }.resume()
        wait(for: [event], timeout: 15)
    }

    func testConfiguredNewlinePreservesChildDraftSelection() throws {
        guard let address = ProcessInfo.processInfo.environment["SHIOJI_HARDWARE_RETURN_HID_URL"],
              let keyboard = URL(string: address) else {
            throw XCTSkip("Requires the simulator hardware keyboard fixture")
        }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-child-agent")
        let child = app.navigationBars["连接验证"]
        XCTAssertTrue(child.waitForExistence(timeout: 8))
        child.buttons["BackButton"].firstMatch.tap()
        let settings = app.buttons["设置"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        settings.tap()
        app.buttons["输入与外观"].firstMatch.tap()
        let newline = app.buttons["换行"].firstMatch
        XCTAssertTrue(newline.waitForExistence(timeout: 6))
        newline.tap()
        let option = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "⌥ Return")).firstMatch
        XCTAssertTrue(option.waitForExistence(timeout: 6))
        option.tap()
        XCTAssertEqual(newline.value as? String, "⌥ Return")
        app.navigationBars["输入与外观"].buttons["BackButton"].firstMatch.tap()
        app.buttons["会话"].firstMatch.tap()
        let session = app.buttons["测试断线重连"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 8))
        session.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        activity.tap()
        let childRow = app.buttons["连接验证"].firstMatch
        XCTAssertTrue(childRow.waitForExistence(timeout: 8))
        childRow.tap()
        let editor = app.textFields["补充给子 Agent"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 8))
        editor.tap()
        app.typeText("ac")
        app.typeKey(XCUIKeyboardKey.leftArrow.rawValue, modifierFlags: [])
        pressSimulatorHardwareKey("alt-return", endpoint: keyboard)
        app.typeText("b")
        XCTAssertEqual(editor.value as? String, "a\nbc")
    }

    func testHardwareReturnPreferenceKeepsSoftwareReturnForNewlines() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let address = environment["SHIOJI_HARDWARE_RETURN_HID_URL"],
              let keyboard = URL(string: address) else {
            throw XCTSkip("Requires an isolated Worker and a simulator hardware keyboard fixture")
        }
        continueAfterFailure = false
        func press(_ key: String) { pressSimulatorHardwareKey(key, endpoint: keyboard) }
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let target = app.buttons["键盘发送回执"]
        XCTAssertTrue(target.waitForExistence(timeout: 12))
        func openReturnPreferences() {
            let settings = app.buttons["设置"].firstMatch
            XCTAssertTrue(settings.waitForExistence(timeout: 8))
            settings.tap()
            let input = app.buttons["输入与外观"].firstMatch
            XCTAssertTrue(input.waitForExistence(timeout: 8))
            input.tap()
            XCTAssertTrue(app.buttons["发送或加入队列"].firstMatch.waitForExistence(timeout: 6))
        }
        func assign(_ action: String, _ chord: String) {
            let row = app.buttons[action].firstMatch
            row.tap()
            let option = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", chord)).firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 6))
            option.tap()
            XCTAssertEqual(row.value as? String, chord)
        }
        openReturnPreferences()
        assign("换行", "⌥ Return")
        assign("发送或加入队列", "Return")
        assign("立即介入当前轮", "⌃ Return")
        capture("Independent shortcuts entered in Settings", app: app)
        app.terminate()
        app.launch()
        XCTAssertTrue(target.waitForExistence(timeout: 12))
        openReturnPreferences()
        XCTAssertEqual(app.buttons["发送或加入队列"].firstMatch.value as? String, "Return")
        XCTAssertEqual(app.buttons["换行"].firstMatch.value as? String, "⌥ Return")
        XCTAssertEqual(app.buttons["立即介入当前轮"].firstMatch.value as? String, "⌃ Return")
        app.navigationBars["输入与外观"].buttons["BackButton"].firstMatch.tap()
        app.buttons["会话"].firstMatch.tap()
        target.tap()

        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        composer.tap()
        app.typeText("Keyboard first")
        let softwareReturn = app.keyboards.descendants(matching: .any)
            .matching(NSPredicate(format: "label IN[c] %@", ["return", "换行", "換行"])).firstMatch
        let keyboardIsVisible = XCTNSPredicateExpectation(predicate: NSPredicate(format: "hittable == true"), object: softwareReturn)
        XCTAssertEqual(XCTWaiter.wait(for: [keyboardIsVisible], timeout: 6), .completed, app.keyboards.debugDescription)
        softwareReturn.tap()
        app.typeText("software newline")
        let softwareText = "Keyboard first\nsoftware newline"
        XCTAssertEqual(composer.value as? String, softwareText)
        capture("Software Return preserved the draft", app: app)

        app.typeText(" @")
        press("return")
        XCTAssertEqual(composer.value as? String, softwareText + " @")
        press("backspace")
        press("backspace")
        press("alt-return")
        app.typeText("option newline")
        let finalText = softwareText + "\noption newline"
        XCTAssertEqual(composer.value as? String, finalText)
        press("command-return")
        XCTAssertEqual(composer.value as? String, finalText, "The unassigned physical Command key must not act as Control")
        if environment["SHIOJI_COMPOSER_SHORTCUT_STEER"] != "1" {
            press("control-return")
            XCTAssertEqual(composer.value as? String, finalText, "Steer requires an active turn")
        }
        press("return")
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "from alpha: Keyboard first")).firstMatch
        if environment["SHIOJI_HARDWARE_RETURN_QUEUE"] == "1" {
            XCTAssertTrue(app.staticTexts["队列已暂停"].exists)
            let queued = app.buttons["介入当前轮，第 2 条待发消息"]
            XCTAssertTrue(queued.waitForExistence(timeout: 12), app.debugDescription)
            XCTAssertFalse(reply.exists)
            capture("Physical Return appended to the paused queue", app: app)
        } else {
            XCTAssertTrue(reply.waitForExistence(timeout: 12), app.debugDescription)
            capture("Physical Return sent the message", app: app)
        }
        XCTAssertNotEqual(composer.value as? String, finalText)
        if environment["SHIOJI_COMPOSER_SHORTCUT_STEER"] == "1" {
            composer.tap()
            app.typeText("Steer only the running turn")
            press("control-return")
            XCTAssertTrue(app.staticTexts["Steer only the running turn"].firstMatch.waitForExistence(timeout: 10))
            XCTAssertTrue(app.buttons["介入当前轮，第 2 条待发消息"].firstMatch.exists)
            capture("Control-Return adds a message to the running turn and leaves the queue intact", app: app)
        }
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 2))
    }

    func testIPadParallelComposerUsesTheOtherWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_CROSS_WORKER_FIXTURE"] != nil else {
            throw XCTSkip("Requires two Workers with independent draft stores")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"]
        let rightName = "右栏设备会话"
        let remoteRow = app.buttons[rightName].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            XCTAssertFalse(remoteRow.exists)
            joinFixtureTailnet(peer, app: app)
        }
        XCTAssertTrue(remoteRow.waitForExistence(timeout: 12))
        left.tap()
        let close = app.buttons["关闭并排会话"]
        let right = app.textFields["右侧会话草稿"]
        func openRight() {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 10))
            activity.tap()
            let parallel = app.buttons["并排查看会话"]
            XCTAssertTrue(parallel.waitForExistence(timeout: 8))
            parallel.tap()
            let choice = app.buttons.matching(identifier: rightName).element(boundBy: 1)
            capture("Cross-worker choices", app: app)
            XCTAssertTrue(choice.waitForExistence(timeout: 8), "The picker must include sessions on the second Worker")
            choice.tap()
            XCTAssertTrue(close.waitForExistence(timeout: 8))
            XCTAssertTrue(right.waitForExistence(timeout: 8))
        }
        openRight()
        XCTAssertEqual(right.value as? String, "Second")
        right.tap()
        app.typeText(" Worker")
        XCTAssertEqual(right.value as? String, "Second Worker")
        close.tap()
        openRight()
        XCTAssertEqual(right.value as? String, "Second Worker")
        let send = app.buttons["发送右侧消息"]
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate { element, _ in
            guard let button = element as? XCUIElement else { return false }
            return button.exists && button.isEnabled
        }, object: send)
        XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 12), .completed)
        send.tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "from alpha: Second Worker")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertGreaterThan(reply.frame.midX, app.frame.midX)
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        XCTAssertTrue(left.exists)
        XCTAssertTrue(close.exists)
        XCTAssertNotEqual(right.value as? String, "Second Worker")
        capture("Cross-worker right-side reply", app: app)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 2.5))
    }

    func testIPadParallelFindLoadsTheRightHistoryIndependently() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_FIND_FIXTURE"] == "1" else {
            throw XCTSkip("Requires isolated parallel sessions with unloaded history")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        XCTAssertTrue(app.buttons["右栏设备会话"].firstMatch.waitForExistence(timeout: 12))
        left.tap()

        let closePane = app.buttons["关闭并排会话"]
        let leftFind = app.textFields["在会话中查找"]
        let rightFind = app.textFields["在右栏会话中查找"]
        let rightEntry = app.buttons["在右栏会话中查找"]
        let oldOutput = app.descendants(matching: .any).matching(identifier: "命令输出：历史输出工具").firstMatch

        func openActivity() {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
        }
        func openRight() {
            openActivity()
            let parallel = app.buttons["并排查看会话"]
            XCTAssertTrue(parallel.waitForExistence(timeout: 8))
            parallel.tap()
            let choice = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
            XCTAssertTrue(choice.waitForExistence(timeout: 8))
            choice.tap()
            XCTAssertTrue(closePane.waitForExistence(timeout: 8))
        }
        openRight()
        capture("Parallel session before opening find", app: app)
        XCTAssertTrue(rightEntry.waitForExistence(timeout: 6), "The right pane needs its own find action")
        XCTAssertFalse(oldOutput.exists)
        rightEntry.tap()
        XCTAssertTrue(rightFind.waitForExistence(timeout: 6))
        app.typeText("RIGHTKEY")
        XCTAssertTrue(app.staticTexts["1 / 2"].waitForExistence(timeout: 10))
        XCTAssertTrue(oldOutput.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertGreaterThan(oldOutput.frame.midX, app.frame.midX)
        capture("Right find reveals unloaded command output", app: app)

        openActivity()
        let leftMenu = app.buttons["在会话中查找"].firstMatch
        XCTAssertTrue(leftMenu.waitForExistence(timeout: 8))
        leftMenu.tap()
        XCTAssertTrue(leftFind.waitForExistence(timeout: 6))
        app.typeText("LEFTKEY")
        XCTAssertTrue(app.staticTexts["1 / 1"].waitForExistence(timeout: 10))
        XCTAssertEqual(leftFind.value as? String, "LEFTKEY")
        XCTAssertEqual(rightFind.value as? String, "RIGHTKEY")
        XCTAssertTrue(oldOutput.exists)
        capture("Both panes keep independent searches", app: app)

        app.buttons["右栏下一处匹配"].tap()
        XCTAssertTrue(app.staticTexts["2 / 2"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["RIGHTKEY latest"].waitForExistence(timeout: 10))
        XCTAssertEqual(leftFind.value as? String, "LEFTKEY")
        closePane.tap()
        XCTAssertTrue(rightFind.waitForNonExistence(timeout: 5))
        openRight()
        XCTAssertTrue(rightFind.waitForExistence(timeout: 8))
        XCTAssertEqual(rightFind.value as? String, "RIGHTKEY")
        XCTAssertEqual(leftFind.value as? String, "LEFTKEY")
        app.buttons["关闭右栏查找"].tap()
        let rightDraft = app.textFields["右侧会话草稿"]
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 6))
        XCTAssertEqual(rightDraft.value as? String, "Second")
        XCTAssertEqual(leftFind.value as? String, "LEFTKEY")
        capture("Closing right find restores its unsent draft", app: app)
    }

    func testIPadParallelFilePreviewUsesItsOwningWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_FILE_FIXTURE"] == "1" else {
            throw XCTSkip("Requires isolated sessions with workspace file links")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        XCTAssertTrue(app.buttons["右栏设备会话"].firstMatch.waitForExistence(timeout: 12))
        left.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        activity.tap()
        let parallel = app.buttons["并排查看会话"]
        XCTAssertTrue(parallel.waitForExistence(timeout: 8))
        parallel.tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()

        let leftLink = app.links["左栏文件链接"]
        let rightLink = app.links["右栏文件链接"]
        let closePreview = app.buttons["关闭文件预览"]
        let rightDraft = app.textFields["右侧会话草稿"]
        let rightRow = app.buttons["右栏设备会话"].firstMatch
        XCTAssertTrue(rightLink.waitForExistence(timeout: 12))
        XCTAssertTrue(leftLink.exists)
        rightLink.tap()
        let opened = closePreview.waitForExistence(timeout: 6)
        if !opened {
            capture("Right file hyperlink tap leaves the split unchanged", app: app)
        }
        XCTAssertTrue(opened, "A file tapped in the right pane must appear in the shared inspector")

        let rightFile = app.staticTexts["右侧设备的文件内容。"]
        let leftFile = app.staticTexts["左侧设备的文件内容。"]
        XCTAssertTrue(rightFile.waitForExistence(timeout: 10))
        XCTAssertTrue(rightLink.exists)
        XCTAssertFalse(leftFile.exists)
        XCTAssertEqual(rightDraft.value as? String, "Second")
        XCTAssertTrue(rightRow.isSelected)
        XCTAssertFalse(left.isSelected)
        capture("Right transcript beside the file from its Worker", app: app)

        let nested = app.links["打开相邻文件"]
        XCTAssertTrue(nested.waitForExistence(timeout: 5))
        nested.tap()
        XCTAssertTrue(app.staticTexts["相邻文件来自：右栏设备会话。"].waitForExistence(timeout: 8))
        XCTAssertTrue(rightLink.exists)
        capture("Nested right-side file resolves in the same workspace", app: app)
        app.buttons["返回上一个文件"].tap()
        XCTAssertTrue(rightFile.waitForExistence(timeout: 6))

        closePreview.tap()
        XCTAssertTrue(closePreview.waitForNonExistence(timeout: 6))
        XCTAssertTrue(leftLink.waitForExistence(timeout: 6))
        XCTAssertTrue(rightLink.exists)
        XCTAssertEqual(rightDraft.value as? String, "Second")
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        XCTAssertTrue(left.isSelected)
        XCTAssertFalse(rightRow.isSelected)
        capture("Closing the inspector restores both transcripts and drafts", app: app)

        rightLink.tap()
        XCTAssertTrue(rightFile.waitForExistence(timeout: 8))
        left.tap()
        XCTAssertTrue(closePreview.waitForNonExistence(timeout: 6))
        XCTAssertTrue(leftLink.waitForExistence(timeout: 6))
        XCTAssertTrue(rightLink.exists)

        leftLink.tap()
        XCTAssertTrue(leftFile.waitForExistence(timeout: 8))
        XCTAssertFalse(rightFile.exists)
        XCTAssertTrue(leftLink.exists)
        closePreview.tap()
        XCTAssertTrue(rightLink.waitForExistence(timeout: 6))
    }

    func testIPadParallelComposerSendsToItsOwnSession() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_PARALLEL_FIXTURE"] != nil else {
            throw XCTSkip("Requires two sessions on the isolated Worker")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let left = app.buttons["主栏回执校验"]
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        left.tap()
        let activity = app.buttons["会话活动"]
        XCTAssertTrue(activity.waitForExistence(timeout: 12))
        activity.tap()
        let parallel = app.buttons["并排查看会话"]
        XCTAssertTrue(parallel.waitForExistence(timeout: 8))
        parallel.tap()
        let choices = app.buttons.matching(identifier: "右栏回执校验")
        let rightChoice = choices.element(boundBy: 1)
        XCTAssertTrue(rightChoice.waitForExistence(timeout: 8))
        rightChoice.tap()
        let close = app.buttons["关闭并排会话"]
        XCTAssertTrue(close.waitForExistence(timeout: 8))
        capture("Parallel pane before typing", app: app)

        let right = app.textFields["右侧会话草稿"]
        XCTAssertTrue(right.waitForExistence(timeout: 5), "A right-pane draft must be editable in place")
        XCTAssertEqual(right.value as? String, "Right pane")
        right.tap()
        app.typeText(" test")
        XCTAssertEqual(right.value as? String, "Right pane test")
        let send = app.buttons["发送右侧消息"]
        XCTAssertTrue(send.waitForExistence(timeout: 5))
        send.tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "from alpha: Right pane test")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertGreaterThan(reply.frame.midX, app.frame.midX)
        XCTAssertTrue(close.exists)
        let leftEditor = app.textFields["消息"].firstMatch
        XCTAssertEqual(leftEditor.value as? String, "Left pane draft")
        XCTAssertNotEqual(right.value as? String, "Right pane test")
        capture("Parallel reply in its own pane", app: app)
    }

    func testTimeGroupingUsesInputDatesFromBothWorkers() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let remoteScope = environment["SHIOJI_TIME_GROUPING_SCOPE"] else {
            throw XCTSkip("Requires the two Workers with different persisted input dates")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let local = app.buttons["当前设备的归档校验"]
        let remote = app.buttons["另一设备的归档校验"]
        let today = app.buttons["今天"]
        let yesterday = app.buttons["昨天"]
        func choice(_ name: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", name)).firstMatch
        }
        let filter = app.buttons["会话视图"]
        XCTAssertTrue(local.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            XCTAssertFalse(remote.exists)
            joinFixtureTailnet(peer, app: app)
        }
        filter.tap()
        capture("Session grouping menu", app: app)
        let time = choice("按时间分组")
        XCTAssertTrue(time.waitForExistence(timeout: 6), "The session list needs the time grouping choice")
        time.tap()
        XCTAssertTrue(remote.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(today.exists)
        XCTAssertTrue(yesterday.exists)
        XCTAssertLessThan(today.frame.midY, local.frame.midY)
        XCTAssertLessThan(local.frame.midY, yesterday.frame.midY)
        XCTAssertLessThan(yesterday.frame.midY, remote.frame.midY)
        capture("Worker input dates grouped as today and yesterday", app: app)
        yesterday.tap()
        XCTAssertFalse(remote.exists)
        XCTAssertTrue(local.exists)
        yesterday.tap()
        XCTAssertTrue(remote.waitForExistence(timeout: 6))

        let scope = app.buttons["切换会话范围"]
        scope.tap()
        choice(remoteScope).tap()
        XCTAssertFalse(local.exists)
        XCTAssertFalse(yesterday.exists)
        filter.tap()
        XCTAssertFalse(choice("按工作区分组").exists)
        time.tap()
        XCTAssertTrue(yesterday.waitForExistence(timeout: 6))
        XCTAssertTrue(remote.exists)
        capture("Time grouping inside a single workspace", app: app)

        scope.tap()
        choice("全局视图").tap()
        XCTAssertTrue(today.waitForExistence(timeout: 8))
        XCTAssertTrue(yesterday.exists)
        app.terminate()
        app.launch()
        XCTAssertTrue(today.waitForExistence(timeout: 10))
        XCTAssertTrue(yesterday.waitForExistence(timeout: 10))
        XCTAssertTrue(local.exists && remote.exists)
    }

    func testGlobalWorkspaceGroupsAcrossDevices() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let remoteScope = environment["SHIOJI_WORKSPACE_REMOTE_SCOPE"],
              let localScope = environment["SHIOJI_WORKSPACE_LOCAL_SCOPE"] else {
            throw XCTSkip("Requires two enrolled workers with identically named workspaces")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let local = app.buttons["当前设备的归档校验"]
        let remote = app.buttons["另一设备的归档校验"]
        XCTAssertTrue(local.waitForExistence(timeout: 15))
        if let peerName = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            XCTAssertFalse(remote.exists, "The other worker has no direct route or disk cache")
            joinFixtureTailnet(peerName, app: app)
        }
        func choice(_ name: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", name)).firstMatch
        }
        app.buttons["会话视图"].tap()
        capture("Global view grouping menu", app: app)
        let workspaceGrouping = choice("按工作区分组")
        XCTAssertTrue(workspaceGrouping.waitForExistence(timeout: 6), "Global view needs the workspace grouping option")
        workspaceGrouping.tap()
        XCTAssertTrue(remote.waitForExistence(timeout: 12), app.debugDescription)
        let headers = app.buttons.matching(NSPredicate(format: "label == %@", "test"))
        XCTAssertEqual(headers.count, 2, app.debugDescription)
        capture("Separate groups for identical workspace names", app: app)
        headers.element(boundBy: 1).tap()
        XCTAssertFalse(remote.exists)
        XCTAssertTrue(local.exists)
        headers.element(boundBy: 1).tap()
        XCTAssertTrue(remote.waitForExistence(timeout: 6))
        app.buttons["切换会话范围"].tap()
        let remoteWorkspace = choice(remoteScope)
        XCTAssertTrue(remoteWorkspace.waitForExistence(timeout: 6), app.debugDescription)
        remoteWorkspace.tap()
        XCTAssertFalse(local.exists)
        XCTAssertTrue(remote.exists)
        app.buttons["会话视图"].tap()
        XCTAssertFalse(choice("按工作区分组").exists)
        let statusGrouping = choice("按状态分组")
        XCTAssertTrue(statusGrouping.exists)
        statusGrouping.tap()
        remote.tap()
        XCTAssertTrue(app.links["另一设备的完整记录"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertFalse(app.links["当前设备的完整记录"].exists)
        capture("Opened a regular session from its owning workspace", app: app)
        if !tablet {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(remote.waitForExistence(timeout: 8))
        XCTAssertFalse(local.exists)
        app.buttons["切换会话范围"].tap()
        choice("全局视图").tap()
        XCTAssertTrue(local.waitForExistence(timeout: 8))
        XCTAssertTrue(remote.exists)
        app.buttons["切换会话范围"].tap()
        choice(localScope).tap()
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 8))
        compose.tap()
        XCTAssertTrue(app.navigationBars["新建会话"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(app.buttons["选择工作区，test"].firstMatch.exists)
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(draft.value as? String, "草稿来自当前设备的归档校验")
        capture("Draft belongs to the selected workspace's worker", app: app)
        app.terminate()
        app.launch()
        XCTAssertTrue(local.waitForExistence(timeout: 12))
        XCTAssertEqual(headers.count, 2)
    }

    func testWorkspaceContextActionsArchiveAndRestore() {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview")
        func choose(_ title: String) {
            let item = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch
            XCTAssertTrue(item.waitForExistence(timeout: 6), title)
            item.tap()
        }
        let views = app.buttons["会话视图"].firstMatch
        views.tap()
        choose("按工作区分组")
        let workspace = app.buttons["Shioji iOS"].firstMatch
        let other = app.buttons["Shioji Mac"].firstMatch
        let session = app.buttons["检查移动端会话体验"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        workspace.press(forDuration: 0.8)
        XCTAssertTrue(app.descendants(matching: .any)["工作区设置"].firstMatch.waitForExistence(timeout: 6))
        XCTAssertTrue(app.descendants(matching: .any)["新建会话"].firstMatch.exists)
        XCTAssertTrue(app.descendants(matching: .any)["导入会话"].firstMatch.exists)
        capture("Native workspace context actions", app: app)
        choose("工作区设置")
        let settings = app.navigationBars["工作区设置"]
        XCTAssertTrue(settings.waitForExistence(timeout: 8))
        let archiveAction = app.buttons["归档工作区"].firstMatch
        if !archiveAction.isHittable { app.swipeUp() }
        XCTAssertTrue(archiveAction.waitForExistence(timeout: 6))
        archiveAction.tap()
        let confirmation = app.alerts["归档工作区？"]
        XCTAssertTrue(confirmation.waitForExistence(timeout: 6))
        XCTAssertTrue(confirmation.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Shioji iOS")).firstMatch.exists)
        confirmation.buttons["取消"].tap()
        XCTAssertTrue(settings.exists)
        settings.buttons["BackButton"].tap()
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        XCTAssertTrue(session.exists)
        workspace.press(forDuration: 0.8)
        choose("归档工作区")
        XCTAssertTrue(confirmation.waitForExistence(timeout: 6))
        capture("Workspace archive identifies its target", app: app)
        confirmation.buttons["归档工作区"].tap()
        XCTAssertTrue(workspace.waitForNonExistence(timeout: 8))
        XCTAssertFalse(session.exists)
        XCTAssertTrue(other.exists)
        views.tap()
        choose("归档")
        let archive = app.navigationBars["已归档"]
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        app.buttons["工作区"].tap()
        let archivedWorkspace = app.buttons["Shioji iOS"].firstMatch
        XCTAssertTrue(archivedWorkspace.waitForExistence(timeout: 8))
        archivedWorkspace.press(forDuration: 0.8)
        choose("恢复工作区")
        XCTAssertTrue(app.staticTexts["没有归档工作区"].waitForExistence(timeout: 8))
        archive.buttons["BackButton"].tap()
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        XCTAssertTrue(session.exists && other.exists)

        workspace.press(forDuration: 0.8)
        choose("导入会话")
        let history = app.navigationBars["导入会话"]
        XCTAssertTrue(history.waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["检查旧版 SSH 配置"].waitForExistence(timeout: 8))
        history.buttons["BackButton"].tap()
        XCTAssertTrue(workspace.waitForExistence(timeout: 8))
        workspace.press(forDuration: 0.8)
        choose("新建会话")
        XCTAssertTrue(app.navigationBars["新建会话"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["选择工作区，Shioji iOS"].firstMatch.exists)
    }

    func testNewConversationOffersWorkspacesAfterAllAreArchived() {
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview")
        func choose(_ title: String) {
            let item = app.descendants(matching: .any)[title].firstMatch
            XCTAssertTrue(item.waitForExistence(timeout: 6), title)
            item.tap()
        }
        let views = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(views.waitForExistence(timeout: 8))
        views.tap()
        choose("按工作区分组")
        for title in ["Shioji iOS", "Shioji Mac"] {
            let workspace = app.buttons[title].firstMatch
            XCTAssertTrue(workspace.waitForExistence(timeout: 6))
            workspace.press(forDuration: 0.8)
            choose("归档工作区")
            let confirm = app.alerts["归档工作区？"].buttons["归档工作区"]
            XCTAssertTrue(confirm.waitForExistence(timeout: 6))
            confirm.tap()
            XCTAssertTrue(workspace.waitForNonExistence(timeout: 8))
        }
        app.buttons["新建会话"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["工作区已全部归档"].waitForExistence(timeout: 6))
        XCTAssertFalse(app.textFields["消息"].exists)
        XCTAssertTrue(app.buttons["创建工作区"].exists)
        capture("New conversation with every workspace archived", app: app)
        app.buttons["查看已归档"].tap()
        XCTAssertTrue(app.navigationBars["已归档"].waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["Shioji iOS"].firstMatch.exists)
        XCTAssertTrue(app.buttons["Shioji Mac"].firstMatch.exists)
    }

    func testScopedWorkspaceArchiveTargetsTheOwningWorker() throws {
        guard let scope = ProcessInfo.processInfo.environment["SHIOJI_WORKSPACE_ARCHIVE_SCOPE"] else {
            throw XCTSkip("Requires two enrolled Workers with identically named workspaces")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let local = app.buttons["当前设备的归档校验"]
        let remote = app.buttons["另一设备的归档校验"]
        XCTAssertTrue(local.waitForExistence(timeout: 18))
        XCTAssertTrue(remote.waitForExistence(timeout: 18))
        func choose(_ title: String) {
            let item = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", title)).firstMatch
            XCTAssertTrue(item.waitForExistence(timeout: 6), title)
            item.tap()
        }
        let scopeButton = app.buttons["切换会话范围"].firstMatch
        scopeButton.tap()
        choose(scope)
        XCTAssertTrue(remote.exists)
        XCTAssertFalse(local.exists)
        app.buttons["会话视图"].firstMatch.tap()
        choose("归档工作区")
        let confirmation = app.alerts["归档工作区？"]
        XCTAssertTrue(confirmation.waitForExistence(timeout: 10))
        capture("Archive in the selected remote workspace", app: app)
        confirmation.buttons["归档工作区"].tap()
        XCTAssertTrue(remote.waitForNonExistence(timeout: 10))
        XCTAssertTrue(local.waitForExistence(timeout: 10))
        app.buttons["会话视图"].firstMatch.tap()
        choose("归档")
        let archive = app.navigationBars["已归档"]
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        app.buttons["工作区"].tap()
        let target = app.buttons["test"].firstMatch
        XCTAssertTrue(target.waitForExistence(timeout: 8))
        target.press(forDuration: 0.8)
        choose("恢复工作区")
        XCTAssertTrue(target.waitForNonExistence(timeout: 10))
        archive.buttons["BackButton"].tap()
        XCTAssertTrue(remote.waitForExistence(timeout: 10))
        XCTAssertTrue(local.exists)
    }

    func testNativeWorkspaceAndSessionOrdering() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview")
        func option(_ label: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", label)).firstMatch
        }
        func views() {
            let button = app.buttons["会话视图"].firstMatch
            XCTAssertTrue(button.waitForExistence(timeout: 8))
            button.tap()
        }
        func choose(_ label: String) {
            let entry = option(label)
            XCTAssertTrue(entry.waitForExistence(timeout: 6), label)
            entry.tap()
        }
        func moveBefore(_ source: String, _ destination: String, in title: String, save: Bool = true) {
            let bar = app.navigationBars[title]
            XCTAssertTrue(bar.waitForExistence(timeout: 8))
            let list = app.tables["navigation-ordering"]
            let from = list.cells.containing(.staticText, identifier: source).firstMatch
            let to = list.cells.containing(.staticText, identifier: destination).firstMatch
            XCTAssertTrue(from.waitForExistence(timeout: 5))
            XCTAssertTrue(to.exists)
            let handle = from.coordinate(withNormalizedOffset: CGVector(dx: 0.96, dy: 0.52))
            let target = to.coordinate(withNormalizedOffset: CGVector(dx: 0.96, dy: 0.18))
            handle.press(forDuration: 0.12, thenDragTo: target, withVelocity: .slow, thenHoldForDuration: 0.18)
            XCTAssertLessThan(from.frame.midY, to.frame.midY)
            capture(title, app: app)
            bar.buttons[save ? "完成" : "取消"].tap()
            XCTAssertTrue(bar.waitForNonExistence(timeout: 6))
        }

        views()
        choose("按工作区分组")
        let ios = app.buttons["Shioji iOS"].firstMatch
        let mac = app.buttons["Shioji Mac"].firstMatch
        XCTAssertTrue(ios.waitForExistence(timeout: 8))
        XCTAssertTrue(mac.exists)
        XCTAssertLessThan(ios.frame.midY, mac.frame.midY)
        views()
        choose("排列工作区")
        moveBefore("Shioji Mac", "Shioji iOS", in: "排列工作区", save: false)
        XCTAssertLessThan(ios.frame.midY, mac.frame.midY)
        views()
        choose("排列工作区")
        moveBefore("Shioji Mac", "Shioji iOS", in: "排列工作区")
        XCTAssertLessThan(mac.frame.midY, ios.frame.midY)

        app.buttons["切换会话范围"].tap()
        choose("Shioji iOS · Studio Display Mac")
        views()
        choose("排列会话")
        moveBefore("整理键盘交互和阅读位置", "检查移动端会话体验", in: "排列会话")
        let first = app.buttons["整理键盘交互和阅读位置"].firstMatch
        let second = app.buttons["检查移动端会话体验"].firstMatch
        XCTAssertTrue(first.exists && second.exists)
        XCTAssertLessThan(first.frame.midY, second.frame.midY)
        XCTAssertFalse(app.buttons["核对本轮后台任务"].exists)
    }

    func testNavigationOrderingSupportsTheMacKeyboardReorderShortcuts() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview")
        let views = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(views.waitForExistence(timeout: 8))
        func openOrdering() {
            views.tap()
            let arrange = app.descendants(matching: .any)["排列工作区"].firstMatch
            XCTAssertTrue(arrange.waitForExistence(timeout: 6))
            arrange.tap()
        }
        openOrdering()
        let bar = app.navigationBars["排列工作区"]
        let list = app.tables["navigation-ordering"]
        let ios = list.cells.containing(.staticText, identifier: "Shioji iOS").firstMatch
        let mac = list.cells.containing(.staticText, identifier: "Shioji Mac").firstMatch
        XCTAssertTrue(mac.waitForExistence(timeout: 8))
        XCTAssertLessThan(ios.frame.midY, mac.frame.midY)
        mac.staticTexts["Shioji Mac"].tap()
        app.typeKey(XCUIKeyboardKey.upArrow.rawValue, modifierFlags: .option)
        XCTAssertLessThan(mac.frame.midY, ios.frame.midY)
        XCTAssertTrue(bar.buttons["完成"].isEnabled)
        capture("Option-Up moves the selected workspace", app: app)
        app.typeKey(XCUIKeyboardKey.downArrow.rawValue, modifierFlags: .option)
        XCTAssertLessThan(ios.frame.midY, mac.frame.midY)
        XCTAssertFalse(bar.buttons["完成"].isEnabled)
        app.typeKey(XCUIKeyboardKey.upArrow.rawValue, modifierFlags: .option)
        bar.buttons["完成"].tap()
        XCTAssertTrue(bar.waitForNonExistence(timeout: 6))
        openOrdering()
        XCTAssertTrue(mac.waitForExistence(timeout: 6))
        XCTAssertLessThan(mac.frame.midY, ios.frame.midY)
    }

    func testNativePinOrderPersistsAcrossWorkers() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_PINNED_DEVICES"] == "1" else {
            throw XCTSkip("Requires two enrolled workers with existing pins")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let localTitle = "当前设备的归档校验"
        let remoteTitle = "另一设备的归档校验"
        let local = app.buttons[localTitle]
        let remote = app.buttons[remoteTitle]
        XCTAssertTrue(local.waitForExistence(timeout: 15))
        XCTAssertTrue(remote.waitForExistence(timeout: 15))
        XCTAssertLessThan(local.frame.midY, remote.frame.midY)
        app.buttons["会话视图"].tap()
        app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "排列置顶会话")).firstMatch.tap()
        let bar = app.navigationBars["排列置顶会话"]
        XCTAssertTrue(bar.waitForExistence(timeout: 8))
        let list = app.tables["navigation-ordering"]
        let source = list.cells.containing(.staticText, identifier: remoteTitle).firstMatch
        let destination = list.cells.containing(.staticText, identifier: localTitle).firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 5))
        XCTAssertTrue(destination.exists)
        source.coordinate(withNormalizedOffset: CGVector(dx: 0.96, dy: 0.52)).press(
            forDuration: 0.12, thenDragTo: destination.coordinate(withNormalizedOffset: CGVector(dx: 0.96, dy: 0.18)),
            withVelocity: .slow, thenHoldForDuration: 0.18)
        XCTAssertLessThan(source.frame.midY, destination.frame.midY)
        bar.buttons["完成"].tap()
        XCTAssertTrue(bar.waitForNonExistence(timeout: 6))
        XCTAssertLessThan(remote.frame.midY, local.frame.midY)
        app.terminate()
        app.launch()
        XCTAssertTrue(remote.waitForExistence(timeout: 15))
        XCTAssertTrue(local.waitForExistence(timeout: 15))
        XCTAssertLessThan(remote.frame.midY, local.frame.midY)
        remote.tap()
        XCTAssertTrue(app.links["另一设备的完整记录"].waitForExistence(timeout: 15))
        XCTAssertFalse(app.links["当前设备的完整记录"].exists)
        capture("Reordered pin reopens its owning worker after relaunch", app: app)
    }

    func testPinnedSessionsOpenAcrossEnrolledDevices() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PINNED_DEVICES"] == "1" else {
            throw XCTSkip("Requires two enrolled workers with existing pins")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let first = app.buttons["当前设备的归档校验"]
        let second = app.buttons["另一设备的归档校验"]
        XCTAssertTrue(first.waitForExistence(timeout: 20))
        if let peerName = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peerName, app: app)
            XCTAssertTrue(first.waitForExistence(timeout: 8))
        }
        func assertRestingToolbar() {
            guard !tablet else { return }
            let scope = app.buttons["切换会话范围"].firstMatch
            XCTAssertTrue(scope.waitForExistence(timeout: 5))
            let nearStatusArea = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
                scope.isHittable && scope.frame.minY < app.frame.minY + 100
            }, object: nil)
            let arrived = XCTWaiter.wait(for: [nearStatusArea], timeout: 4) == .completed
            capture(arrived ? "Home toolbar beneath the status area" : "Blank inset above the resting home toolbar", app: app)
            print("Home toolbar position: \(scope.frame.minY) pt")
            XCTAssertTrue(arrived, "The resting toolbar starts at \(scope.frame.minY) pt")
        }
        assertRestingToolbar()
        capture("Pinned sessions on the home list", app: app)
        if !tablet {
            app.buttons["切换会话范围"].firstMatch.tap()
            let global = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "全局视图")).firstMatch
            XCTAssertTrue(global.waitForExistence(timeout: 5), "The scope control must open its menu at the top edge")
            global.tap()
        }
        XCTAssertTrue(second.waitForExistence(timeout: 8), "The home list must include the other enrolled device's pin")
        XCTAssertEqual(app.buttons.matching(identifier: "当前设备的归档校验").count, 1)
        XCTAssertEqual(app.buttons.matching(identifier: "另一设备的归档校验").count, 1)
        second.tap()
        XCTAssertTrue(app.links["另一设备的完整记录"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertFalse(app.links["当前设备的完整记录"].exists)
        capture("Pinned conversation opened on its owning worker", app: app)
        if !tablet {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(first.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(second.exists)
        assertRestingToolbar()
        first.tap()
        XCTAssertTrue(app.links["当前设备的完整记录"].waitForExistence(timeout: 15), app.debugDescription)
        app.terminate()
        app.launch()
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        XCTAssertTrue(second.exists)
        XCTAssertLessThan(first.frame.midY, second.frame.midY)
        first.press(forDuration: 0.7)
        let moveDown = app.buttons["下移"]
        XCTAssertTrue(moveDown.waitForExistence(timeout: 6), app.debugDescription)
        moveDown.tap()
        let order = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in second.frame.midY < first.frame.midY }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [order], timeout: 6), .completed)
        capture("Cross-device pinned order after moving a session", app: app)
        app.terminate()
        app.launch()
        XCTAssertTrue(second.waitForExistence(timeout: 10))
        XCTAssertLessThan(second.frame.midY, first.frame.midY)
        second.press(forDuration: 0.7)
        let unpin = app.buttons["取消置顶"]
        XCTAssertTrue(unpin.waitForExistence(timeout: 6), app.debugDescription)
        unpin.tap()
        let removed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            second.exists && app.buttons["刚完成"].exists && first.frame.midY < second.frame.midY
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [removed], timeout: 6), .completed)
        XCTAssertTrue(first.exists)
        app.terminate()
        app.launch()
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        XCTAssertTrue(second.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["刚完成"].exists)
        XCTAssertLessThan(first.frame.midY, second.frame.midY)
    }

    func testWorkerSearchJumpsIntoTheOriginalConversation() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_HISTORY_WORKER_SESSION"] else {
            throw XCTSkip("Requires the isolated history search worker fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        XCTAssertTrue(app.buttons[title].waitForExistence(timeout: 20), app.debugDescription)
        app.buttons["会话视图"].tap()
        let fullSearch = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "全文搜索")).firstMatch
        XCTAssertTrue(fullSearch.waitForExistence(timeout: 6), app.debugDescription)
        fullSearch.tap()
        XCTAssertTrue(app.navigationBars["全文搜索"].waitForExistence(timeout: 8))
        let field = app.textFields["搜索会话与对话内容"]
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5))
        for letter in "cursor" {
            let lower = keyboard.keys[String(letter)]
            (lower.exists ? lower : keyboard.keys[String(letter).uppercased()]).tap()
        }
        // The simulator can use Pinyin. Commit the English candidate so the
        // search receives committed text rather than active IME composition.
        if (field.value as? String)?.isEmpty == true {
            let candidate = app.cells["cursor"].firstMatch
            XCTAssertTrue(candidate.waitForExistence(timeout: 5), app.debugDescription)
            candidate.tap()
        }
        XCTAssertEqual((field.value as? String)?.trimmingCharacters(in: .whitespaces).lowercased(), "cursor", app.debugDescription)
        app.buttons["搜索"].tap()
        let hit = app.buttons[title].firstMatch
        XCTAssertTrue(hit.waitForExistence(timeout: 10), app.debugDescription)
        hit.tap()
        XCTAssertTrue(app.buttons["会话活动"].waitForExistence(timeout: 8))
        let oldLink = app.links["查看旧记录来源"]
        XCTAssertTrue(oldLink.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(oldLink.isHittable, app.debugDescription)
        XCTAssertTrue(app.buttons["发送"].exists)
        XCTAssertFalse(app.buttons["下一条"].exists)
        capture("Global search opens the original conversation at the old message", app: app)
        let latest = app.buttons["回到最新消息"]
        XCTAssertTrue(latest.waitForExistence(timeout: 8), app.debugDescription)
        latest.tap()
        XCTAssertTrue(app.links["查看新记录来源"].waitForExistence(timeout: 12), app.debugDescription)
    }

    func testMarkdownRendersMermaidAndKeepsSourceAvailable() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-transcript-mermaid")
        let report = app.links["打开重连流程说明"]
        XCTAssertTrue(report.waitForExistence(timeout: 8))
        capture("Mermaid in conversation before interaction", app: app)
        let graph = app.images["Mermaid 图表"].firstMatch
        guard graph.waitForExistence(timeout: 5) else { return XCTFail("The closed Mermaid block did not render a native diagram") }
        XCTAssertGreaterThan(graph.frame.width, 180)
        XCTAssertGreaterThan(graph.frame.height, 28)
        let source = app.buttons["查看 Mermaid 源码"].firstMatch
        XCTAssertTrue(source.exists)
        capture("Rendered conversation diagram", app: app)
        source.tap()
        let preview = app.buttons["查看 Mermaid 图表"].firstMatch
        XCTAssertTrue(preview.waitForExistence(timeout: 4))
        XCTAssertFalse(graph.exists)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "flowchart LR")).firstMatch.exists)
        capture("Conversation diagram source", app: app)
        preview.tap()
        XCTAssertTrue(graph.waitForExistence(timeout: 4))
        report.tap()
        if tablet {
            XCTAssertTrue(app.buttons["关闭文件预览"].waitForExistence(timeout: 6), app.debugDescription)
        } else {
            XCTAssertTrue(app.navigationBars["reconnect-diagram.md"].waitForExistence(timeout: 6), app.debugDescription)
        }
        let inspector = tablet ? app.images.matching(identifier: "Mermaid 图表").element(boundBy: 1) : graph
        XCTAssertTrue(inspector.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertGreaterThan(inspector.frame.height, 80)
        capture("Mermaid in Markdown file preview", app: app)
    }

    func testMermaidFallbackKeepsOrdinaryAndStreamingCode() {
        let app = launch("--preview-board-transcript-mermaid-fallback")
        let ordinary = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "normalCode")).firstMatch
        XCTAssertTrue(ordinary.waitForExistence(timeout: 8), app.debugDescription)
        capture("Invalid and unclosed Mermaid source", app: app)
        XCTAssertTrue(app.staticTexts["无法渲染图表，已显示源码"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "invalid diagram")).firstMatch.exists)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "正在接收")).firstMatch.exists)
        XCTAssertFalse(app.images["Mermaid 图表"].exists)
        XCTAssertFalse(app.buttons["查看 Mermaid 源码"].exists)
    }

    func testConversationTCPLinksCopyAndOpenFromTheirOwningWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_LINK_FIXTURE"] == "1", let destination = environment["SHIOJI_LINK_DEST"] else {
            throw XCTSkip("Requires an enrolled Worker and its private HTTP listener")
        }
        continueAfterFailure = false
        let rightPane = environment["SHIOJI_LINK_RIGHT_PANE"] == "1"
        XCUIDevice.shared.orientation = rightPane ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        if rightPane {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
            app.buttons["并排查看会话"].tap()
            app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1).tap()
            XCTAssertTrue(app.buttons["关闭并排会话"].waitForExistence(timeout: 8))
        }
        let userLink = app.links[destination].firstMatch
        let previewLink = app.links["网页预览"].firstMatch
        XCTAssertTrue(previewLink.waitForExistence(timeout: 8), app.debugDescription)
        let hasLink = userLink.waitForExistence(timeout: 5)
        if !hasLink {
            capture("Plain message with an inert URL before the change", app: app)
        }
        XCTAssertTrue(hasLink, "A plain user URL must be exposed as an accessible link")
        let firstLine = userLink.coordinate(withNormalizedOffset: CGVector(dx: 0.62, dy: 0.22))
        firstLine.press(forDuration: 0.85)
        let copy = app.descendants(matching: .any).matching(identifier: "复制链接").firstMatch
        XCTAssertTrue(copy.waitForExistence(timeout: 6), app.debugDescription)
        let tunnel = app.descendants(matching: .any).matching(identifier: "端口转发并打开").firstMatch
        XCTAssertTrue(tunnel.exists)
        capture("A plain user URL has copy and owning-device actions", app: app)
        copy.tap()
        XCTAssertTrue(copy.waitForNonExistence(timeout: 5))

        previewLink.press(forDuration: 0.85)
        XCTAssertTrue(tunnel.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(copy.exists)
        capture("An assistant Markdown link shows the same actions", app: app)
        tunnel.tap()

        let webText = app.staticTexts["所属设备已打开预览"]
        XCTAssertTrue(webText.waitForExistence(timeout: 14), app.debugDescription)
        XCTAssertTrue(app.staticTexts["验证路径与查询参数。"].exists)
        capture("The native browser loads the page through its Worker", app: app)
        let done = app.buttons.matching(NSPredicate(format: "label IN %@", ["Done", "完成"])).firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 6))
        done.coordinate(withNormalizedOffset: CGVector(dx: 0.20, dy: 0.5)).tap()
        XCTAssertTrue(previewLink.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(userLink.exists)
    }

    func testConversationLinksExportToFilesFromTheirOwningWorker() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_LINK_FIXTURE"] == "1", environment["SHIOJI_LINK_EXPORT"] == "1" else {
            throw XCTSkip("Requires the enrolled Worker and its download fixture")
        }
        continueAfterFailure = false
        let rightPane = environment["SHIOJI_LINK_RIGHT_PANE"] == "1"
        XCUIDevice.shared.orientation = rightPane ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        if rightPane {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
            app.buttons["并排查看会话"].tap()
            app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1).tap()
            XCTAssertTrue(app.buttons["关闭并排会话"].waitForExistence(timeout: 8))
        }
        let link = app.links["下载验收文件"].firstMatch
        XCTAssertTrue(link.waitForExistence(timeout: 12), app.debugDescription)
        link.press(forDuration: 0.85)
        let save = app.buttons["另存为…"].firstMatch
        let canSave = save.waitForExistence(timeout: 4)
        capture(canSave ? "Export available from the link menu" : "Link menu before export", app: app)
        XCTAssertTrue(canSave, "A remote HTTP resource needs an export action")
        save.tap()
        let progress = app.alerts["正在读取链接…"]
        XCTAssertTrue(progress.waitForExistence(timeout: 6), app.debugDescription)
        capture("Slow transfer with a native cancel action", app: app)
        progress.buttons["取消"].tap()
        XCTAssertTrue(progress.waitForNonExistence(timeout: 5))
        XCTAssertTrue(link.exists)
        capture("Canceled the download and returned to the same conversation", app: app)

        let pickerSave = app.buttons["DOCPicker.actionButton"].firstMatch
        let filename = app.textFields["DOCPicker.filenameTextField"].firstMatch
        link.press(forDuration: 0.85)
        XCTAssertTrue(save.waitForExistence(timeout: 5))
        save.tap()
        XCTAssertTrue(pickerSave.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertEqual(filename.value as? String, "shioji-export-check")
        capture("The system Files picker exports the response", app: app)
        XCTAssertTrue(pickerSave.isEnabled, app.debugDescription)
        pickerSave.tap()
        XCTAssertTrue(pickerSave.waitForNonExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(link.exists)

        let file = app.links["远端工作区文件"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 6), app.debugDescription)
        file.press(forDuration: 0.85)
        XCTAssertTrue(save.waitForExistence(timeout: 5), app.debugDescription)
        save.tap()
        XCTAssertTrue(pickerSave.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertEqual(filename.value as? String, "shioji-worker-export")
        capture("The system Files picker exports the workspace file", app: app)
        pickerSave.tap()
        XCTAssertTrue(pickerSave.waitForNonExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(file.exists)
    }

    func testIPadParallelQueueManagesOnlyItsOwningSession() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_QUEUE_FIXTURE"] == "1", UIDevice.current.userInterfaceIdiom == .pad else {
            throw XCTSkip("Requires an iPad and paused queues on enrolled Workers")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["并排查看会话"].tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()
        let rightDraft = app.textFields["右侧会话草稿"].firstMatch
        let leftDraft = app.textFields["消息"].firstMatch
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 8))
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("Before managing the right queue", app: app)

        let alpha = app.staticTexts["Right queue alpha"].firstMatch
        let beta = app.staticTexts["Right queue beta"].firstMatch
        let gamma = app.staticTexts["Right queue gamma"].firstMatch
        let delta = app.staticTexts["Right queue delta"].firstMatch
        XCTAssertTrue(alpha.waitForExistence(timeout: 8), "An enrolled right session must show its own queue")
        XCTAssertTrue(beta.exists)
        let leftQueued = app.staticTexts["Left queued message"].firstMatch
        XCTAssertTrue(leftQueued.exists)
        let paneStart = rightDraft.frame.minX - 20
        func actions(for item: XCUIElement) throws -> XCUIElement {
            let buttons = app.buttons.matching(identifier: "待发消息操作").allElementsBoundByIndex
            return try XCTUnwrap(buttons.first { $0.isHittable && $0.frame.midX > paneStart && abs($0.frame.midY-item.frame.midY) < 24 })
        }
        func choose(_ title: String, for item: XCUIElement) throws {
            try actions(for: item).tap()
            let choice = app.descendants(matching: .any).matching(identifier: title).firstMatch
            XCTAssertTrue(choice.waitForExistence(timeout: 6), app.debugDescription)
            choice.tap()
        }
        let lists = app.descendants(matching: .any).matching(identifier: "待发消息列表").allElementsBoundByIndex
        let list = try XCTUnwrap(lists.first { $0.frame.midX > paneStart })
        XCTAssertEqual(list.frame.height, 125, accuracy: 1)
        XCTAssertLessThan(gamma.frame.minY, list.frame.maxY)
        XCTAssertGreaterThanOrEqual(delta.frame.minY, list.frame.maxY)
        capture("The right queue shows two and a half rows", app: app)
        list.swipeUp()
        let revealed = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            list.frame.contains(CGPoint(x: delta.frame.midX, y: delta.frame.midY))
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [revealed], timeout: 6), .completed)
        list.swipeDown()
        let atStart = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            list.frame.contains(CGPoint(x: alpha.frame.midX, y: alpha.frame.midY))
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [atStart], timeout: 6), .completed)

        try choose("编辑", for: alpha)
        let save = app.buttons["保存待发消息"].firstMatch
        XCTAssertTrue(save.waitForExistence(timeout: 8))
        XCTAssertEqual(rightDraft.value as? String, "Right queue alpha")
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        capture("A right queue item opens in its own composer", app: app)
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 5))
        XCTAssertEqual(rightDraft.value as? String, "Second")
        try choose("编辑", for: alpha)
        rightDraft.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.45)).tap()
        app.typeText(" revised")
        XCTAssertEqual(rightDraft.value as? String, "Right queue alpha revised")
        save.tap()
        let revised = app.staticTexts["Right queue alpha revised"].firstMatch
        XCTAssertTrue(revised.waitForExistence(timeout: 10))
        XCTAssertEqual(rightDraft.value as? String, "Second")
        try choose("下移", for: revised)
        let reordered = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in beta.frame.midY < revised.frame.midY }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [reordered], timeout: 8), .completed)
        capture("The right queue retains the saved text in its new order", app: app)

        let send = app.buttons["发送第 2 条待发消息"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 8))
        send.tap()
        XCTAssertTrue(app.staticTexts["from alpha: Right queue alpha revised"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(beta.exists)
        try choose("移除", for: gamma)
        XCTAssertTrue(gamma.waitForNonExistence(timeout: 8))
        let resumes = app.buttons.matching(identifier: "恢复").allElementsBoundByIndex
        let resume = try XCTUnwrap(resumes.first { $0.frame.midX > paneStart })
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            resume.isEnabled && resume.isHittable && abs(list.frame.height - 100) < 1
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 8), .completed, app.debugDescription)
        resume.tap()
        XCTAssertTrue(app.staticTexts["from alpha: Right queue delta"].waitForExistence(timeout: 14), app.debugDescription)
        XCTAssertTrue(leftQueued.exists)
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("Sending and resuming the right queue leaves the left queue paused", app: app)
    }

    func testRequestReceiptsStayWithTheirOriginalMessages() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_INLINE_RECEIPTS_FIXTURE"] == "1" else { throw XCTSkip("Requires an enrolled Worker with saved request receipts") }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] { app.launchEnvironment["TS_CONTROL_URL"] = control }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] { joinFixtureTailnet(peer, app: app) }
        left.tap()
        let activity = app.buttons["会话活动"].firstMatch
        XCTAssertTrue(activity.waitForExistence(timeout: 8))
        var rightStart: CGFloat = 0
        if tablet {
            activity.tap()
            app.buttons["并排查看会话"].tap()
            let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
            XCTAssertTrue(right.waitForExistence(timeout: 8))
            right.tap()
            let editor = app.textFields["右侧会话草稿"].firstMatch
            XCTAssertTrue(editor.waitForExistence(timeout: 10))
            rightStart = editor.frame.minX - 20
        }
        let side = tablet ? "右栏" : "左栏"
        let laterReply = app.staticTexts[side + "后续回复"].firstMatch
        XCTAssertTrue(laterReply.waitForExistence(timeout: 8))
        capture("Saved request receipts in the conversation", app: app)
        let questions = app.buttons.matching(identifier: "已回答 2 个问题")
        let projectQuestions = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            questions.allElementsBoundByIndex.contains { $0.frame.midX > rightStart }
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [projectQuestions], timeout: 6), .completed, "The original question must disclose its saved answers")
        let summary = try XCTUnwrap(questions.allElementsBoundByIndex.first { $0.frame.midX > rightStart })
        XCTAssertLessThan(summary.frame.minY, laterReply.frame.minY)
        let answer = app.staticTexts.matching(identifier: side + "保留现有记录")
        XCTAssertFalse(answer.firstMatch.exists, "The answer must not be repeated by a later standalone row")
        summary.tap()
        XCTAssertTrue(answer.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(answer.count, 1)
        XCTAssertLessThan(answer.firstMatch.frame.minY, laterReply.frame.minY)
        capture("A saved answer expands under its request", app: app)
        summary.tap()
        XCTAssertTrue(answer.firstMatch.waitForNonExistence(timeout: 5))

        let decision = app.staticTexts[side + "命令核对 · 拒绝这一次"].firstMatch
        XCTAssertTrue(decision.exists)
        XCTAssertLessThan(decision.frame.minY, laterReply.frame.minY)
        let permission = app.buttons[side + "目录访问 · 允许本轮"].firstMatch
        XCTAssertTrue(permission.exists)
        permission.tap()
        let scope = app.staticTexts.matching(identifier: side + "只读目录")
        XCTAssertTrue(scope.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(scope.count, 1)
        XCTAssertTrue(app.staticTexts[side + "表单回执"].exists)
        XCTAssertFalse(app.staticTexts[side + "仅子任务可见"].exists)
        capture("The permission receipt discloses its granted scope", app: app)

        if !tablet {
            activity.tap()
            app.buttons["子任务回执"].firstMatch.tap()
            let child = app.buttons["已回答 · 左栏仅子任务可见"].firstMatch
            XCTAssertTrue(child.waitForExistence(timeout: 8), app.debugDescription)
            child.tap()
            XCTAssertTrue(app.staticTexts["子任务范围"].firstMatch.waitForExistence(timeout: 5))
            XCTAssertEqual(app.staticTexts.matching(identifier: "左栏仅子任务可见").count, 1)
            capture("The child receipt remains inside its activity", app: app)
        }
    }

    func testIPadParallelGoalsStayAboveEachQueueAndControlOnlyTheirSession() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_GOAL_FIXTURE"] == "1", UIDevice.current.userInterfaceIdiom == .pad else {
            throw XCTSkip("Requires goals and paused queues on enrolled iPad sessions")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["并排查看会话"].tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()
        let rightDraft = app.textFields["右侧会话草稿"].firstMatch
        let leftDraft = app.textFields["消息"].firstMatch
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 10))

        let summaries = app.buttons.matching(identifier: "目标摘要")
        func goal(_ objective: String) -> XCUIElement {
            summaries.matching(NSPredicate(format: "value CONTAINS %@", objective)).firstMatch
        }
        let leftGoal = goal("核对左侧目标")
        let rightGoal = goal("核对右侧目标")
        let leftQueue = app.staticTexts["Left queued message"].firstMatch
        let rightQueue = app.staticTexts["Right queue alpha"].firstMatch
        XCTAssertTrue(leftGoal.waitForExistence(timeout: 8))
        XCTAssertTrue(rightQueue.waitForExistence(timeout: 8))
        capture("The goal and queue at both conversation docks", app: app)
        continueAfterFailure = true
        XCTAssertLessThan(leftGoal.frame.maxY, leftQueue.frame.minY, "The left goal belongs above the pending queue")
        XCTAssertTrue(rightGoal.waitForExistence(timeout: 6), "The right dock must show its own goal")
        continueAfterFailure = false
        guard rightGoal.exists else { return }
        XCTAssertLessThan(rightGoal.frame.maxY, rightQueue.frame.minY)

        let rightStart = rightDraft.frame.minX - 20
        func rightButton(_ label: String) throws -> XCUIElement {
            let buttons = app.buttons.matching(identifier: label)
            XCTAssertTrue(buttons.firstMatch.waitForExistence(timeout: 8))
            return try XCTUnwrap(buttons.allElementsBoundByIndex.first { $0.frame.midX > rightStart })
        }
        try rightButton("暂停目标").tap()
        let resume = try rightButton("恢复目标")
        XCTAssertTrue((rightGoal.value as? String)?.contains("已暂停") ?? false)
        XCTAssertTrue((leftGoal.value as? String)?.contains("执行中") ?? false)
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in resume.isEnabled && resume.isHittable }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 8), .completed, app.debugDescription)
        capture("Only the right goal is paused", app: app)
        resume.tap()
        let active = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            (rightGoal.value as? String)?.contains("执行中") ?? false
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [active], timeout: 10), .completed)

        try rightButton("目标操作").tap()
        app.descendants(matching: .any)["清除目标"].firstMatch.tap()
        let confirmation = app.alerts["清除目标？"].firstMatch
        XCTAssertTrue(confirmation.waitForExistence(timeout: 6))
        confirmation.buttons["取消"].tap()
        XCTAssertTrue(rightGoal.exists)
        try rightButton("目标操作").tap()
        app.descendants(matching: .any)["清除目标"].firstMatch.tap()
        XCTAssertTrue(confirmation.waitForExistence(timeout: 6))
        confirmation.buttons["清除目标"].tap()
        XCTAssertTrue(rightGoal.waitForNonExistence(timeout: 10))
        XCTAssertTrue((leftGoal.value as? String)?.contains("执行中") ?? false)
        XCTAssertTrue(leftQueue.exists && rightQueue.exists)
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("The right goal clears while both queues and drafts remain", app: app)
    }

    func testIPadParallelApprovalsKeepTheirSessionAndSelection() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_APPROVAL_FIXTURE"] == "1", UIDevice.current.userInterfaceIdiom == .pad else {
            throw XCTSkip("Requires pending approvals on enrolled iPad sessions")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["并排查看会话"].tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()
        let rightDraft = app.textFields["右侧会话草稿"].firstMatch
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["左栏命令一"].waitForExistence(timeout: 8))
        capture("Approval panel before changing the request in either pane", app: app)
        XCTAssertTrue(app.staticTexts["右栏命令一"].waitForExistence(timeout: 6), "The right session must display its own request panel")

        let rightStart = rightDraft.frame.minX - 20
        let next = app.buttons.matching(identifier: "下一条请求").allElementsBoundByIndex
        try XCTUnwrap(next.first { $0.frame.midX < rightStart }).tap()
        let leftSecond = app.staticTexts["左栏命令二"].firstMatch
        XCTAssertTrue(leftSecond.waitForExistence(timeout: 5))
        try XCTUnwrap(next.first { $0.frame.midX > rightStart }).tap()
        let rightSecond = app.staticTexts["右栏命令二"].firstMatch
        XCTAssertTrue(rightSecond.waitForExistence(timeout: 5))
        XCTAssertTrue(leftSecond.exists, "Selecting the right request must keep the left panel on its own selection")
        capture("Each pane retains its own request selection", app: app)

        app.buttons["右栏请求二"].firstMatch.tap()
        XCTAssertTrue(rightSecond.waitForNonExistence(timeout: 5))
        let links = app.buttons.matching(identifier: "查看请求来源").allElementsBoundByIndex
        let source = try XCTUnwrap(links.first { ($0.value as? String) == "右栏请求二" && $0.frame.midX > rightStart })
        source.tap()
        XCTAssertTrue(rightSecond.waitForExistence(timeout: 5))
        XCTAssertTrue(leftSecond.exists)
        app.buttons["批准右栏二"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["右栏请求二 · 已处理"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(leftSecond.exists)
        XCTAssertEqual(rightDraft.value as? String, "Second")
        XCTAssertEqual(app.textFields["消息"].firstMatch.value as? String, "First Worker draft")
        capture("The right request was answered with both drafts untouched", app: app)
    }

    func testPendingHistoryCanBeCancelledOnItsOwningWorker() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_HISTORY_CANCEL_FIXTURE"] == "1" else {
            throw XCTSkip("Requires isolated Worker history receipts")
        }
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = launch("")
        func open(_ title: String, leavingConversation: Bool = true) {
            if leavingConversation && !tablet {
                app.navigationBars.buttons["BackButton"].firstMatch.tap()
            }
            let row = app.buttons[title].firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 12))
            row.tap()
        }
        let cancel = app.buttons["取消本次操作"].firstMatch
        let forkQuery = app.buttons["查询分叉回执"].firstMatch
        let draft = app.textFields["消息"].firstMatch
        open("待核对的分叉", leavingConversation: false)
        XCTAssertTrue(forkQuery.waitForExistence(timeout: 10))
        forkQuery.tap()
        XCTAssertTrue(app.buttons["继续完成分叉"].waitForExistence(timeout: 8))
        capture("Prepared fork in its source conversation", app: app)
        XCTAssertTrue(cancel.exists, "A prepared fork needs a cancellation action")
        cancel.tap()
        XCTAssertTrue(forkQuery.waitForNonExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["from alpha: 分叉原文"].exists)
        XCTAssertEqual(draft.value as? String, "分叉的普通草稿")
        XCTAssertFalse(app.navigationBars["分叉会话"].exists)

        if tablet {
            app.buttons["会话活动"].firstMatch.tap()
            app.buttons["并排查看会话"].firstMatch.tap()
            let choice = app.buttons.matching(identifier: "待核对的历史修改").element(boundBy: 1)
            XCTAssertTrue(choice.waitForExistence(timeout: 8))
            choice.tap()
        } else {
            open("待核对的历史修改")
        }
        let restartDraft = tablet ? app.textFields["右侧会话草稿"].firstMatch : draft
        let edit = app.textFields["编辑已发送消息"].firstMatch
        let restartQuery = app.buttons["核对重开回执"].firstMatch
        XCTAssertTrue(restartQuery.waitForExistence(timeout: 10))
        XCTAssertTrue(edit.waitForExistence(timeout: 8))
        XCTAssertEqual(edit.value as? String, "保留这份历史改稿")
        capture("Pending restart and its saved edit", app: app)
        cancel.tap()
        XCTAssertTrue(app.buttons["确认修改并重开"].waitForExistence(timeout: 8))
        XCTAssertFalse(restartQuery.exists)
        XCTAssertEqual(edit.value as? String, "保留这份历史改稿")
        XCTAssertEqual(restartDraft.value as? String, "重开的普通草稿")
        XCTAssertTrue(app.staticTexts["from alpha: 重开原文"].exists)
        capture("Cancellation unlocks the edit without replacing either draft", app: app)
        if tablet {
            XCTAssertEqual(draft.value as? String, "分叉的普通草稿")
            XCTAssertTrue(app.staticTexts["from alpha: 分叉原文"].exists)
            app.buttons["关闭并排会话"].firstMatch.tap()
        }

        open("另一端已完成的分叉")
        XCTAssertTrue(cancel.waitForExistence(timeout: 8))
        cancel.tap()
        let completed = app.descendants(matching: .any)["操作已完成"].firstMatch
        XCTAssertTrue(completed.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.staticTexts["from alpha: 已完成分叉原文"].exists)
        XCTAssertEqual(draft.value as? String, "已完成分叉的普通草稿")
        capture("The completed receipt stays on the source conversation", app: app)
    }

    func testIPadParallelHistoryActionsStayInTheirPane() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_HISTORY_FIXTURE"] == "1", UIDevice.current.userInterfaceIdiom == .pad else {
            throw XCTSkip("Requires an iPad and enrolled history fixtures")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["并排查看会话"].tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()
        let rightDraft = app.textFields["右侧会话草稿"].firstMatch
        let leftDraft = app.textFields["消息"].firstMatch
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 8))
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        let rightPaneStart = rightDraft.frame.minX - 20

        func rightAction(_ title: String) -> XCUIElement? {
            let actions = app.buttons.matching(identifier: title)
            let available = NSPredicate { _, _ in
                actions.allElementsBoundByIndex.contains { $0.isHittable && $0.frame.midX > rightPaneStart }
            }
            let wait = XCTNSPredicateExpectation(predicate: available, object: nil)
            guard XCTWaiter.wait(for: [wait], timeout: 7) == .completed else { return nil }
            return actions.allElementsBoundByIndex.first { $0.isHittable && $0.frame.midX > rightPaneStart }
        }
        capture("Right conversation before history controls", app: app)
        let edit = try XCTUnwrap(rightAction("编辑消息"), "The right pane needs a history edit control")
        XCTAssertTrue(edit.isEnabled)
        edit.tap()
        let field = app.textFields["编辑已发送消息"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 8))
        XCTAssertEqual(field.value as? String, "Right source")
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("History edit appears inside the right conversation", app: app)
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(field.waitForNonExistence(timeout: 6))
        edit.tap()
        XCTAssertTrue(field.waitForExistence(timeout: 6))
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.45)).tap()
        app.typeText(" corrected")
        XCTAssertEqual(field.value as? String, "Right source corrected")
        app.buttons["确认修改并重开"].tap()
        if environment["SHIOJI_PARALLEL_HISTORY_CONTINUE"] == "1" {
            let receipt = app.buttons["核对重开回执"].firstMatch
            XCTAssertTrue(receipt.waitForExistence(timeout: 10))
            XCTAssertTrue(app.staticTexts["fixture restart prepared"].waitForExistence(timeout: 10), app.debugDescription)
            XCTAssertFalse(app.buttons["继续完成重开"].exists)
            receipt.tap()
            let resume = app.buttons["继续完成重开"].firstMatch
            XCTAssertTrue(resume.waitForExistence(timeout: 8), "The confirmed prepared restart should be resumable")
            resume.tap()
        }
        let revised = app.staticTexts["from alpha: Right source corrected"].firstMatch
        XCTAssertTrue(revised.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts["from alpha: Left source"].exists)
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("Only the right session restarted", app: app)

        let fork = try XCTUnwrap(rightAction("从这里分叉"))
        XCTAssertTrue(fork.isEnabled)
        fork.tap()
        XCTAssertFalse(app.navigationBars["分叉会话"].exists)
        if environment["SHIOJI_PARALLEL_HISTORY_RETRY"] == "1" {
            let rejected = app.staticTexts["fixture fork rejected"].firstMatch
            XCTAssertTrue(rejected.waitForExistence(timeout: 10), app.debugDescription)
            app.buttons["查询分叉回执"].firstMatch.tap()
            let retry = try XCTUnwrap(rightAction("从这里分叉"), "A failed receipt must release the original fork action")
            XCTAssertTrue(retry.isEnabled)
            retry.tap()
        }
        if environment["SHIOJI_PARALLEL_HISTORY_CONTINUE"] == "1" {
            XCTAssertTrue(app.staticTexts["fixture fork prepared"].waitForExistence(timeout: 10), app.debugDescription)
            XCTAssertFalse(app.buttons["继续完成分叉"].exists)
            app.buttons["查询分叉回执"].firstMatch.tap()
            let resume = app.buttons["继续完成分叉"].firstMatch
            XCTAssertTrue(resume.waitForExistence(timeout: 8), "The confirmed prepared fork should be resumable")
            resume.tap()
        }
        let title = app.staticTexts["右栏设备会话 · 分叉"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertGreaterThan(title.frame.midX, app.frame.midX)
        XCTAssertTrue(revised.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["from alpha: Left source"].exists)
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertNotEqual(rightDraft.value as? String, "Second")
        capture("Fork opens in the right pane beside the unchanged left conversation", app: app)
    }

    func testPhoneActivityReviewReturnsCommentToConversationDraft() throws {
        guard ProcessInfo.processInfo.environment["SHIOJI_PARALLEL_REVIEW_FIXTURE"] == "1",
              UIDevice.current.userInterfaceIdiom == .phone else {
            throw XCTSkip("Requires an iPhone and an enrolled Git review fixture")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 12))
        session.tap()
        let draft = app.textFields["消息"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 8))
        XCTAssertEqual(draft.value as? String, "First Worker draft")
        draft.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 2, dy: 10)).tap()
        app.buttons["会话活动"].firstMatch.tap()
        let review = app.buttons["工作区改动"].firstMatch
        XCTAssertTrue(review.waitForExistence(timeout: 6))
        review.tap()
        let file = app.buttons["left-review.go"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 12), app.debugDescription)
        file.tap()
        let diff = app.descendants(matching: .any).matching(identifier: "文件差异：left-review.go").firstMatch
        XCTAssertTrue(diff.waitForExistence(timeout: 8))
        diff.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 36, dy: 61)).tap()
        let comment = app.buttons["评论选区"].firstMatch
        XCTAssertTrue(comment.isEnabled)
        comment.tap()
        let editor = app.descendants(matching: .any).matching(identifier: "代码选区评论").firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 6))
        editor.tap()
        app.typeText("Review this change")
        app.buttons["加入当前草稿"].tap()
        XCTAssertTrue(app.staticTexts["已加入当前会话草稿"].waitForExistence(timeout: 10), app.debugDescription)
        for _ in 0..<3 {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(draft.waitForExistence(timeout: 8))
        XCTAssertEqual(draft.value as? String, "\u{FFFC}First Worker draft")
        draft.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 32, dy: 11)).tap()
        XCTAssertTrue(app.staticTexts["Review this change"].waitForExistence(timeout: 8), app.debugDescription)
    }

    func testIPadParallelCodeReviewAddsCommentToItsOwnDraft() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_PARALLEL_REVIEW_FIXTURE"] == "1", UIDevice.current.userInterfaceIdiom == .pad else {
            throw XCTSkip("Requires an iPad and enrolled Git review fixtures")
        }
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        app.buttons["会话活动"].firstMatch.tap()
        app.buttons["并排查看会话"].tap()
        let right = app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1)
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        right.tap()
        let leftDraft = app.textFields["消息"].firstMatch
        let rightDraft = app.textFields["右侧会话草稿"].firstMatch
        XCTAssertTrue(rightDraft.waitForExistence(timeout: 8))
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertEqual(rightDraft.value as? String, "Second")
        capture("Parallel session before reviewing changes", app: app)
        let entry = app.buttons["右栏文件改动"].firstMatch
        XCTAssertTrue(entry.waitForExistence(timeout: 6), "The right pane needs its own file changes entry")
        rightDraft.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 2, dy: 10)).tap()
        entry.tap()
        let file = app.buttons["right-review.go"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 14), app.debugDescription)
        if environment["SHIOJI_PARALLEL_REVIEW_CROSS_WORKER"] == "1" {
            XCTAssertFalse(app.buttons["left-review.go"].exists)
        }
        capture("The right session lists its own changes", app: app)
        file.tap()
        let diff = app.descendants(matching: .any).matching(identifier: "文件差异：right-review.go").firstMatch
        XCTAssertTrue(diff.waitForExistence(timeout: 10), app.debugDescription)
        let comment = app.buttons["评论选区"].firstMatch
        XCTAssertTrue(comment.exists)
        diff.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 36, dy: 61)).tap()
        XCTAssertTrue(comment.isEnabled, app.debugDescription)
        comment.tap()
        let editor = app.descendants(matching: .any).matching(identifier: "代码选区评论").firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 6))
        editor.tap()
        app.typeText("Check the right change")
        app.buttons["加入当前草稿"].tap()
        XCTAssertTrue(app.staticTexts["已加入当前会话草稿"].waitForExistence(timeout: 10), app.debugDescription)
        capture("The selected change is added to the owning draft", app: app)
        app.buttons["关闭改动"].tap()
        XCTAssertTrue(leftDraft.waitForExistence(timeout: 8))
        XCTAssertEqual(leftDraft.value as? String, "First Worker draft")
        XCTAssertTrue((rightDraft.value as? String ?? "").hasSuffix("Second"))
        capture("Only the right draft gained an inline comment", app: app)
        rightDraft.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 32, dy: 11)).tap()
        XCTAssertTrue(app.staticTexts["Check the right change"].waitForExistence(timeout: 8), app.debugDescription)
    }

    func testDraftDiffCommentCanBeEditedInPlaceBeforeSending() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_DIFF_DRAFT_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the enrolled Worker with a retained draft comment")
        }
        continueAfterFailure = false
        let rightPane = environment["SHIOJI_LINK_RIGHT_PANE"] == "1"
        XCUIDevice.shared.orientation = rightPane ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        if rightPane {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
            app.buttons["并排查看会话"].tap()
            app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1).tap()
            XCTAssertTrue(app.buttons["关闭并排会话"].waitForExistence(timeout: 8))
        }
        let composer = app.textFields.matching(NSPredicate(format: "label == %@ AND value CONTAINS %@",
                                                            rightPane ? "右侧会话草稿" : "消息", "保留这段草稿正文")).firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 8), app.debugDescription)
        let originalDraft = composer.value as? String
        if rightPane {
            capture("The parallel composer shows both draft lines", app: app)
        }
        let chip = composer.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 32, dy: 11))
        chip.tap()
        let edit = app.buttons["编辑评论"].firstMatch
        let opens = edit.waitForExistence(timeout: 8)
        capture(opens ? "Draft reference opens the saved comparison" : "Draft reference cannot be opened", app: app)
        XCTAssertTrue(opens, "Tapping the draft comment should open its original comparison with an edit action")

        let field = app.descendants(matching: .any).matching(identifier: "评论内容").firstMatch
        edit.tap()
        XCTAssertTrue(field.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertEqual(field.value as? String, "请核对旋转前后的选区")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.90, dy: 0.22)).tap()
        app.typeText(" discarded")
        XCTAssertTrue((field.value as? String ?? "").contains("discarded"), app.debugDescription)
        app.buttons["取消编辑评论"].tap()
        XCTAssertTrue(edit.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["请核对旋转前后的选区"].exists)

        edit.tap()
        XCTAssertTrue(field.waitForExistence(timeout: 6))
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.90, dy: 0.22)).tap()
        app.typeText(" reviewed")
        capture("Only the draft comment is being edited", app: app)
        app.buttons["保存评论"].tap()
        let updated = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "reviewed")).firstMatch
        XCTAssertTrue(updated.waitForExistence(timeout: 14), app.debugDescription)
        XCTAssertTrue(app.buttons["评论选区"].exists, "The same comparison must retain its saved ranges")
        capture("Edited draft comment keeps the original ranges", app: app)
        if rightPane {
            app.buttons["关闭代码评论"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, originalDraft)
        chip.tap()
        XCTAssertTrue(updated.waitForExistence(timeout: 8), app.debugDescription)
        if rightPane {
            app.buttons["关闭代码评论"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }

        let historical = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "预览引用：rotation.go:")).firstMatch
        XCTAssertTrue(historical.waitForExistence(timeout: 8))
        historical.tap()
        XCTAssertTrue(app.staticTexts["请核对旋转前后的选区"].waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(edit.exists)
    }

    func testSentDiffCommentRestoresTheOriginalComparisonAndSelection() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["SHIOJI_DIFF_COMMENT_FIXTURE"] == "1" else {
            throw XCTSkip("Requires the enrolled Worker with a retained Git comparison")
        }
        continueAfterFailure = false
        let rightPane = environment["SHIOJI_LINK_RIGHT_PANE"] == "1"
        XCUIDevice.shared.orientation = rightPane ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        if let control = environment["SHIOJI_TAILNET_FIXTURE_URL"] {
            app.launchEnvironment["TS_CONTROL_URL"] = control
        }
        app.launch()
        let left = app.buttons["左栏设备会话"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 12))
        if let peer = environment["SHIOJI_TAILNET_FIXTURE_PEER"] {
            joinFixtureTailnet(peer, app: app)
        }
        left.tap()
        if rightPane {
            let activity = app.buttons["会话活动"].firstMatch
            XCTAssertTrue(activity.waitForExistence(timeout: 8))
            activity.tap()
            app.buttons["并排查看会话"].tap()
            app.buttons.matching(identifier: "右栏设备会话").element(boundBy: 1).tap()
            XCTAssertTrue(app.buttons["关闭并排会话"].waitForExistence(timeout: 8))
        }
        let reference = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "预览引用：rotation.go:")).firstMatch
        XCTAssertTrue(reference.waitForExistence(timeout: 12), app.debugDescription)
        reference.tap()
        let comment = app.staticTexts["请核对旋转前后的选区"]
        let opened = comment.waitForExistence(timeout: 10)
        capture(opened ? "Comment opens at the retained baseline range" : "Sent comment opens as a generic attachment", app: app)
        XCTAssertTrue(opened, "The reference should display the saved comment and its original comparison")
        let ranges = app.buttons["评论选区"].firstMatch
        XCTAssertTrue(ranges.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue((ranges.value as? String ?? "").contains("基准 · 第 68 行"), app.debugDescription)
        let diff = app.descendants(matching: .any).matching(identifier: "历史差异：rotation.go").firstMatch
        XCTAssertTrue(diff.exists)
        ranges.tap()
        let target = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "目标 · 第 94 行")).firstMatch
        XCTAssertTrue(target.waitForExistence(timeout: 6), app.debugDescription)
        target.tap()
        XCTAssertTrue((ranges.value as? String ?? "").contains("目标 · 第 94 行"))
        capture("The second selection reveals the retained target range", app: app)
        ranges.tap()
        let unchanged = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "基准 · 第 28 行")).firstMatch
        XCTAssertTrue(unchanged.waitForExistence(timeout: 6), app.debugDescription)
        unchanged.tap()
        XCTAssertTrue((ranges.value as? String ?? "").contains("基准 · 第 28 行"))
        capture("An unchanged baseline fragment is highlighted in the unified diff", app: app)
        ranges.tap()
        let copy = app.buttons["复制当前选区"].firstMatch
        XCTAssertTrue(copy.waitForExistence(timeout: 6), app.debugDescription)
        copy.tap()
        XCTAssertTrue(copy.waitForNonExistence(timeout: 5))
        if rightPane {
            app.buttons["关闭代码评论"].tap()
        } else {
            app.navigationBars.buttons["BackButton"].firstMatch.tap()
        }
        XCTAssertTrue(reference.waitForExistence(timeout: 6))
    }

    func testConversationFileLinksOpenOwningPreview() {
        let app = launch("--preview-board-transcript-links")
        let source = app.links["查看连接源码"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 8))
        source.tap()
        let position = app.staticTexts["第 17–19 行"]
        XCTAssertTrue(position.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(app.staticTexts["connection.go"].exists || app.navigationBars["connection.go"].exists)
        capture("Linked source", app: app)
        if app.buttons["关闭文件预览"].exists {
            XCTAssertTrue(source.exists)
            app.buttons["关闭文件预览"].tap()
        } else {
            app.navigationBars["connection.go"].buttons.firstMatch.tap()
        }
        let manual = app.links["查看运维手册"].firstMatch
        XCTAssertTrue(manual.waitForExistence(timeout: 5))
        manual.tap()
        let child = app.links["阅读故障恢复"].firstMatch
        XCTAssertTrue(child.waitForExistence(timeout: 6))
        child.tap()
        let recovery = app.staticTexts["重新连接执行服务，然后查询同一轮次的回执。"]
        XCTAssertTrue(recovery.waitForExistence(timeout: 6), app.debugDescription)
        capture("Nested Markdown file", app: app)
        if app.buttons["返回上一个文件"].exists {
            app.buttons["返回上一个文件"].tap()
            XCTAssertTrue(child.waitForExistence(timeout: 5))
        }
    }

    func testSavedDeviceRouteRemovalKeepsSessionAndSurvivingPath() throws {
        let env = ProcessInfo.processInfo.environment
        guard let activeAddress = env["SHIOJI_ROUTE_ACTIVE"], let backupAddress = env["SHIOJI_ROUTE_BACKUP"],
              let unavailableAddress = env["SHIOJI_ROUTE_UNAVAILABLE"] else {
            throw XCTSkip("Requires one isolated Worker with two verified paths")
        }
        continueAfterFailure = false
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        XCUIDevice.shared.orientation = tablet ? .landscapeLeft : .portrait
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        let session = app.buttons["连接路径验收会话"].firstMatch
        let composer = app.textFields["消息"].firstMatch
        let draft = "Keep this draft across route removal."
        func openPaths() {
            let devices = app.buttons["设备"].firstMatch
            XCTAssertTrue(devices.waitForExistence(timeout: 12))
            devices.tap()
            let host = app.buttons["路径验收设备"].firstMatch
            XCTAssertTrue(host.waitForExistence(timeout: 8))
            host.tap()
            let paths = app.buttons["连接路径"].firstMatch
            XCTAssertTrue(paths.waitForExistence(timeout: 8))
            paths.tap()
        }
        func route(_ address: String, _ phase: String = "") -> XCUIElement {
            if phase.isEmpty {
                return app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "直接 SSH，" + address + "，")).firstMatch
            }
            return app.buttons["直接 SSH，" + address + "，" + phase].firstMatch
        }
        let remove = app.descendants(matching: .any)["移除此连接"].firstMatch
        func confirmRemoval() {
            let confirm = app.alerts.firstMatch
            XCTAssertTrue(confirm.waitForExistence(timeout: 6))
            confirm.buttons["移除连接"].tap()
            XCTAssertTrue(confirm.waitForNonExistence(timeout: 6))
        }
        app.launch()
        XCTAssertTrue(session.waitForExistence(timeout: 12))
        session.tap()
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, draft)
        if !tablet { app.navigationBars.buttons["BackButton"].firstMatch.tap() }
        openPaths()
        XCTAssertTrue(route(activeAddress, "当前").waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(route(backupAddress, "可用").waitForExistence(timeout: 12), app.debugDescription)
        let unavailable = route(unavailableAddress, "不可用")
        XCTAssertTrue(unavailable.waitForExistence(timeout: 12), app.debugDescription)
        capture("Connection paths with a verified backup and one unavailable path", app: app)
        unavailable.press(forDuration: 0.8)
        guard remove.waitForExistence(timeout: 5) else {
            capture("Path long press before removal was available", app: app)
            return XCTFail("Connection paths should offer native removal")
        }
        remove.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 6))
        alert.buttons["取消"].tap()
        XCTAssertTrue(unavailable.waitForExistence(timeout: 6))
        unavailable.press(forDuration: 0.8)
        XCTAssertTrue(remove.waitForExistence(timeout: 5))
        remove.tap()
        confirmRemoval()
        XCTAssertTrue(route(unavailableAddress).waitForNonExistence(timeout: 8))
        let active = route(activeAddress, "当前")
        XCTAssertTrue(active.exists)
        active.press(forDuration: 0.8)
        XCTAssertTrue(remove.waitForExistence(timeout: 5))
        remove.tap()
        confirmRemoval()
        let remaining = route(backupAddress, "当前")
        XCTAssertTrue(remaining.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertFalse(route(activeAddress).exists)
        capture("The original Worker remains connected through its backup route", app: app)
        remaining.press(forDuration: 0.8)
        XCTAssertTrue(remove.waitForExistence(timeout: 5))
        XCTAssertFalse(remove.isEnabled)
        capture("The last route cannot be removed", app: app)

        app.terminate()
        app.launch()
        openPaths()
        XCTAssertTrue(remaining.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertFalse(route(activeAddress).exists)
        XCTAssertFalse(route(unavailableAddress).exists)
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        app.navigationBars.buttons["BackButton"].firstMatch.tap()
        app.buttons["会话"].firstMatch.tap()
        XCTAssertTrue(session.waitForExistence(timeout: 8))
        session.tap()
        XCTAssertTrue(composer.waitForExistence(timeout: 8))
        XCTAssertEqual(composer.value as? String, draft)
    }

    func testSavedDeviceRoutesRetainOwnCredentials() {
        let app = launch("--preview-board-device-paths")
        let paths = app.buttons["连接路径"]
        XCTAssertTrue(paths.waitForExistence(timeout: 8))
        paths.tap()
        XCTAssertTrue(app.buttons["直接 SSH，mac-mini.local:22，当前"].waitForExistence(timeout: 6))
        let tailnet = app.buttons["Tailnet SSH，100.64.7.18:22，可用"]
        XCTAssertTrue(tailnet.exists)
        tailnet.tap()
        XCTAssertTrue(app.staticTexts["100.64.7.18:22 · remote"].waitForExistence(timeout: 6), app.debugDescription)
    }

    func testTailnetManagementShowsPeerDetailsAndRefresh() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-tailnet-peers")
        let overview = app.buttons["Tailnet"]
        XCTAssertTrue(overview.waitForExistence(timeout: 10))
        overview.tap()
        let online = app.buttons["Mac mini"]
        XCTAssertTrue(online.waitForExistence(timeout: 8))
        capture("Tailnet management before an explicit refresh or inspection", app: app)
        let refresh = app.buttons["刷新网络设备"]
        guard refresh.waitForExistence(timeout: 4) else { return XCTFail("Missing refresh action on the Tailnet management page") }
        let copyLocal = app.buttons["复制本机 Tailnet 地址"]
        XCTAssertTrue(copyLocal.exists)
        let copied = NSPredicate(format: "value == %@", "已复制")
        if !tablet {
            copyLocal.tap()
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: copied, object: copyLocal)], timeout: 3), .completed)
        }
        refresh.tap()
        let idle = NSPredicate { _, _ in refresh.isEnabled }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: idle, object: refresh)], timeout: 6), .completed)
        capture("Tailnet management after refresh", app: app)
        online.tap()
        let details = app.navigationBars["Tailnet 设备"]
        XCTAssertTrue(details.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["DERP 中继 · Taipei (tpe)"].exists)
        XCTAssertTrue(app.staticTexts["32 ms"].exists)
        XCTAssertTrue(app.staticTexts["macOS"].exists)
        let copyPeer = app.buttons["复制 Tailnet 设备地址"]
        XCTAssertTrue(copyPeer.exists)
        if !tablet {
            copyPeer.tap()
            XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: copied, object: copyPeer)], timeout: 3), .completed)
        }
        XCTAssertTrue(app.buttons["添加设备"].exists)
        capture("Online peer details show the actual relay and latency", app: app)
        details.buttons.firstMatch.tap()
        app.buttons["旧 Mac"].tap()
        XCTAssertTrue(details.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["旧 Mac · 离线"].exists)
        XCTAssertTrue(app.staticTexts["尚未确认"].exists)
        XCTAssertFalse(app.staticTexts["0 ms"].exists)
        XCTAssertFalse(app.buttons["添加设备"].exists)
        capture("Offline peer details retain its address and unknown path", app: app)
    }

    func testTailnetPeerStatusAndPairing() {
        let app = launch("--preview-board-tailnet-peers")
        let tailnet = app.buttons["Tailnet"]
        XCTAssertTrue(tailnet.waitForExistence(timeout: 8))
        tailnet.tap()
        let peer = app.buttons["Mac mini"]
        XCTAssertTrue(peer.waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["旧 Mac"].exists)
        XCTAssertTrue(app.buttons["退出当前 Tailnet"].exists)
        capture("Tailnet peers", app: app)
        peer.tap()
        XCTAssertTrue(app.navigationBars["Tailnet 设备"].waitForExistence(timeout: 6))
        app.buttons["添加设备"].tap()
        XCTAssertTrue(app.navigationBars["添加设备"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["100.64.0.20"].exists)
        XCTAssertTrue(app.buttons["连接方式"].exists)
    }

    func testTailnetSelectorReturnsToTheOriginalSSHDraft() {
        let app = launch("--preview-board-tailnet-pair-draft")
        let pair = app.navigationBars["添加设备"]
        XCTAssertTrue(pair.waitForExistence(timeout: 8))
        let user = app.textFields["用户名"]
        let port = app.textFields["端口"]
        XCTAssertEqual(user.value as? String, "remote-operator")
        XCTAssertEqual(port.value as? String, "2222")
        app.buttons["连接方式"].tap()
        app.descendants(matching: .any)["通过 Tailnet 连接"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["选择 Tailnet 设备"].waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["旧 Mac"].exists)
        app.buttons["Mac mini"].tap()
        XCTAssertTrue(pair.waitForExistence(timeout: 6))
        XCTAssertTrue(app.staticTexts["100.64.0.20"].exists)
        XCTAssertEqual(user.value as? String, "remote-operator")
        XCTAssertEqual(port.value as? String, "2222")
        capture("Previously entered SSH settings survive Tailnet selection", app: app)
        XCTAssertTrue(app.buttons["检查连接"].isEnabled)
        app.buttons["连接方式"].tap()
        app.descendants(matching: .any)["直接 SSH"].firstMatch.tap()
        XCTAssertEqual(app.textFields["主机"].value as? String, "pair.test")
        pair.buttons.firstMatch.tap()
        XCTAssertTrue(pair.waitForNonExistence(timeout: 6))
        XCTAssertFalse(app.navigationBars["选择 Tailnet 设备"].exists)
    }

    func testTailnetPeerShowsFullObservedRelayAndUnknownOfflineLatency() {
        let app = launch("--preview-board-tailnet-peers")
        let network = app.buttons["Tailnet"]
        XCTAssertTrue(network.waitForExistence(timeout: 8))
        network.tap()
        XCTAssertTrue(app.buttons["Mac mini"].waitForExistence(timeout: 8))
        capture("Tailnet relay metadata before alignment", app: app)
        let relay = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", "DERP 中继 · Taipei (tpe)", "DERP 中继 · Taipei (tpe)")).firstMatch
        XCTAssertTrue(relay.waitForExistence(timeout: 4), "Tailnet peer is missing its observed relay name and region code")
        let offline = app.buttons["旧 Mac"]
        let value = offline.value as? String ?? ""
        XCTAssertTrue(value.contains("离线"))
        XCTAssertFalse(value.contains("0 ms"))
    }

    func testTailnetPausedDeviceLabelsItsLastRouteObservation() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-tailnet-route-last")
        let last = app.staticTexts["上次 32 ms"]
        XCTAssertTrue(last.waitForExistence(timeout: 8))
        XCTAssertTrue(app.staticTexts["上次 · DERP 中继 · Taipei (tpe)"].exists)
        XCTAssertTrue(app.staticTexts["离线"].exists)
        XCTAssertFalse(app.staticTexts["32 ms"].exists)
        capture("Paused Tailnet route shows its last observation", app: app)
    }

    func testTailnetEmbeddedNodeReadsHostFingerprint() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let control = environment["SHIOJI_TAILNET_FIXTURE_URL"],
              let peerName = environment["SHIOJI_TAILNET_FIXTURE_PEER"],
              let fingerprint = environment["SHIOJI_TAILNET_FIXTURE_FINGERPRINT"] else {
            throw XCTSkip("Requires a fresh simulator and the isolated Tailnet fixture")
        }
        continueAfterFailure = false
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launchEnvironment["TS_CONTROL_URL"] = control
        app.launch()
        let devices = app.tabBars.buttons["设备"]
        XCTAssertTrue(devices.waitForExistence(timeout: 12))
        devices.tap()
        let tailnet = app.buttons["Tailnet"]
        XCTAssertTrue(tailnet.waitForExistence(timeout: 8))
        tailnet.tap()
        let join = app.buttons["在浏览器授权"]
        XCTAssertTrue(join.waitForExistence(timeout: 8))
        join.tap()
        let peer = app.buttons[peerName]
        XCTAssertTrue(peer.waitForExistence(timeout: 25), app.debugDescription)
        let refresh = app.buttons["刷新网络设备"]
        XCTAssertTrue(refresh.isEnabled)
        refresh.tap()
        let ready = NSPredicate { _, _ in refresh.isEnabled }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: ready, object: refresh)], timeout: 12), .completed)
        capture("Embedded simulator node discovered an isolated peer", app: app)
        peer.tap()
        XCTAssertTrue(app.navigationBars["Tailnet 设备"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["复制 Tailnet 设备地址"].exists)
        XCTAssertTrue(app.staticTexts["往返延迟"].exists)
        capture("Live Tailnet peer detail after refresh", app: app)
        app.buttons["添加设备"].tap()
        let username = app.textFields["用户名"]
        XCTAssertTrue(username.waitForExistence(timeout: 6))
        XCTAssertTrue(app.buttons["检查连接"].isEnabled)
        capture("Pairing opened for the discovered peer", app: app)
        username.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 6))
        app.typeText("shioji ")
        XCTAssertEqual((username.value as? String)?.trimmingCharacters(in: .whitespaces), "shioji")
        app.typeText("\n")
        XCTAssertTrue(app.staticTexts[fingerprint].waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(app.buttons["信任主机，继续配置 SSH"].isEnabled)
        XCTAssertFalse(app.textFields["设备上核实的指纹"].exists)
        capture("Fingerprint read from the actual Tailnet peer", app: app)
    }

    func testConversationLoadsOlderHistoryInline() {
        let app = launch("--preview-board-transcript-history")
        let previous = app.staticTexts["这是前一轮的回复。"]
        let last = app.staticTexts["稍后的消息 25"]
        XCTAssertTrue(last.waitForExistence(timeout: 8))
        XCTAssertFalse(previous.exists, "Opening at the newest window must not prefetch history before a scroll")
        let top = app.coordinate(withNormalizedOffset: CGVector(dx: 0.55, dy: 0.22))
        let bottom = app.coordinate(withNormalizedOffset: CGVector(dx: 0.55, dy: 0.71))
        for _ in 0..<7 where !previous.isHittable {
            top.press(forDuration: 0.05, thenDragTo: bottom, withVelocity: .slow, thenHoldForDuration: 0.35)
        }
        XCTAssertTrue(previous.waitForExistence(timeout: 5), "An upward scroll must fetch earlier history without a button")
        XCTAssertFalse(app.buttons["加载更早消息"].exists)
        XCTAssertFalse(app.buttons["正在读取历史…"].exists)
        capture("Earlier conversation history", app: app)
    }

    func testHistoryCanLoadFiveRounds() {
        let app = launch("--preview-board-input-prefs")
        let rounds = app.buttons["每次加载轮次"].firstMatch
        XCTAssertTrue(rounds.waitForExistence(timeout: 8))
        rounds.tap()
        let five = app.descendants(matching: .any)["5"].firstMatch
        XCTAssertTrue(five.waitForExistence(timeout: 5))
        five.tap()
        XCTAssertEqual(rounds.value as? String, "5")
    }

    func testScrollingPrefetchesEarlierRoundsFromOwningWorker() throws {
        guard let title = ProcessInfo.processInfo.environment["SHIOJI_HISTORY_WORKER_SESSION"] else {
            throw XCTSkip("Requires an isolated Worker with multiple history windows")
        }
        continueAfterFailure = false
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = XCUIApplication(bundleIdentifier: "ai.shioji.mobile")
        app.launch()
        let session = app.buttons[title].firstMatch
        XCTAssertTrue(session.waitForExistence(timeout: 16), app.debugDescription)
        session.tap()
        let newest = app.staticTexts["核对第 48 项。"].firstMatch
        XCTAssertTrue(newest.waitForExistence(timeout: 15))
        let previousPage = app.staticTexts["核对第 28 项。"].firstMatch
        XCTAssertFalse(previousPage.exists)
        let top = app.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.23))
        let bottom = app.coordinate(withNormalizedOffset: CGVector(dx: 0.65, dy: 0.74))
        for _ in 0..<12 where !previousPage.isHittable {
            top.press(forDuration: 0.05, thenDragTo: bottom, withVelocity: .default, thenHoldForDuration: 0.18)
        }
        XCTAssertTrue(previousPage.waitForExistence(timeout: 8), "Scrolling must load a round outside the initial worker window")
        XCTAssertFalse(app.buttons["加载更早消息"].exists)
        capture("Earlier rounds fetched from the same Worker", app: app)
        let backToLatest = app.buttons["回到最新消息"].firstMatch
        XCTAssertTrue(backToLatest.waitForExistence(timeout: 5))
        backToLatest.tap()
        XCTAssertTrue(newest.waitForExistence(timeout: 8))
        XCTAssertTrue(backToLatest.waitForNonExistence(timeout: 5))
    }

    func testPhonePairFieldsShareLabelBaseline() {
        let app = launch("--preview-board-pair")
        let host = app.textFields["主机"].firstMatch
        let label = app.staticTexts["主机"].firstMatch
        XCTAssertTrue(host.waitForExistence(timeout: 8))
        XCTAssertTrue(label.exists)
        XCTAssertEqual(host.frame.midY, label.frame.midY, accuracy: 1)
        XCTAssertEqual(host.value as? String, "mac-mini.local")
        capture("Pair form with inline values", app: app)
    }

    @available(iOS 27.0, *)
    func testPhoneProminentComposeOpensDraft() {
        let app = launch("--preview")
        let compose = app.tabBars.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertEqual(compose.frame.midY, app.tabBars.buttons["会话"].firstMatch.frame.midY, accuracy: 2)
        capture("System tab bar and prominent compose", app: app)
        compose.tap()
        XCTAssertTrue(app.navigationBars["新建会话"].waitForExistence(timeout: 5))
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(compose.waitForExistence(timeout: 5))
    }

    func testPhoneQueueAndBackGesture() {
        let app = launch("--preview-session")
        let rowActions = app.buttons.matching(identifier: "待发消息操作")
        XCTAssertTrue(rowActions.firstMatch.waitForExistence(timeout: 8), app.debugDescription)
        let back = app.navigationBars.buttons["BackButton"]
        let title = app.navigationBars.staticTexts["conversation-title"].firstMatch
        XCTAssertTrue(title.exists)
        XCTAssertGreaterThanOrEqual(title.frame.minX - back.frame.maxX, 16)
        XCTAssertTrue(rowActions.element(boundBy: 1).exists)
        let firstRow = rowActions.element(boundBy: 0).frame
        let secondRow = rowActions.element(boundBy: 1).frame
        XCTAssertEqual(secondRow.midY - firstRow.midY, 50, accuracy: 1)
        print("PIXEL-QUEUE-ROWS", firstRow, secondRow)
        capture("iPhone queue expanded", app: app)
        let second = app.staticTexts["检查 iPad 窄窗切换后的阅读位置"]
        XCTAssertTrue(second.exists)
        let last = app.staticTexts["确认长队列保留最后一条"]
        second.swipeUp(velocity: .slow)
        XCTAssertTrue(last.waitForExistence(timeout: 4))
        capture("iPhone queue scrolled", app: app)
        let editor = app.descendants(matching: .any).matching(identifier: "消息").firstMatch
        XCTAssertTrue(editor.exists, app.debugDescription)
        editor.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 4), app.debugDescription)
        let keyboard = app.keyboards.firstMatch
        let keyboardIntro = keyboard.buttons["Continue"].firstMatch
        if keyboardIntro.exists {
            keyboardIntro.tap()
        }
        keyboard.keys["n"].tap()
        keyboard.keys["i"].tap()
        let candidate = app.staticTexts["你"].firstMatch
        XCTAssertTrue(candidate.waitForExistence(timeout: 4), app.debugDescription)
        candidate.tap()
        XCTAssertTrue((editor.value as? String)?.contains("你") == true, app.debugDescription)
        capture("iPhone editor above keyboard", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        let row = app.buttons["检查移动端会话体验"]
        XCTAssertTrue(row.waitForExistence(timeout: 5), app.debugDescription)
        row.tap()
        XCTAssertTrue(rowActions.firstMatch.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue((editor.value as? String)?.contains("你") == true)
        let edge = app.coordinate(withNormalizedOffset: CGVector(dx: 0.002, dy: 0.34))
        let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.86, dy: 0.34))
        edge.press(forDuration: 0.08, thenDragTo: to)
        XCTAssertTrue(row.waitForExistence(timeout: 5), app.debugDescription)
    }

    func testTabletNewSessionForm() {
        let app = launch("--preview")
        let compose = app.buttons["新建会话"].firstMatch
        XCTAssertTrue(compose.waitForExistence(timeout: 8), app.debugDescription)
        compose.tap()
        let nav = app.navigationBars["新建会话"]
        XCTAssertTrue(nav.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(app.buttons["选择工作区，Shioji iOS"].firstMatch.exists, app.debugDescription)
        capture("iPad native form sheet", app: app)
        nav.buttons["取消"].tap()
        XCTAssertTrue(nav.waitForNonExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(app.buttons["检查移动端会话体验"].exists)
    }


    func testPhoneSearchAndOpenDraft() {
        let app = launch("--preview")
        let search = app.buttons["搜索会话"].firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), app.debugDescription)
        field.tap()
        field.typeText("移动端")
        XCTAssertTrue(app.buttons["检查移动端会话体验"].exists)
        XCTAssertFalse(app.buttons["核对本轮后台任务"].exists)
        capture("iPhone system search", app: app)
    }

    func testHardwareSessionNavigationShortcuts() {
        let tablet = UIDevice.current.userInterfaceIdiom == .pad
        if tablet { XCUIDevice.shared.orientation = .landscapeLeft }
        let app = launch("--preview")
        let first = "检查移动端会话体验"
        let second = "整理键盘交互和阅读位置"
        func conversation(_ title: String) -> XCUIElement {
            if tablet { return app.staticTexts[title].firstMatch }
            return app.navigationBars.staticTexts.matching(identifier: "conversation-title").matching(
                NSPredicate(format: "label == %@", title)
            ).firstMatch
        }

        let firstRow = app.buttons[first].firstMatch
        XCTAssertTrue(firstRow.waitForExistence(timeout: 8), app.debugDescription)
        firstRow.tap()
        XCTAssertTrue(conversation(first).waitForExistence(timeout: 6))
        let editor = app.textFields["消息"].firstMatch
        XCTAssertTrue(editor.waitForExistence(timeout: 5))
        editor.tap()
        app.typeText("Keep this draft")
        XCTAssertEqual(editor.value as? String, "Keep this draft")

        app.typeKey("f", modifierFlags: .command)
        let find = app.textFields["在会话中查找"].firstMatch
        XCTAssertTrue(find.waitForExistence(timeout: 6), "Command-F opens find in the active conversation")
        capture("Command-F opens find in the active conversation", app: app)
        app.buttons["关闭会话查找"].firstMatch.tap()
        XCTAssertTrue(editor.waitForExistence(timeout: 6))
        XCTAssertEqual(editor.value as? String, "Keep this draft")
        editor.tap()

        app.typeKey("k", modifierFlags: .command)
        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 6), "Command-K opens native session search from a conversation")
        app.typeText("键盘交互")
        XCTAssertEqual(search.value as? String, "键盘交互", "Typing after Command-K goes to native search")
        let secondRow = app.buttons[second].firstMatch
        XCTAssertTrue(secondRow.waitForExistence(timeout: 5))
        app.typeKey("k", modifierFlags: .command)
        XCTAssertTrue(search.waitForNonExistence(timeout: 5), "Command-K closes search while its field has focus")
        XCTAssertTrue(secondRow.waitForExistence(timeout: 5))
        secondRow.tap()
        XCTAssertTrue(conversation(second).waitForExistence(timeout: 6))

        app.typeKey("h", modifierFlags: [.command, .shift])
        XCTAssertTrue(conversation(first).waitForExistence(timeout: 6), "Command-Shift-H returns to the previous conversation")
        XCTAssertEqual(editor.value as? String, "Keep this draft")
        app.typeKey("n", modifierFlags: .command)
        let draft = app.navigationBars["新建会话"]
        XCTAssertTrue(draft.waitForExistence(timeout: 6), "Command-N opens the native draft")
        draft.buttons[tablet ? "取消" : "BackButton"].tap()
        XCTAssertTrue(conversation(first).waitForExistence(timeout: 6))
        XCTAssertEqual(editor.value as? String, "Keep this draft")

        app.typeKey(",", modifierFlags: .command)
        XCTAssertTrue(app.buttons["输入与外观"].firstMatch.waitForExistence(timeout: 6), "Command-Comma opens Settings")
    }

    func testIPadHardwareFindStaysInTheFocusedPane() throws {
        try XCTSkipUnless(UIDevice.current.userInterfaceIdiom == .pad, "Requires the iPad split view")
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = launch("--preview-board-pad-parallel")
        let left = app.textFields["消息"].firstMatch
        let right = app.textFields["右侧会话草稿"].firstMatch
        XCTAssertTrue(left.waitForExistence(timeout: 8))
        XCTAssertTrue(right.waitForExistence(timeout: 8))
        left.tap()
        app.typeText("Left draft")
        right.tap()
        app.typeText("Right draft")
        app.typeKey("f", modifierFlags: .command)
        let leftFind = app.textFields["在会话中查找"].firstMatch
        let rightFind = app.textFields["在右栏会话中查找"].firstMatch
        XCTAssertTrue(rightFind.waitForExistence(timeout: 6))
        XCTAssertFalse(leftFind.exists)
        XCTAssertEqual(left.value as? String, "Left draft")
        capture("Command-F keeps find in the focused right pane", app: app)

        app.buttons["关闭右栏查找"].firstMatch.tap()
        XCTAssertTrue(right.waitForExistence(timeout: 6))
        XCTAssertEqual(right.value as? String, "Right draft")
        left.tap()
        app.typeKey("f", modifierFlags: .command)
        XCTAssertTrue(leftFind.waitForExistence(timeout: 6))
        XCTAssertFalse(rightFind.exists)
        XCTAssertEqual(right.value as? String, "Right draft")
    }

    func testHardwarePreviousClosesArchive() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-board-session-archive")
        let archive = app.navigationBars["已归档"]
        XCTAssertTrue(archive.waitForExistence(timeout: 8))
        app.typeKey("h", modifierFlags: [.command, .shift])
        XCTAssertTrue(archive.waitForNonExistence(timeout: 6), "Command-Shift-H dismisses the archive before navigating")
    }

    func testMobileSendAndSteerReachTheTranscript() {
        for (route, text) in [("motion-send", "发送后的新消息"), ("motion-steer", "插入当前执行轮的消息")] {
            let app = launch("--preview-board-" + route)
            let composer = app.textFields["消息"].firstMatch
            XCTAssertTrue(composer.waitForExistence(timeout: 8), app.debugDescription)
            XCTAssertEqual(composer.value as? String, text)
            if route == "motion-steer" {
                app.descendants(matching: .any).matching(identifier: "发送选项").firstMatch.tap()
                app.descendants(matching: .any).matching(identifier: "介入当前轮").firstMatch.tap()
            } else {
                app.buttons["发送"].tap()
            }
            let sent = app.staticTexts[text].firstMatch
            XCTAssertTrue(sent.waitForExistence(timeout: 6), app.debugDescription)
            XCTAssertLessThan(sent.frame.maxY, composer.frame.minY)
            capture("Motion accepted " + route, app: app)
        }
    }

    func testQueueLongPressPreviewsAndDispatchesTheAcceptedMessage() {
        let app = launch("--preview-board-motion-queue")
        let options = app.buttons["发送选项"]
        XCTAssertTrue(options.waitForExistence(timeout: 8), app.debugDescription)
        options.tap()
        app.descendants(matching: .any).matching(identifier: "加入待发队列").firstMatch.tap()
        let queued = app.buttons["待发消息 1"].firstMatch
        let message = "先排队再强制插入"
        let row = app.buttons["先排队再强制插入"].firstMatch
        let appeared = row.waitForExistence(timeout: 5)
        capture("Queue after an accepted arrival", app: app)
        XCTAssertTrue(appeared, app.debugDescription)
        XCTAssertFalse(queued.exists)
        XCTAssertFalse(app.buttons["立即发送"].exists)
        row.press(forDuration: 0.65)
        let send = app.buttons["立即发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 6), app.debugDescription)
        XCTAssertTrue(app.staticTexts[message].firstMatch.exists)
        XCTAssertTrue(app.buttons["编辑"].firstMatch.exists)
        XCTAssertTrue(app.buttons["删除"].firstMatch.exists)
        XCTAssertFalse(app.keyboards.firstMatch.exists)
        capture("Queue long-press preview and operations", app: app)
        send.tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts[message].firstMatch.waitForExistence(timeout: 5))
        capture("Queued input inserted into the active turn", app: app)
    }

    func testLiveReplyGrowsAndKeepsTheComposerInPlace() {
        let app = launch("--preview-board-motion-reply-height")
        let initial = app.staticTexts["已记录第一段回复。"].firstMatch
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(initial.waitForExistence(timeout: 8), app.debugDescription)
        let before = initial.frame.minY
        let dock = composer.frame
        capture("Live reply before new paragraphs", app: app)
        let ending = app.staticTexts["追加第四段进展。"].firstMatch
        XCTAssertTrue(ending.waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertLessThan(initial.frame.minY, before - 10)
        XCTAssertEqual(composer.frame.minY, dock.minY, accuracy: 1)
        XCTAssertLessThan(ending.frame.maxY, composer.frame.minY)
        capture("Live reply after new paragraphs", app: app)
    }

    func testAutomaticQueueDispatchUsesTheAcceptedInput() {
        let app = launch("--preview-board-motion-queue-auto")
        let options = app.buttons["发送选项"]
        XCTAssertTrue(options.waitForExistence(timeout: 8), app.debugDescription)
        options.tap()
        app.descendants(matching: .any).matching(identifier: "加入待发队列").firstMatch.tap()
        let source = app.buttons["轮次结束后自动发送"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(source.waitForNonExistence(timeout: 8), app.debugDescription)
        let sent = app.staticTexts.matching(identifier: "轮次结束后自动发送")
        XCTAssertTrue(sent.firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(sent.count, 1)
        XCTAssertLessThan(sent.firstMatch.frame.maxY, app.textFields["消息"].firstMatch.frame.minY)
        capture("Automatically dispatched queued input", app: app)
    }

    func testRunningConversationWaitsForColdHistoryBeforeShowingItsTail() throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else {
            throw XCTSkip("Checks the first pushed conversation on iPhone")
        }
        let app = launch("--preview-board-running-session-cold-entry")
        let heading = app.buttons["2 次工具调用"].firstMatch
        let loading = app.descendants(matching: .any)["正在读取会话"].firstMatch
        XCTAssertTrue(loading.waitForExistence(timeout: 3))
        XCTAssertFalse(heading.exists, "The live tool group is only a partial history window")
        capture("Waiting for the full conversation", app: app)
        XCTAssertTrue(heading.waitForExistence(timeout: 24))
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(composer.exists)
        let phase = app.staticTexts["正在执行命令"].firstMatch
        XCTAssertTrue(phase.exists)
        XCTAssertGreaterThanOrEqual(composer.frame.minY - phase.frame.maxY, 0)
        XCTAssertLessThan(composer.frame.minY - phase.frame.maxY, 48)
        XCTAssertFalse(app.buttons["回到最新消息"].exists)
        capture("The first complete conversation at the latest message", app: app)
    }

    func testCollapsingAnExpandedLiveToolGroupAtTheEndKeepsTheTailVisible() {
        let app = launch("--preview-board-transcript-tool-tail")
        let group = app.buttons["2 次工具调用"].firstMatch
        let phase = app.staticTexts["正在执行命令"].firstMatch
        let latest = app.buttons["回到最新消息"].firstMatch
        let composer = app.textFields["消息"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 8))
        XCTAssertTrue(phase.waitForExistence(timeout: 4))
        group.tap()
        XCTAssertTrue(phase.waitForNonExistence(timeout: 4))
        let foldedY = group.frame.minY
        group.tap()
        XCTAssertTrue(latest.waitForExistence(timeout: 5))
        let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.72))
        let end = app.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.50))
        start.press(forDuration: 0.05, thenDragTo: end, withVelocity: .slow, thenHoldForDuration: 0.2)
        XCTAssertTrue(latest.waitForNonExistence(timeout: 5))
        XCTAssertTrue(phase.waitForExistence(timeout: 4))
        XCTAssertLessThan(phase.frame.maxY, composer.frame.minY)
        XCTAssertLessThan(group.frame.minY, foldedY - 20)
        capture("Expanded tool group scrolled to the end", app: app)
        group.tap()
        XCTAssertTrue(phase.waitForNonExistence(timeout: 4))
        XCTAssertFalse(latest.exists)
        XCTAssertEqual(group.frame.minY, foldedY, accuracy: 1)
        capture("Folded tool group still at the end", app: app)
    }

    func testPhoneReviewOpensFile() {
        let app = launch("--preview-board-diff")
        let file = app.buttons["internal/transport/connection.go"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 8), app.debugDescription)
        capture("Board diff", app: app)
        file.tap()
        XCTAssertTrue(app.navigationBars["connection.go"].waitForExistence(timeout: 5))
        capture("Board file", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        XCTAssertTrue(file.waitForExistence(timeout: 5))
    }

    func testTabletSplitReview() {
        if XCUIDevice.shared.orientation != .landscapeLeft {
            XCUIDevice.shared.orientation = .landscapeLeft
        }
        let app = launch("--preview-session")
        let session = app.buttons["检查移动端会话体验"]
        let changes = app.buttons["工作区改动"]
        XCTAssertTrue(session.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertFalse(changes.exists)
        app.buttons["会话活动"].firstMatch.tap()
        XCTAssertTrue(changes.waitForExistence(timeout: 5), app.debugDescription)
        changes.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        let file = app.buttons["internal/ui/composer.go"]
        XCTAssertTrue(file.waitForExistence(timeout: 5), app.debugDescription)
        capture("Board pad-diff", app: app)
        file.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(app.buttons["返回文件列表"].waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(session.exists)
        capture("iPad split with diff", app: app)
        app.buttons["关闭改动"].coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(app.buttons["关闭改动"].waitForNonExistence(timeout: 5))
    }
    func testSidebarToolbarMatchesDesignRow() {
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = launch("--preview-session")
        let workspace = app.buttons["切换会话范围"].firstMatch
        XCTAssertTrue(workspace.waitForExistence(timeout: 8), "Missing workspace control in the sidebar toolbar")
        let search = app.buttons["搜索会话"].firstMatch
        let filter = app.buttons["会话视图"].firstMatch
        let compose = app.buttons["新建会话"].firstMatch
        let chatMore = app.buttons["会话活动"].firstMatch
        let sidebarToggle = app.buttons["切换侧栏"].firstMatch
        for element in [search, filter, compose, chatMore, sidebarToggle] { XCTAssertTrue(element.exists) }
        XCTAssertEqual(workspace.frame.midY, search.frame.midY, accuracy: 1)
        XCTAssertEqual(search.frame.midY, filter.frame.midY, accuracy: 1)
        XCTAssertEqual(search.frame.midY, chatMore.frame.midY, accuracy: 1)
        XCTAssertEqual(search.frame.midY, sidebarToggle.frame.midY, accuracy: 1)
        XCTAssertEqual(search.frame.width, 44, accuracy: 1)
        XCTAssertEqual(filter.frame.width, 44, accuracy: 1)
        XCTAssertGreaterThan(compose.frame.midY, workspace.frame.midY)
        let tab = app.buttons["设备"].firstMatch
        XCTAssertTrue(tab.exists)
        if tab.frame.midY < app.frame.midY {
            XCTAssertGreaterThanOrEqual(workspace.frame.minY, tab.frame.maxY)
        }
        print("PIXEL-TOOLBAR", app.frame, workspace.frame, search.frame, filter.frame, compose.frame, chatMore.frame)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = "iPad sidebar toolbar"
        attachment.lifetime = .keepAlways
        add(attachment)
        sidebarToggle.tap()
        let hidden = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in !workspace.isHittable }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [hidden], timeout: 4), .completed)
        sidebarToggle.tap()
        let visible = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in workspace.isHittable }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [visible], timeout: 4), .completed)
    }
    func testIconAuditOnImplementedPages() {
        let app = launch("--preview")
        let scope = app.buttons["切换会话范围"].firstMatch
        let search = app.buttons["搜索会话"].firstMatch
        let filter = app.buttons["会话视图"].firstMatch
        XCTAssertTrue(scope.waitForExistence(timeout: 8))
        XCTAssertTrue(search.exists && filter.exists)
        print("PIXEL-PHONE-TOOLBAR", app.frame, scope.frame, search.frame, filter.frame)
        capture("Audit iPhone inbox", app: app)
        app.buttons["新建会话"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["新建会话"].waitForExistence(timeout: 5))
        capture("Audit iPhone compose", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        app.buttons["设备"].firstMatch.tap()
        let device = app.buttons["Studio Display Mac"].firstMatch
        XCTAssertTrue(device.waitForExistence(timeout: 5))
        capture("Audit iPhone devices", app: app)
        device.tap()
        XCTAssertTrue(app.navigationBars["Studio Display Mac"].waitForExistence(timeout: 5))
        app.buttons["SSH 连接"].firstMatch.tap()
        XCTAssertTrue(app.navigationBars["SSH 凭据"].waitForExistence(timeout: 5))
        capture("Audit iPhone SSH", app: app)
        app.navigationBars.buttons["BackButton"].tap()
        app.navigationBars.buttons["BackButton"].tap()
        app.buttons["设置"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["跟随系统"].waitForExistence(timeout: 5))
        capture("Audit iPhone settings", app: app)
        app.buttons["会话"].firstMatch.tap()
        app.buttons["检查移动端会话体验"].firstMatch.tap()
        let queueMore = app.buttons["待发消息操作"].firstMatch
        let more = app.navigationBars.buttons["会话活动"].firstMatch
        XCTAssertTrue(queueMore.waitForExistence(timeout: 5))
        print("PIXEL-PHONE-CHAT", app.frame, more.frame, queueMore.frame)
        capture("Audit iPhone conversation", app: app)
        more.tap()
        XCTAssertTrue(app.navigationBars["会话活动"].waitForExistence(timeout: 5))
        capture("Audit iPhone activity", app: app)
    }
    func testTabletCompactSidebarRestoresAfterRotation() {
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = launch("--preview-session")
        let scope = app.buttons["切换会话范围"].firstMatch
        XCTAssertTrue(scope.waitForExistence(timeout: 8))
        XCUIDevice.shared.orientation = .portrait
        let back = app.navigationBars.buttons["BackButton"].firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue(scope.waitForNonExistence(timeout: 4))
        capture("Audit iPad compact conversation", app: app)
        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertTrue(scope.waitForExistence(timeout: 8), app.debugDescription)
        let activity = app.buttons["会话活动"].firstMatch
        let aligned = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            scope.exists && activity.exists && abs(scope.frame.midY - activity.frame.midY) <= 1
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [aligned], timeout: 6), .completed, app.debugDescription)
    }
}
