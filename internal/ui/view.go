// Package ui renders Shioji with MyGo's native components; no WebView is used.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type editBinding struct {
	Text      string
	LastModel string
}

type View struct {
	historyPage          *conversationHistory
	historyBaseline      bool
	historyTarget        string
	deviceConnectionSave *deviceConnectionSave
	pathNotice           devicePathNotice
	sshExpectedDevice    string
	tailnet              *tailnetUI
	sshTailnetPeer       string
	commentTagEntries    map[string]time.Time

	pendingCommentInsertions []diffCommentInsertion

	commentResources     map[string]*diffCommentResource
	commentTagBounds     map[*richtext.Attachment]n.Rect
	nextCommentTagBounds map[*richtext.Attachment]n.Rect
	diffLocateSequence   uint64

	navigationSave                                                                                                  *navigationSave
	navigationSaveError                                                                                             string
	navigationGeometry                                                                                              navigationGeometry
	navigationDrag                                                                                                  *navigationDragState
	navigationScroll                                                                                                n.ScrollState
	conversationImport                                                                                              *conversationImportUI
	runningProgress                                                                                                 *runningProgress
	runningGroup, activityTurn                                                                                      string
	contextInputs                                                                                                   map[app.WorkKey]*contextInputUI
	contextRowKeys                                                                                                  map[app.WorkKey]string
	requestActionsState                                                                                             map[app.WorkKey]*requestActionState
	workPanels                                                                                                      map[app.WorkKey]*workPanelUI
	workspaceTools                                                                                                  map[string]*workspaceToolsUI
	skillManagement                                                                                                 map[string]*skillManagementUI
	skillReturn                                                                                                     *skillManagementReturn
	directoryPicker                                                                                                 *directoryPicker
	toolServices                                                                                                    map[string]*toolServicesUI
	servicePreparations                                                                                             map[string]*servicePreparationUI
	servicePreparationRestored                                                                                      bool
	workerMigrations                                                                                                map[string]*workerMigrationUI
	workerMigrationRestored                                                                                         bool
	sshHostKey                                                                                                      *transport.SSHHostKey
	sshTerminal                                                                                                     *sshSetupTerminal
	agentSignIns                                                                                                    map[string]*agentSignInUI
	welcome                                                                                                         *welcomeFlow
	composerDocuments                                                                                               map[string]*composerDocument
	fileMention                                                                                                     *fileMention
	finds                                                                                                           map[string]*conversationFind
	searchResultsQuery                                                                                              string
	historyRowBounds                                                                                                map[string]n.Rect
	historyExit                                                                                                     *historyRestartMotion
	historyFocus                                                                                                    string
	historyForked                                                                                                   map[string]bool
	historyChecked                                                                                                  map[string]string
	historyBusy                                                                                                     map[string]bool
	notificationDeliveries                                                                                          []nativeNotificationDelivery
	nativeWindow                                                                                                    *mygo.Window
	settings                                                                                                        *Settings
	notificationContext                                                                                             context.Context
	cancelNotifications                                                                                             context.CancelFunc
	viewportWidth                                                                                                   float32
	previousSession, previousWorkspace                                                                              string
	OpenSettings                                                                                                    func()
	services                                                                                                        n.Services
	recoveries                                                                                                      map[string]*recoveryState
	paneFocusVersion                                                                                                uint64
	outline                                                                                                         *conversationOutline
	parent, secondary                                                                                               *View
	visibleSessions                                                                                                 map[string]string
	attachmentStore                                                                                                 *attachments.Repository
	attachmentCache                                                                                                 *app.AttachmentCache
	attachmentImages                                                                                                map[string]*attachmentImage
	attachmentUploading                                                                                             map[string]bool
	attachmentPreparing                                                                                             map[string]int
	attachmentPreview                                                                                               *attachmentPreview
	navVisibility                                                                                                   map[string]*navVisibility
	workspaceEditor                                                                                                 *workspaceEditor
	rename                                                                                                          *sessionRename
	showArchived                                                                                                    bool
	currentTranscriptRow                                                                                            int
	activityMarkdown                                                                                                markdown.Options
	secretAnswers                                                                                                   map[string]string
	secretBindings                                                                                                  map[string]*editBinding
	activityPatches                                                                                                 map[string]*activityPatch
	replyStreams                                                                                                    map[string]*replyStream
	replySession                                                                                                    string
	replyEpoch                                                                                                      string
	replyFrame                                                                                                      uint64
	replyLatestOrder                                                                                                int64
	toolMotions                                                                                                     map[string]*toolDisclosureMotion
	toolArrivals                                                                                                    *toolArrivalState
	frameTime                                                                                                       time.Time
	toolsAnimating                                                                                                  bool
	homeSend                                                                                                        *homeSendPresentation
	sent                                                                                                            *sentMessageMotion
	sessionEntry                                                                                                    *sessionEntryMotion
	composerHadFocus                                                                                                bool
	focusComposer                                                                                                   bool
	queueBusy                                                                                                       map[string]bool
	queueDrag                                                                                                       *queuedRowDrag
	queueSources                                                                                                    map[string]queuedMessageSource
	queueSourceSession                                                                                              string
	queueSourceEpoch                                                                                                string
	queueArrivals                                                                                                   map[string]*queuedArrivalMotion
	queueDisplay                                                                                                    domain.QueueState
	composerHeightKey                                                                                               string
	composerInputHeight                                                                                             float32
	composerNaturalHeight                                                                                           float32
	composerBaseHeight                                                                                              float32
	composerEditID                                                                                                  string
	composerHeightUntil                                                                                             time.Time
	composerBounds                                                                                                  n.Rect
	inputBounds                                                                                                     n.Rect
	reduceMotion                                                                                                    bool
	draftBindings                                                                                                   map[string]*editBinding
	answerBindings                                                                                                  map[string]*editBinding
	customBindings                                                                                                  map[string]*editBinding
	Saver                                                                                                           *app.StateWriter
	lastQueued                                                                                                      []byte
	lastState                                                                                                       []byte
	Model                                                                                                           *app.Model
	Controller                                                                                                      *app.Controller
	StatePath                                                                                                       string
	lists                                                                                                           map[string]*n.ListState
	nav                                                                                                             n.ListState
	fileLink                                                                                                        *fileLinkState
	md                                                                                                              markdown.Options
	permissionOpen                                                                                                  bool
	runMenu                                                                                                         runMenuPhase
	devices                                                                                                         bool
	selected                                                                                                        string
	selectedDevice                                                                                                  string
	deviceSelected                                                                                                  string
	deviceList                                                                                                      bool
	deviceTab                                                                                                       string
	deviceSearch                                                                                                    string
	deviceStorageStates                                                                                             map[string]*deviceStorageState
	clientStorageState                                                                                              *deviceStorageState
	sshCheck                                                                                                        sshCheckPhase
	sshSetup                                                                                                        app.DeviceSetupCheck
	sshAgents                                                                                                       *deviceAgentEditor
	sshName, sshTarget, sshPort, sshError, sshRequest                                                               string
	remoteWorkspaceOpen                                                                                             bool
	remoteWorkspaceConnection, remoteWorkspaceName, remoteWorkspacePath, remoteWorkspaceExtra, remoteWorkspaceError string
	remoteWorkspaceRequest                                                                                          string
	remoteWorkspaceBusy                                                                                             bool
	collapsedWorkspaces                                                                                             map[string]bool
	dockHeight                                                                                                      float32
	searchOpen                                                                                                      bool
	searchText, searchSent                                                                                          string
	searchResults                                                                                                   []domain.SearchResult
	searchList                                                                                                      n.ListState
	branchOpen                                                                                                      bool
	branchSearch                                                                                                    string
	branchList                                                                                                      n.ListState
	switchingBranch                                                                                                 bool
	gitLoads                                                                                                        map[string]*gitLoadState
	resizingInspector                                                                                               bool
	changes                                                                                                         map[string]*changesState
	menuOpen                                                                                                        map[string]bool
	menuContext                                                                                                     string
}

func New(model *app.Model, controller *app.Controller, path string) *View {
	v := &View{historyForked: map[string]bool{}, historyChecked: map[string]string{}, historyBusy: map[string]bool{}, draftBindings: map[string]*editBinding{}, answerBindings: map[string]*editBinding{}, customBindings: map[string]*editBinding{}, Model: model, Controller: controller, StatePath: path, lists: map[string]*n.ListState{}, collapsedWorkspaces: map[string]bool{}, md: markdown.Options{ShareSelection: true, WrapCode: true}}
	if path != "" {
		v.Saver = app.NewStateWriter(path)
	}
	return v
}
func (v *View) collectBindings() {
	for _, b := range v.composerDocuments {
		edit := v.Model.QueueEdits[strings.TrimPrefix(b.key, "session:")]
		if b.editToken == edit.Token {
			v.publishComposer(b.key, b)
		}
	}
	for key, b := range v.draftBindings {
		if b.Text != b.LastModel {
			v.Model.Drafts[key] = b.Text
			b.LastModel = b.Text
		}
	}
	for key, b := range v.answerBindings {
		if b.Text != b.LastModel {
			v.Model.Answers[key] = b.Text
			b.LastModel = b.Text
		}
	}
	for key, b := range v.customBindings {
		if b.Text != b.LastModel {
			v.Model.Answers["custom:"+key] = b.Text
			v.Model.Answers[key] = b.Text
			b.LastModel = b.Text
		}
	}

}
func (v *View) Flush() error {
	root := v.splitRoot()
	root.collectBindings()
	if root.secondary != nil {
		root.secondary.collectBindings()
	}
	if root.Saver == nil {
		return nil
	}
	data, err := root.Model.EncodeState()
	if err != nil {
		return err
	}
	return root.Saver.Flush(data)
}

func (v *View) queueState() {
	data, err := v.Model.EncodeState()
	if err == nil && !bytes.Equal(data, v.lastQueued) {
		v.Saver.Queue(data)
		v.lastQueued = data
	}
}

func (v *View) watchStateWriter() {
	mainServices := v.services
	settingsServices := n.Services{}
	if v.settings != nil {
		settingsServices = v.settings.services
	}
	v.Saver.SetInvalidate(func() { mainServices.Invalidate(); settingsServices.Invalidate() })
}

func svg(path string) *n.SVG {
	return n.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

var plus = svg("M10 4v12M4 10h12")
var chevron = svg("M6 8l4 4 4-4")
var monitor = svg("M3 3h14v10H3zM7 17h6M10 13v4")
var check = svg("M4 10l4 4 8-9")
var attention = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var arrow = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M12 21V3M4 11l8-8 8 8" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var stop = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><rect x="6" y="5" width="12" height="14" rx="2" fill="currentColor"/></svg>`))
var terminalIcon = svg("M4 6l4 4-4 4M11 14h5")
var questionIcon = svg("M17.5 13.3h-10l-5 4.2v-15h15z")

// Native thumb: 6 px, 2 px wider while dragged, and 3 px from the edge.
const conversationScrollbarGutter = 11

func (v *View) View(c *n.Context) {
	m := v.Model
	m.Preferences = m.Preferences.Normalized()
	c.ReduceMotion(m.Preferences.ReduceMotion)
	v.services = c.Services()
	v.viewportWidth, _ = c.Size()
	v.frameTime, v.reduceMotion = c.Now(), c.Preferences().ReduceMotion
	if v.Controller != nil && v.nativeWindow == nil {
		v.Controller.SetInvalidate(c.Services().Invalidate)
	}
	v.collectBindings()
	if v.secondary != nil {
		v.secondary.collectBindings()
	}
	v.pumpUpdates()
	if p := v.historyPage; p != nil && (p.session != m.SessionID || p.epoch != m.ConnectionEpoch || v.devices) {
		v.cancelPagedHistory()
	}
	v.syncDeviceConnectionSave()
	m.RefreshDevices()
	m.SyncHistoryEdits()
	v.syncHistoryExit(c)
	if pane := v.secondary; pane != nil {
		// A confirmed action must finish even if the split was hidden or closed.
		pane.reduceMotion = v.reduceMotion
		pane.syncHistoryExit(c)
	}
	v.syncHistoryActions()
	v.syncQueuedMotions()
	v.syncHomeSend()
	v.syncSendMotion()
	v.syncSessionEntry()
	if v.selected != m.SessionID || v.selectedDevice != m.ConnectionID() {
		v.selected = m.SessionID
		v.selectedDevice = m.ConnectionID()
		if state := v.lists[m.SessionID]; state != nil {
			state.ReleaseRow()
		}
		m.FoldToolHistory[m.SessionID] = true
		if v.Controller != nil {
			v.Controller.SelectDevice(m.ConnectionID(), m.SessionID)
		}
	}
	v.commentTagBounds = v.nextCommentTagBounds
	v.nextCommentTagBounds = map[*richtext.Attachment]n.Rect{}
	t := *applyAppearance(c, m.Preferences)
	for id, born := range v.commentTagEntries {
		if c.Now().Sub(born) >= 180*time.Millisecond {
			delete(v.commentTagEntries, id)
		}
	}
	v.flushDiffCommentInsertions(c)
	v.syncTailnet()
	v.syncAgentSignIns(c)
	v.syncToolServiceAuthorizations(c)
	v.syncWorkerMigrations(c)
	v.syncServicePreparation(c)
	// Keep the overlay parent alive so popup entrance and exit both have a
	// persistent parent, even when no popup is currently open.
	n.Overlay(c, func() {})
	v.md.Theme = replyTheme(m.Preferences)
	v.bindMarkdownLinks(c, &v.md)
	if v.fileLink != nil && !v.fileLinkCurrent(v.fileLink) {
		v.cancelFileLink()
	}
	if v.Saver != nil {
		v.watchStateWriter()
		saved, err := v.Saver.Result()
		v.lastState = saved
		if !v.syncNavigationSave(saved, err) && err != nil {
			m.Error = "无法保存草稿：" + err.Error()
		}
		defer v.queueState()
	}

	if m.FirstRun {
		v.welcomeView(c, &t)
		v.directoryDialog(c, &t)
		v.devicePathFeedback(c, &t)
		return
	}

	menuContext := fmt.Sprintf("%s/%s/%t", m.DraftKey(), m.WorkspaceID, v.devices)
	if menuContext != v.menuContext {
		clear(v.menuOpen)
		v.menuContext = menuContext
	}
	v.prepareSecondary()
	v.requestGit(false)
	if c.Shortcut(n.Cmd, n.KeyK) {
		v.searchOpen = !v.searchOpen
	}
	if c.Shortcut(n.Cmd, n.KeyF) {
		v.focusedView().openConversationFind()
	}
	if c.Shortcut(n.Cmd, n.KeyG) {
		if pane := v.focusedView(); pane.findState().mode == findLocal {
			pane.stepConversationFind(1)
		}
	}
	if c.Shortcut(n.Cmd|n.Shift, n.KeyG) {
		if pane := v.focusedView(); pane.findState().mode == findLocal {
			pane.stepConversationFind(-1)
		}
	}
	if c.Shortcut(n.Cmd, n.KeyN) {
		v.home()
	}
	if c.Shortcut(n.Cmd, n.KeyComma) && v.OpenSettings != nil {
		v.OpenSettings()
	}
	if c.Shortcut(n.Cmd|n.Shift, n.KeyJ) {
		v.nextAttention()
	}
	if c.Shortcut(n.Cmd|n.Shift, n.KeyH) && v.previousSession != "" {
		v.navigateSession(v.previousSession, v.previousWorkspace)
	}
	if m.SessionID != "" && !v.devices {
		v.syncReplyStreams(m.Details[m.SessionID])
	}
	owner := v
	main := n.Row(c.Key("main-view")).Fill().Background(t.Background)
	if f := v.welcome; f != nil && f.step == welcomeDone {
		distance, _ := c.Size()
		duration := 260 * time.Millisecond
		if c.Preferences().ReduceMotion {
			distance = 0
			duration = 0
		}
		main.Transition(n.ElementTransition{Duration: duration, Ease: n.EaseOut, Enter: &n.Motion{X: distance, Opacity: 1}})
		if c.Now().Sub(f.changed) > 260*time.Millisecond {
			v.welcome = nil
		}
	}
	main.Children(func() {
		if v.devices && v.skillReturn == nil {
			v.deviceView(c, &t)
			return
		}
		v.sidebar(c, &t)
		width, _ := c.Size()
		owner = v.focusedView()
		open := owner.Model.SessionID != "" && !v.devices && v.workspaceEditor == nil && m.Inspectors[owner.Model.SessionID] && (!m.FocusSecondary || m.SecondarySessionID == owner.Model.SessionID)
		inline := width > 1160
		if !open {
			v.resizingInspector = false
		}
		panelWidth := v.Model.InspectorWidth
		if panelWidth == 0 {
			panelWidth = 280
		}
		limit := width - 256 - 360
		if !inline {
			navigation := float32(236)
			if width <= 980 {
				navigation = 224
			}
			limit = width - navigation - 16
		}
		panelWidth = min(max(panelWidth, 280), min(limit, 960))
		conversation := n.Column(c.Key("conversation-pane")).FillHeight().Grow(1)
		if !v.resizingInspector {
			conversation.Transition(n.ElementTransition{Duration: motionAnimation(c, 200*time.Millisecond).Duration, Ease: n.EaseOut, Size: true})
		}
		conversation.Children(func() {
			if v.workspaceEditor != nil || v.skillReturn != nil {
				page, depth := "workspace", 0
				if v.devices && v.skillReturn != nil {
					page, depth = "device-skills", 1
				}
				configurationTabPanel(c.Key("workspace-navigation"), page, depth, configurationPanelFill, func() {
					if v.devices && v.skillReturn != nil {
						r := v.skillReturn
						v.skillDeviceManagementPage(c, &t, r)
					} else {
						v.toolbar(c, &t)
						v.workspaceSettingsView(c, &t)
					}
				}, false)
			} else if m.SessionID == "" && v.homeSend == nil {
				v.toolbar(c, &t)
				v.homeView(c, &t)
			} else {
				v.parallelConversation(c, &t)
			}
		})
		if open {
			if inline {
				column := n.Column(c.Key("review-column")).FillHeight().Shrink(0).Clip()
				if !v.resizingInspector {
					column.Transition(columnMotion(c, panelWidth))
				}
				column.Children(func() { owner.inspector(c, &t, false, panelWidth, limit) })
			} else {
				owner.inspector(c, &t, true, panelWidth, limit)
			}
		}
	})
	if v.selected != m.SessionID || v.selectedDevice != m.ConnectionID() {
		v.selected = m.SessionID
		v.selectedDevice = m.ConnectionID()
		if state := v.lists[m.SessionID]; state != nil {
			state.ReleaseRow()
		}
		m.FoldToolHistory[m.SessionID] = true
		if v.Controller != nil {
			v.Controller.SelectDevice(m.ConnectionID(), m.SessionID)
		}
		c.After(0)
	}
	if owner != v {
		m.InspectorWidth = owner.Model.InspectorWidth
		m.InspectorLayout = owner.Model.InspectorLayout
		m.InspectorSplitHeight = owner.Model.InspectorSplitHeight
	}
	v.updateVisibleSessions()
	v.pruneHistoryCaches()
	v.deviceDialogs(c, &t)
	v.managementDialogs(c, &t)
	v.fileLinkDialog(c, &t)
	v.attachmentDialog(c, &t)
	v.directoryDialog(c, &t)
	v.conversationImportDialog(c, &t)
	v.devicePathFeedback(c, &t)
}

func (v *View) home() {
	v.focusedView().cancelPagedHistory()
	if v.homeSend != nil {
		v.sent = nil
	}
	v.clearHomeSend()
	if v.Model.SessionID != "" {
		v.previousSession, v.previousWorkspace = v.Model.SessionID, v.Model.WorkspaceID
	}
	v.workspaceEditor = nil
	v.skillReturn = nil
	v.Model.PrepareNewSession()
	v.Model.FocusSecondary = false
	v.devices = false
	v.runMenu = runMenuClosed
	v.permissionOpen = false
	v.branchOpen = false
	v.searchOpen = false
}
func iconButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return control(c).Size(28, 28).Radius(6).Label(label).Tooltip(label).TextColor(c.Theme().TextMuted).Children(func() { n.Icon(c, icon).Size(16, 16) })
}
func ghost(c *n.Context, label string, radius ...float32) n.Element {
	r := c.Theme().Radius
	if len(radius) > 0 {
		r = radius[0]
	}
	return controlFeedback(c, n.Button(c, label).Radius(r), r).FontWeight(400).Justify(n.Start).Background(n.Transparent).Border(0, n.Transparent).Height(28)
}
func (v *View) toolbar(c *n.Context, t *n.Theme) {
	if v.workspaceEditor != nil && !v.devices {
		v.workspaceSettingsHeader(c, t)
		return
	}
	titleAnimation := motionAnimation(c, 160*time.Millisecond)
	m := v.Model
	rightInset := float32(24)
	if m.SessionID != "" && !v.devices {
		// Controls stop before the native scrollbar's independent gutter.
		rightInset = 18 - conversationScrollbarGutter
	}
	n.Row(c).Height(48).FillWidth().Padding(0, rightInset, 0, 24).Gap(6).AlignItems(n.Center).DragWindow().Label("会话工具栏").Children(func() {
		if v.devices {
			n.Text(c, "设备").FontSize(13).Grow(1)
			add := iconButton(c, plus, "添加设备")
			v.choiceMenu(c, t, "add-device", add, []menuChoice{{id: "ssh", name: "直接 SSH", choose: func() { v.dismissDeviceSetup(); v.sshCheck = v.sshCheck.show() }}, {id: "tailnet", name: "从 Tailnet 添加", disabled: true}})
			return
		} else if m.SessionID != "" {
			heading := n.Row(c.Key("session-heading:" + m.SessionID)).Grow(1).MinWidth(0).Gap(6).AlignItems(n.Center).Label("会话名称")
			if progress := v.sessionEntryProgress(c, m.SessionID, true); progress < 1 {
				heading.Opacity(progress).Transform(n.Translation(0, 4*(1-progress)))
			}
			heading.Children(func() {
				for _, s := range m.Snapshot.Sessions {
					if s.ID == m.SessionID {
						v.toolbarWorkspace(c, t)
						n.Column(c).Size(1, 14).Background(t.Border).MarginX(8)
						n.Text(c, s.Title).SingleLine().Grow(1).FontSize(13).FontWeight(400).ContentTransition(n.ContentTransition{Animation: &titleAnimation})
					}
				}
			})
		} else {
			n.Spacer(c).Grow(1)
		}
		device := control(c).Height(26).PaddingX(6).Radius(6).Label("当前设备")
		device.Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
				color := t.TextMuted
				if m.Connected {
					color = t.Success
				}
				n.Column(c).Size(4, 4).Radius(2).Background(color).AlignSelf(n.Center)
				font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400}
				inlineIconText(c, v.deviceGlyph(m.ConnectionID()), m.CurrentDeviceName(), font, 14, 6, t.TextMuted)
				inlineSymbol(c, chevron, m.CurrentDeviceName(), font, 12, t.TextMuted)
			})
		})
		status := "已连接"
		if !m.Connected {
			status = "未连接"
		}
		v.choiceMenu(c, t, "current-device", device, []menuChoice{{id: "connectivity", name: status, disabled: true}, {id: "runtime", name: m.RunReadiness().Descriptor().Name + " · " + string(m.RunReadiness().State), disabled: true}, {id: "details", name: "设备详情", choose: func() { v.deviceSelected = m.ConnectionID(); v.deviceList = false; v.devices = true }}})
		if m.SessionID != "" && !v.devices {
			label := "展开更改侧栏"
			if m.Inspectors[m.SessionID] {
				label = "收起更改侧栏"
			}
			if iconButton(c, sidebarIcon, label).Clicked() {
				m.Inspectors[m.SessionID] = !m.Inspectors[m.SessionID]
			}
		}

	})
}
func (v *View) workspaceName() string {
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == v.Model.WorkspaceID {
			return w.Name
		}
	}
	return "工作区"
}
func (v *View) homeView(c *n.Context, t *n.Theme) {
	n.Column(c).Fill().PaddingX(36).AlignItems(n.Center).Children(func() {
		n.Spacer(c).Grow(.84)
		if len(v.Model.Snapshot.Workspaces) == 0 {
			n.Text(c, "从一个工作区开始").FontSize(28)
			if ghost(c, "创建工作区").Margin(18, 0, 0, 0).Disabled(!v.Model.Connected).Clicked() {
				v.createWorkspace()
			}
		} else {
			n.Row(c).Gap(8).Margin(0, 0, 28, 0).AlignItems(n.Center).Children(func() {
				n.Text(c, "想在").FixedLineHeight(48).FontSize(32).TextColor(t.TextMuted)
				picker := control(c).Height(48).MaxWidth(360).Label("选择工作区").Children(func() {
					n.Text(c, v.workspaceName()).FixedLineHeight(48).FontSize(32).FontWeight(400).Underline().SingleLine()
				})
				choices := []menuChoice{}
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.Archived {
						continue
					}
					w := w
					choices = append(choices, menuChoice{id: w.ID, name: w.Name, detail: v.Model.DeviceName(w.DeviceID), checked: w.ID == v.Model.WorkspaceID, choose: func() { v.Model.WorkspaceID = w.ID; v.Model.PrepareNewSession() }})
				}

				v.choiceMenu(c, t, "home-workspace", picker, choices)
				n.Text(c, "做些什么？").FixedLineHeight(48).FontSize(32).TextColor(t.TextMuted)
			})
			v.composer(c, t, true)
		}
		v.notice(c, t)
		n.Spacer(c).Grow(1.16)
	})
}
func (v *View) notice(c *n.Context, t *n.Theme) {
	v.historyOperationNotice(c, t)
	v.archivedNotice(c, t)
	message := v.Model.Error
	if message == app.ModelUnavailableMessage {
		message = ""
	}
	if message == "" && !v.Model.Connected {
		message = "连接执行服务中…"
	}
	if message == "" && v.Model.RunReadiness().State != "ready" {
		message = v.Model.RunReadiness().Error
	}
	gap := float32(0)
	if v.Model.SessionID != "" {
		gap = 8
	}
	frame := n.Column(c.Key("conversation-notice:"+v.Model.SessionID)).FillWidth().MaxWidth(736).Clip().Background(t.Background).Margin(-gap, 0, 0, 0)
	retained := n.Local(frame, "message", func() string { return message })
	if message != "" {
		*retained = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Text(c.Key("message"), *retained).FontSize(12).TextColor(t.TextMuted).Padding(10+gap, 0, 0, 0).
			Transition(fadeMotion(c, 180*time.Millisecond)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
		return 0
	})
}
func (v *View) composer(c *n.Context, t *n.Theme, home bool) {
	m := v.Model
	key := m.DraftKey()
	waiting := v.homeSend != nil && m.SessionID == ""
	if waiting {
		key = "home-send-editor:" + v.homeSend.in.OperationID
	}
	stackKey := key
	if m.SessionID != "" {
		stackKey = "session:" + m.SessionID
	}
	stackKey = v.homeSendKey(stackKey)
	binding := v.composerDocument(c, key)
	draft := m.Drafts[key]
	cfg := m.Config()
	running := false
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID {
			running = s.Status.Active()
		}
	}
	bg := t.Background
	if t.Dark {
		bg = t.Surface
	}
	maxWidth := float32(768)
	inputHeight := float32(54)
	inputSize, inputLeading := float32(14), float32(22)
	if home {
		maxWidth = 736
		inputHeight = 72
		inputSize, inputLeading = 15, 26
	}
	stack := n.Column(c.Key("composer-stack:" + stackKey)).Label("消息编辑区").FillWidth().MaxWidth(maxWidth).Gap(0).Children(func() {
		v.queueDisclosure(c, t)
		binding = v.composerDocument(c, key)

		editID := m.QueueEdits[m.SessionID].ID
		if v.composerHeightKey != stackKey {
			v.composerHeightKey, v.composerNaturalHeight, v.composerEditID = stackKey, 0, editID
			v.composerBaseHeight = 0
			v.composerHeightUntil = time.Time{}
			v.composerInputHeight = inputHeight
		}
		if v.composerEditID != editID {
			v.composerEditID = editID
			v.composerHeightUntil = c.Now().Add(360 * time.Millisecond)
		}
		heightFrame := n.Column(c.Key("composer-height:"+stackKey)).Label("消息编辑器").FillWidth().Clip().Background(bg).Border(1, t.Border).Radius(16).Shadow(0, 4, 14, 0, n.RGBA(0, 0, 0, .04))
		resize := c.Now().Before(v.composerHeightUntil) || editID != ""
		// The attachment strip already animates its layout. Animate only the
		// remaining editor height, then add this frame's strip height directly.
		baseHeight := v.composerBaseHeight
		shownHeight := baseHeight
		if v.composerNaturalHeight > 0 {
			if resize {
				shownHeight = heightFrame.AnimateTo("composer-height", baseHeight, n.Animation{Duration: 360 * time.Millisecond, Ease: composerDescentEase})
			} else {
				heightFrame.Jump("composer-height", baseHeight)
			}
		}
		attachmentHeight := float32(0)
		heightFrame.Children(func() {
			var input composerInput
			composer := n.Column(c.Key("composer:"+stackKey)).FillWidth().Padding(10, 14, 8, 6).Gap(4).Children(func() {
				if m.QueueEdits[m.SessionID].ID != "" {
					n.Column(c).Height(28)
				}
				binding = v.composerDocument(c, key)
				v.branchStrip(c, t, home)
				v.contextCommandNotice(c, t)
				if !waiting {
					attachmentHeight = v.draftAttachmentStrip(c, t)
				}
				n.Column(c.Key("composer-input-space")).Height(v.composerInputHeight).Shrink(0)
				// A zero-height origin fixed above the footer keeps outgoing editor
				// snapshots still while the composer changes its natural height.
				n.Column(c.Key("composer-input-anchor")).Absolute().Left(6).Right(14).Bottom(40).Height(0).Children(func() {
					inputKey := "input"
					if editID != "" {
						inputKey += ":edit:" + editID
					}
					options := richtext.EditorOptions{Label: v.composerLabel(), Placeholder: "描述任务…", Font: n.Font{Family: t.Font, Size: inputSize, Weight: 400}, NoMarkdown: true, PlainText: true, MaxLines: 7, LineHeight: inputLeading, Theme: &richtext.Theme{Muted: subtle(t), Selection: editorSelection(t)}}
					options.ReadOnly = waiting
					options.OnKey = func(keyCode n.Key, mods n.Modifiers) bool {
						k := composerKey{Key: keyCode, Mods: mods, binding: binding}
						v.publishComposer(key, binding)
						if v.mentionKey(k) {
							return true
						}
						if keyCode != n.KeyEnter {
							return false
						}
						bind := composerBinding(mods)
						if bind == m.Preferences.Shortcuts.Newline {
							return false
						}
						if bind == m.Preferences.Shortcuts.Send || bind == m.Preferences.Shortcuts.Steer {
							if m.HasDraftContent(key) {
								v.sendMessage(bind == m.Preferences.Shortcuts.Steer)
							}
						}
						return true
					}
					options.OnPaste = func(text string) bool { return v.pasteAttachments(key, text) }
					if s := v.sent; s != nil && s.source.W > 0 && s.session == m.SessionID && !s.queued && !home && !c.Preferences().ReduceMotion {
						options.Theme.Muted = subtle(t).Alpha(v.sendProgress(c))
					}
					input = composerInput{Element: richtext.EditorBase(c.Key(inputKey), binding.doc, options).FocusRing(false).Absolute().Left(9).Right(9).Bottom(0).FillWidth().MinHeight(inputHeight), binding: binding}

					input.Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})

					if v.focusComposer {
						input.Focus()
						v.focusComposer = false
					}
					input.Changed()
					v.inputBounds = input.Bounds()
					if s := v.sent; s != nil && s.origin == "" && s.session == m.SessionID && !home && !s.focused {
						if v.composerHadFocus {
							input.Focus()
						}
						s.focused = true
					}
					v.publishComposer(key, binding)
					draft = m.Drafts[key]
					v.diffCommentInputHover(c, t, input)
					v.syncMention(c, input, binding.doc.Text().String())
					v.mentionPopup(c, t, heightFrame, input)
				})
				n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
					if iconButton(c, plus, "添加附件").Size(28, 28).Disabled(v.Controller == nil || v.StatePath == "" || m.WorkerMinor < 6 || waiting || v.preparingAttachments(m.DraftKey())).Clicked() {
						v.pickAttachments()
					}
					permission := control(c).Height(28).PaddingX(6).Radius(6).Label("Permission").Children(func() {
						inlineIconText(c, shieldIcon, m.PermissionName(cfg.Permission), n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 13, 6, t.TextMuted)
					})
					choices := []menuChoice{}
					for _, id := range m.RunReadiness().Permissions {
						id := id
						detail := m.PermissionDescription(id)
						choices = append(choices, menuChoice{id: id, name: m.PermissionName(id), detail: detail, checked: cfg.Permission == id, choose: func() { cfg.Permission = id; m.SetConfig(key, cfg) }})
					}
					v.choiceMenu(c, t, "permission:"+key, permission, choices)
					n.Spacer(c).Grow(1)
					if !home {
						v.contextMeter(c, t, cfg)
					}
					v.runControl(c, t, key, cfg)
					empty := strings.TrimSpace(draft) == "" && len(m.Attachments[key]) == 0 && len(m.DraftSkills[key]) == 0
					icon := arrow
					iconSize := float32(28.0 * 14 / 24)
					iconKey := "primary-send-icon"
					label := "发送"
					editing := m.QueueEdits[m.SessionID].ID != ""
					queuing := running || len(m.Queue().Items) > 0
					disabled := !m.Connected || m.RunReadiness().State != "ready" || m.ModelUnavailable() || m.ReadOnlyConversationReason() != "" || empty || m.HasPendingInput() || v.homeSendPending() || m.ArchivedContext() != "" || v.preparingAttachments(m.DraftKey())
					if queuing {
						label = "加入队列"
						disabled = disabled || m.WorkerMinor < 2
					}
					if editing {
						label = "保存编辑"
						disabled = disabled || v.queueBusy[m.SessionID]
					}
					if fields := strings.Fields(draft); !editing && len(fields) > 0 && fields[0] == "/compact" {
						label = "压缩上下文"
						disabled = v.compactReason() != ""
					}
					if running && empty && !editing && (!v.homeSendPending() || m.ActiveTurn() != "") {
						icon = stop
						iconSize = 18
						iconKey = "primary-stop-icon"
						label = "停止当前轮"
						disabled = !m.Connected || !m.RunReadiness().Capabilities.Interrupt
					}
					fill := t.Accent
					if disabled {
						fill = subtle(t)
					}
					send := controlFeedback(c, n.ButtonBase(c), 14, true).Size(28, 28).Radius(14).Background(fill).TextColor(t.AccentText).Label(label).Disabled(disabled).Children(func() {
						n.Icon(c.Key(iconKey), icon).Size(iconSize, iconSize).Transition(n.ElementTransition{Duration: motionAnimation(c, 120*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
					})
					if reason := m.ReadOnlyConversationReason(); reason != "" {
						send.Tooltip(reason)
					}
					if send.Clicked() {
						if running && empty && !editing {
							v.interrupt()
						} else {
							m.Drafts[key] = draft
							v.submit()
						}
					}
				})
			})

			if v.composerNaturalHeight > 0 && resize {
				composer.Absolute().Left(0).Right(0).Bottom(0)
			}
			invalidate := c.Services().Invalidate
			composer.DrawOver(func(_ *n.Painter, r n.Rect) {
				// Measure from the live editor's layout even at zero opacity;
				// outgoing editor snapshots must never feed the dock's height.
				if input.Valid() {
					bounds := input.LayoutBounds()
					if bounds.H > 0 && math.Abs(float64(v.composerInputHeight-bounds.H)) > .5 {
						v.composerInputHeight = bounds.H
						invalidate()
					}
				}
				if v.composerHeightKey == stackKey {
					changed := math.Abs(float64(v.composerNaturalHeight-r.H-2)) > .5
					v.composerNaturalHeight = r.H + 2
					base := r.H + 2 - attachmentHeight
					if math.Abs(float64(v.composerBaseHeight-base)) > .5 {
						v.composerBaseHeight = base
						changed = true
					}
					if changed {
						invalidate()
					}
				}
			})
			if m.QueueEdits[m.SessionID].ID != "" {
				// Keep expanding editor contents below the pinned edit controls.
				n.Column(c.Key("queue-edit-header-surface")).Absolute().Left(1).Right(1).Top(1).Height(37).Radius(15, 15, 0, 0).Background(bg).IgnoreInput(true)
			}
			v.queueEditBar(c, t)
		})
		if v.composerNaturalHeight > 0 && resize {
			heightFrame.Height(shownHeight + attachmentHeight)
		}
	})
	if !home {
		stack.Draw(func(p *n.Painter, r n.Rect) {
			// Continue the bottom clearance behind the editor's 16 px lower
			// corners, before its rounded surface and border are painted.
			p.Fill(n.Rect{X: r.X, Y: r.Y + r.H - 16, W: r.W, H: 16}, t.Background, 0)
		})
	}
	if files := stack.DroppedFiles(); len(files) > 0 {
		v.addAttachmentFiles(key, files)
	}
	v.composerSendTransition(c, stack, home)
	v.composerHadFocus = stack.FocusWithin()
}
func (v *View) runControl(c *n.Context, t *n.Theme, key string, cfg domain.RunConfig) {
	ready := v.Model.RunReadiness()
	descriptor := ready.Descriptor()
	summary := cfg.Model
	if cfg.Effort != "" {
		summary += " · " + cfg.Effort
	}
	if cfg.Plan {
		summary += " · Plan"
	}
	if cfg.Fast {
		summary += " · Fast"
	}
	missing := v.Model.ModelUnavailable()
	tip := descriptor.Name + " · " + summary
	if missing {
		tip = cfg.Model + " 已不可用，请更换模型"
	}
	trigger := control(c).Height(28).MaxWidth(390).MinWidth(0).PaddingX(6).Radius(6).Label("运行配置").Tooltip(tip)
	modelColor := t.TextMuted
	if missing {
		modelColor = t.Danger
	}
	modelColor = trigger.AnimateColor("model-availability", modelColor, motionAnimation(c, 180*time.Millisecond))
	trigger.Children(func() {
		font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
		text := cfg.Model
		if cfg.Effort != "" {
			text += " · " + cfg.Effort
		}
		spans := []n.Span{inlineIconSpan(text, font, 20, 3), {Text: cfg.Model, Color: modelColor}, {Text: strings.TrimPrefix(text, cfg.Model)}}
		icons := map[int]*n.SVG{0: harnessIcon(descriptor)}
		at := 1 + utf8.RuneCountInString(text)
		if cfg.Plan {
			icons[at] = planModeIcon
			spans = append(spans, inlineIconSpan(text, font, 14, 6))
			at++
		}
		if cfg.Fast {
			icons[at] = fastModeIcon
			spans = append(spans, inlineIconSpan(text, font, 14, 6))
		}
		n.RichText(c, spans...).Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).SingleLine().TextColor(t.TextMuted).Label(summary).DrawObjects(func(p *n.Painter, at int, r n.Rect) {
			if at == 0 {
				r.W = 20
			} else {
				r.X += 6
				r.W = 14
			}
			if icon := icons[at]; icon != nil {
				p.Icon(icon, r, t.TextMuted)
			}
		})
	})
	if trigger.Clicked() {
		if v.runMenu == runMenuClosed {
			v.runMenu = runMenuMain
			if missing {
				v.runMenu = runMenuModels
			}
		} else {
			v.runMenu = runMenuClosed
		}
	}
	open := v.runMenu != runMenuClosed
	popover(c.Key("run-panel:"+key), trigger, &open, func(panel n.Element) {
		phase := v.runMenu
		depth := 1
		if phase == runMenuMain {
			depth = 0
		}
		panel.AttachTo(trigger, n.AnchorTopEnd, n.AnchorBottomEnd)
		panel.Label("运行配置菜单").Width(240).Padding(4).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			configurationTabPanel(c.Key("run-menu-pages"), fmt.Sprint(phase), depth, configurationPanelFit, func() {
				if phase != runMenuMain {
					if ghost(c, "‹  返回").FillWidth().TextAlign(n.Start).Clicked() {
						v.runMenu = runMenuMain
					}
					n.Scroll(c).MaxHeight(300).FillWidth().Children(func() {
						if phase == runMenuHarnesses {
							for _, info := range v.Model.Readiness.Harnesses {
								if ghost(c, info.Name).FillWidth().TextAlign(n.Start).Label(info.Name).Clicked() {
									if err := v.Model.SelectHarness(info.ID); err != nil {
										v.Model.Error = err.Error()
									}
									v.runMenu = runMenuMain
								}
							}
						} else if phase == runMenuModels {
							for _, model := range v.Model.ModelsForMenu(v.Model.ConnectionDeviceID(v.Model.ConnectionID()), ready.HarnessID, ready.Models) {
								label := model.Name
								if label == "" {
									label = model.ID
								}
								if ghost(c, label).FillWidth().TextAlign(n.Start).Tooltip(model.ID).Disabled(ready.State != domain.RuntimeReady).Clicked() {
									cfg.Model = model.ID
									if v.Model.Error == app.ModelUnavailableMessage {
										v.Model.Error = ""
									}
									v.Model.SetConfig(key, cfg)
									v.Model.Config()
									v.runMenu = runMenuMain
								}
							}
						} else {
							for _, model := range ready.Models {
								if model.ID == cfg.Model {
									for _, effort := range model.Efforts {
										if ghost(c, effort).FillWidth().TextAlign(n.Start).Disabled(ready.State != domain.RuntimeReady).Clicked() {
											cfg.Effort = effort
											v.Model.SetConfig(key, cfg)
											v.runMenu = runMenuMain
										}
									}
								}
							}
						}
					})
					return
				}
				if v.Model.SessionID == "" && len(v.Model.Readiness.Harnesses) > 1 {
					if settingRow(c, "Agent", descriptor.Name+" ›").Disabled(v.Model.HasPendingInput()).Clicked() {
						v.runMenu = runMenuHarnesses
					}
				} else {
					n.Row(c).Height(28).PaddingX(8).AlignItems(n.Center).Children(func() { n.Text(c, "Agent").Grow(1); n.Text(c, descriptor.Name) })
				}
				if settingRow(c, "模型", cfg.Model+" ›").Clicked() {
					v.runMenu = runMenuModels
				}
				effortsAvailable := false
				for _, model := range ready.Models {
					if model.ID == cfg.Model {
						effortsAvailable = len(model.Efforts) > 0
					}
				}
				if effortsAvailable {
					if settingRow(c, "推理强度", cfg.Effort+" ›").Clicked() {
						v.runMenu = runMenuEfforts
					}
				}

				fastAvailable := false
				for _, model := range ready.Models {
					if model.ID == cfg.Model {
						fastAvailable = model.Fast
					}
				}
				if ready.Plan || fastAvailable {
					n.Column(c).Height(1).FillWidth().Background(t.Border).MarginY(4)
				}
				if ready.Plan {
					plan := cfg.Plan
					if runOptionToggle(c, "Plan", planModeIcon, &plan).Changed() {
						cfg.Plan = plan
						v.Model.SetConfig(key, cfg)
					}
				}
				if fastAvailable {
					fast := cfg.Fast
					if runOptionToggle(c, "Fast", fastModeIcon, &fast).Changed() {
						cfg.Fast = fast
						v.Model.SetConfig(key, cfg)
					}
				}
			})
		})
	})
	if !open {
		v.runMenu = runMenuClosed
	}
}
func settingRow(c *n.Context, name, value string) n.Element {
	return control(c, 5).FillWidth().Height(28).PaddingX(8).Radius(5).Label(name).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			n.Text(c, name)
			n.Spacer(c).Grow(1)
			n.Text(c, value).SingleLine().MaxWidth(160).Tooltip(value)
		})
	})
}
func (v *View) conversation(c *n.Context, t *n.Theme) {
	m := v.Model
	v.syncPagedHistory(c)
	v.refreshConversationFind()
	// An empty detail keeps the dock mounted through first-turn hydration.
	// Its zero Session.ID also keeps the history-fold barrier unconsumed.
	detail := v.homeSendDetail(m.Details[m.SessionID])
	viewKey := v.homeSendKey(m.SessionID)
	state := v.lists[m.SessionID]
	if h := v.homeSend; h != nil {
		if h.list == nil {
			h.list = &n.ListState{FollowEnd: true}
		}
		state = h.list
	}
	if state == nil {
		state = &n.ListState{FollowEnd: true}
	}
	if m.SessionID != "" {
		v.lists[m.SessionID] = state
	}
	// Follow measured layout, including the reveal tail after execution ends.
	// ListState owns interruption/reading anchors and stops itself when settled;
	// authoritative turn completion must not cut off an in-progress glide.
	reopening := m.FoldToolHistory[m.SessionID]
	if !v.reduceMotion && !reopening && v.historyExit == nil {
		state.FollowAnimation = &conversationFollowAnimation
	} else {
		state.FollowAnimation = nil
	}
	rows := v.transcriptRows(detail.Items)
	rows = v.contextRows(rows)
	rows = v.revealFindTarget(rows, reopening)
	v.syncRunningGroup(c, rows, detail)
	turns := make(map[string]domain.Turn, len(detail.Turns))
	for _, turn := range detail.Turns {
		turns[turn.ID] = turn
	}
	rows = withTurnFooters(rows, turns)
	for i := range rows {
		if rows[i].footer != "" {
			if motion := v.toolArrivals.motions[rows[i].key]; motion != nil && !v.reduceMotion {
				rows[i].opacity, _ = motion.sample(v.frameTime)
			}
		}
	}
	rows = v.historyBridgeRows(rows)
	rows = v.historyRunningRows(rows)
	rows = v.homeSendRows(rows)
	v.positionHistoryTarget(state, rows)
	if v.toolsAnimating {
		c.AnimationFrame()
	}
	state.Reveals = state.Reveals[:0]
	if !v.reduceMotion {
		for i, row := range rows {
			if row.opacity >= 1 || row.restarting || v.historySuffixRow(row) {
				continue
			}
			owner := row.toolParent
			if owner == "" {
				owner = row.group
			}
			if owner == "" {
				owner = row.key
			}
			previousOwner := ""
			if i > 0 {
				previousOwner = rows[i-1].toolParent
				if previousOwner == "" {
					previousOwner = rows[i-1].group
				}
			}
			if len(state.Reveals) > 0 && owner == previousOwner && rows[i-1].opacity == row.opacity {
				state.Reveals[len(state.Reveals)-1].End = i + 2
			} else {
				reveal := n.ListReveal{First: i + 1, End: i + 2, Progress: row.opacity}
				if row.toolParent != "" && v.toolMotions[owner] != nil {
					// Animate the visible prefix rather than racing through the
					// full natural height of thousands of virtualized calls.
					reveal.MaxHeight = state.Bounds(c).H
				}
				state.Reveals = append(state.Reveals, reveal)
			}
		}
	}
	v.historySuffixReveals(c, state, rows)
	historyEndRow := len(rows) + 1
	hasNewer := detail.History != nil && detail.History.Newer
	if hasNewer {
		historyEndRow++
	}
	state.Key = func(i int) any {
		if i == 0 {
			return "history-top"
		}
		if i == historyEndRow {
			return "history-bottom"
		}
		if hasNewer && i == len(rows)+1 {
			return "history-newer"
		}
		row := rows[i-1]
		if key := v.toolArrivals.footerKeys[row.footer]; row.footer != "" && key != "" {
			return key
		}
		return row.key
	}
	v.anchorHistoryRows(state, rows)
	v.anchorSentMessage(c, state, rows)
	v.positionFind(c, state, rows)
	reserve := v.dockHeight
	if reserve == 0 {
		reserve = 108
	}
	state.FollowInset = reserve
	rowBounds := map[string]n.Rect{}
	history := n.List(c.Key("history:"+viewKey), state, historyEndRow+1, func(row int) {
		if row == 0 {
			height := float32(80)
			if v.hasHistoryPageControl() {
				height = 48 + 20 + 8
			}
			n.Column(c).Height(height).Padding(48, 18, 0, 18).AlignItems(n.Center).Children(func() { v.historyPageControl(c, t, false) })
			return
		}
		if row == historyEndRow {
			// Message rows already own the clearance above the dock.
			n.Column(c).Height(reserve)
			return
		}
		if hasNewer && row == len(rows)+1 {
			n.Column(c).FillWidth().Height(20 + 8).AlignItems(n.Center).Children(func() { v.historyPageControl(c, t, true) })
			return
		}
		entry := rows[row-1]
		if entry.toolTitle && entry.opacity == 0 {
			n.Column(c).Height(0)
			return
		}
		v.currentTranscriptRow = row
		i := entry.end
		item := entry.item
		bottom := float32(8)
		if i >= 0 && (i == len(detail.Items)-1 || (item.TurnID != "" && detail.Items[i+1].TurnID != item.TurnID)) {
			bottom = 32
			if _, ok := turns[item.TurnID]; ok {
				bottom = 8
			}
		}
		// Receipt spacing follows the rendered next row, including the local
		// waiting receipt before the accepted input receives its turn identity.
		if row < len(rows) && (rows[row].footer != "" || rows[row].restarting || rows[row].preparing != "") {
			bottom = 8
		}
		// Local compaction rows already occupy their place while the command
		// and source journal events catch up. Keep the same inter-item spacing.
		if row < len(rows) && (rows[row].item.Presentation == "compaction" || rows[row].item.Kind == "command") && rows[row].end < 0 {
			bottom = 8
		}
		// Metadata uses a smaller line box than prose. Match the visible
		// paragraph gap rather than reusing only its outer 8 DIP padding.
		if item.Kind == "assistant" && row < len(rows) {
			next := rows[row]
			if next.footer != "" || next.restarting || next.preparing != "" || next.group != "" || next.item.Kind == "request" {
				bottom = v.bodyMetadataGap()
			}
		}
		// Waiting is the first output slot. Keep the same user-to-output gap
		// when the status line gives way to real prose, tools or a request.
		if item.Kind == "user" || item.Kind == "command" {
			bottom = 32
		}
		if entry.footer != "" || entry.restarting || entry.preparing != "" {
			bottom = 32
		}
		// A user message begins a new reading block even when steering keeps
		// the protocol turn identity. Match ordinary turn-to-user spacing.
		if row < len(rows) && rows[row].item.Kind == "user" {
			bottom = 32
		}
		if hasNewer && row == len(rows) {
			bottom = 8
		}
		n.Column(c).FillWidth().PaddingX(18).AlignItems(n.Center).Children(func() {
			content := n.Column(c).FillWidth().MinWidth(0).MaxWidth(768).Gap(8).Padding(0, 0, bottom, 0).ClipX().Selectable()
			if entry.footer == "" && entry.group == "" && (item.Kind == "user" || item.Kind == "assistant" || item.Kind == "command") {
				n.SelectionItem(content, item.ID)
			}
			v.sentMessagePose(c, content, item)
			if entry.toolParent != "" && !entry.toolTitle {
				content.Padding(0, 0, bottom, 24).Draw(func(p *n.Painter, r n.Rect) {
					height := r.H
					if row == len(rows) || rows[row].toolParent != entry.toolParent {
						height = 10
					}
					p.Fill(n.Rect{X: r.X + 7.5, Y: r.Y, W: 1, H: height}, t.Border, 0)
				})
				content.Opacity(entry.opacity)
				if !v.reduceMotion {
					content.Transform(n.Translation(0, -4*(1-entry.opacity)))
				}
			}
			if entry.group != "" || entry.toolTitle || entry.toolParent == "" && !entry.restarting && entry.opacity < 1 {
				content.Opacity(entry.opacity)
			}
			content.DrawOver(func(_ *n.Painter, r n.Rect) { rowBounds[entry.key] = r })
			v.historySuffixPose(c, content, entry)
			content.Children(func() {
				if entry.preparing != "" {
					v.homeSendStatus(c, t, entry.preparing)
				} else if entry.restarting {
					v.turnFooter(c, t, domain.Turn{Status: "running"})
				} else if entry.footer != "" {
					v.turnFooter(c, t, turns[entry.footer])
				} else if entry.toolTitle {
					v.toolGroupTitle(c, t, entry, row)
				} else if entry.group != "" {
					v.toolGroup(c, t, entry, row)
				} else if entry.toolParent != "" {
					v.toolTimelineItem(c, t, entry)
				} else {
					v.item(c, t, item)
				}
			})
		})
	}).Absolute().Left(0).Right(0).Top(0).Bottom(0).ScrollbarInsets(8, 0, 8, 0).Label("对话记录")
	history.DrawOver(func(_ *n.Painter, _ n.Rect) {
		v.historyRowBounds = rowBounds
	})
	history.HandleInput(v.historyInput)
	n.VirtualTextSelection(history, func(key string) (string, bool) {
		for _, item := range v.Model.Details[v.Model.SessionID].Items {
			if item.ID == key {
				if item.Kind == "user" || item.Kind == "command" {
					return v.messageCopyText(item), true
				}
				return item.Text, true
			}
		}
		return "", false
	})
	v.conversationOutline(c, t, state, rows)
	v.prefetchHistory(state, rows)
	n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(48).Padding(0, conversationScrollbarGutter, 0, 0).Label("会话顶栏").Draw(func(p *n.Painter, r n.Rect) {
		// History is clipped to its reading column, so the gutter contains
		// only the pane background and native thumb, never overflowing text.
		p.Fill(n.Rect{X: r.X, Y: r.Y, W: max(0, r.W-conversationScrollbarGutter), H: r.H}, t.Background, 0)
	}).Children(func() { v.toolbar(c, t) })
	v.conversationFindBar(c, t, state.Bounds(c).W)
	dock := n.Column(c.Key("conversation-dock")).Absolute().Left(0).Right(0).Bottom(8).PaddingX(18).AlignItems(n.Center).Gap(8).Label("会话底部面板")
	dock.Draw(func(p *n.Painter, r n.Rect) {
		// Cards paint their own opaque rounded surfaces; the composer also
		// covers its lower corners. Continue that cover to the pane bottom,
		// leaving the native scrollbar gutter uncovered.
		p.Fill(n.Rect{X: r.X, Y: r.Y + r.H, W: max(0, r.W-conversationScrollbarGutter), H: 8}, t.Background, 0)
	})
	// Native wrapping and question disclosure determine the complete dock height.
	invalidate := c.Services().Invalidate
	dock.DrawOver(func(_ *n.Painter, r n.Rect) {
		if math.Abs(float64(v.dockHeight-r.H)) > .5 {
			v.dockHeight = r.H
			invalidate()
		}
	})
	dock.Children(func() { v.recovery(c, t, detail); v.pending(c, t, detail); v.composer(c, t, false); v.notice(c, t) })
	v.returnToLatest(c, t, state, dock)
}

func (v *View) item(c *n.Context, t *n.Theme, item domain.Item) {
	switch item.Kind {
	case "user", "command":
		v.historyUserMessage(c, t, item)
	case "assistant":
		v.assistantReply(c, item)
	case "tool":
		if !commandItem(item) {
			v.activityItem(c, t, item)
			return
		}
		n.Column(c).FillWidth().Children(func() {
			label := item.Name
			if label == "" {
				label = "工具活动"
			}
			heading := control(c).FillWidth().MinHeight(20).Justify(n.Start).Label(label).Tooltip(label).ContextMenu(commandCopyMenu(c, item.Name))
			if heading.Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
					n.Text(c, commandPreview(label)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).Font("Menlo").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1).MinWidth(0).MaxLines(2).Ellipsis("…")
					if issue := toolIssueLabel(item); issue != "" {
						n.Text(c.Key("command-issue"), issue).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).
							Transition(fadeMotion(c, 160*time.Millisecond))
					}
				})
			}).Clicked() {
				key := v.commandOutputKey(item.ID, "")
				v.Model.Collapsed[key] = !v.Model.Collapsed[key]
			}
			v.commandOutput(c, t, item, heading)
		})
	case "activity":
		v.activityItem(c, t, item)
	case "request_result":
		v.requestResult(c, t, item)
	case "request":
		v.requestSource(c, t, item, false)
	default:
		n.Text(c, item.Text).TextColor(t.TextMuted).FontSize(12).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
	}
}
func (v *View) turnFooter(c *n.Context, t *n.Theme, turn domain.Turn) {
	frame := n.Column(c.Key("reply-footer")).Label("回复信息").FillWidth().MinHeight(24).Clip()
	state := string(turn.Status)
	if (turn.Status == "running" || turn.Status == "starting") && !v.Model.Connected {
		state = "disconnected"
	}
	activity := v.runningActivity(turn)
	delegated := turn.Status.Active() && (v.runningGroup != "" && v.activityTurn == turn.ID || v.Model.Connected && activity.compaction != "")
	waiting := turn.Status.Active() && v.Model.Connected && activity.waiting
	if turn.Status == "running" || turn.Status == "starting" {
		frame.MinHeight(0)
	}
	if state == "starting" {
		state = "running"
	}
	// A tool-to-prose handoff already has its final footer height. A second
	// entrance/height animation would clip the new dot and move it on screen.
	if p := v.runningProgress; activity.compaction == "" && !waiting && state == "running" && p != nil && p.turn == turn.ID && p.handoff != nil {
		frame.Height(24).Children(func() { v.runningStatus(c, t, turn) })
		return
	}
	animatedContentHeight(c, frame, !delegated && !waiting, 180*time.Millisecond, func() float32 {
		row := n.Row(c.Key("state:" + state)).FillWidth().Wrap().Gap(messageFooterGap).MinHeight(24).AlignItems(n.Baseline).Transition(fadeMotion(c, 180*time.Millisecond))

		row.Children(func() {
			if delegated || waiting {
				return
			}
			if state == "disconnected" {
				n.Text(c, "连接已断开，执行状态待核对").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				return
			}
			label := string(turn.Status)
			switch turn.Status {
			case "running":
				label = "进行中"
			case "interrupted":
				label = "已停止"
			case "failed":
				label = "本轮失败"
			case "abandoned":
				label = "已手动结束 · 结果未确认"
			case "unknown":
				label = "执行状态待核实"
			}
			if turn.Status == "completed" {
				copy := n.ButtonBase(c.Key("copy-reply:"+turn.ID)).Size(messageFooterActionWidth, 24).Justify(n.Start).Label("复制回复")
				copiedUntil := n.Local(copy, "copied-until", func() time.Time { return time.Time{} })
				if copy.Clicked() {
					var parts []string
					for _, item := range v.Model.Details[v.Model.SessionID].Items {
						if item.TurnID == turn.ID && item.Kind == "assistant" && strings.TrimSpace(item.Text) != "" {
							parts = append(parts, item.Text)
						}
					}
					if len(parts) > 0 {
						c.WriteClipboard(strings.Join(parts, "\n\n"))
						*copiedUntil = c.Now().Add(2 * time.Second)
					}
				}
				glyph, glyphKey, tooltip := copyReplyIcon, "copy-icon", "复制回复"
				if c.Now().Before(*copiedUntil) {
					glyph, glyphKey, tooltip = check, "copied-icon", "已复制"
					c.After(copiedUntil.Sub(c.Now()))
				}
				copy.Tooltip(tooltip)
				color := textHoverColor(c, copy, t.TextMuted)
				copy.Children(func() {
					inlineSymbol(c.Key(glyphKey), glyph, "回复", n.Font{Family: c.Theme().Font, Size: 11, Weight: 400}, 12, color).
						Label(tooltip).Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 100*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
				})
				fork := n.ButtonBase(c.Key("fork:"+turn.ID)).Size(messageFooterActionWidth, 24).Justify(n.Start).Label("从这里分叉").Tooltip("从这里分叉").Disabled(!v.historyReady(v.Model.Details[v.Model.SessionID].Session) || !v.Model.RunReadiness().Capabilities.Fork || v.historyBusy[v.Model.SessionID])
				forkColor := textHoverColor(c, fork, t.TextMuted)
				fork.Children(func() {
					inlineSymbol(c, forkIcon, "回复", n.Font{Family: t.Font, Size: 11, Weight: 400}, 13, forkColor)
				})
				if fork.Clicked() {
					v.forkSession(v.Model.Details[v.Model.SessionID].Session, turn.ID)
				}
				ended, exact := "—", "结束时间未知"
				if turn.CompletedAt != nil {
					local := turn.CompletedAt.Local()
					ended = local.Format("15:04")
					exact = local.Format("2006-01-02 15:04:05")
				}
				n.Text(c, ended).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)}).Label("结束时间：" + ended).Tooltip(exact).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				duration := "用时 —"
				if turn.DurationMS != nil {
					seconds := *turn.DurationMS / 1000
					duration = fmt.Sprintf("用时 %d分%d秒", seconds/60, seconds%60)
				}
				n.Text(c, duration).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)}).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			} else if turn.Status == "running" || turn.Status == "starting" {
				v.runningStatus(c, t, turn)
			} else {
				animation := motionAnimation(c, 160*time.Millisecond)
				n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: &animation})
				if turn.Status == domain.ExecutionFailed && turn.Error != "" {
					n.Text(c, turn.Error).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine().MaxWidth(480).Tooltip(turn.Error).
						Transition(fadeMotion(c, 160*time.Millisecond))
				}
			}
			if turn.Status != "completed" && !turn.Status.Active() && turn.DurationMS != nil {
				n.Text(c, fmt.Sprintf("%.1f 秒", float64(*turn.DurationMS)/1000)).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			if turn.Status == "completed" {
				n.Column(c).MinHeight(24).Justify(n.Center).Label("本轮用量").Children(func() {
					n.Text(c, fmt.Sprintf("输入 %s · 输出 %s · 缓存 %s", count(turn.InputTokens), count(turn.OutputTokens), count(turn.CachedInputTokens))).
						FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).
						ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)})
				})
			}
		})
		return 0
	})
}

func count(value *int64) string {
	if value == nil {
		return "—"
	}
	if *value > 1000 {
		return fmt.Sprintf("%.1fk", float64(*value)/1000)
	}
	return fmt.Sprint(*value)
}
func (v *View) pendingCount(session string) int {
	count := 0
	for _, r := range v.Model.Details[session].Requests {
		if r.State.AwaitingResolution() {
			count++
		}
	}
	return count
}

const requestRadius = 8

func (v *View) respond(r domain.PendingRequest, answer domain.Answer) {
	if v.Controller == nil || !v.Model.Connected || r.State != "pending" {
		return
	}
	state := v.requestAction(r)
	if state.busy {
		return
	}
	state.busy, state.err = true, ""
	if !v.do(app.Job{Method: "request.respond", Input: protocol.AnswerInput{SessionID: r.SessionID, Answer: answer}, Apply: func(_ *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			state.busy = false
			state.err = err.Error()
		}
	}}) {
		state.busy = false
	}
}
func (v *View) submit() { v.sendMessage(false) }
func (v *View) sendMessage(steer bool) {
	if v.parent != nil && v.parent.Model.SecondarySessionID != v.Model.SessionID {
		return
	}
	if v.tryContextCommand() {
		return
	}
	if a := v.contextAction(); a != nil && a.State.Active() {
		if !v.Model.Active() {
			return
		}
		steer = false
	}
	if v.Model.HasPendingInput() || v.homeSendPending() {
		return
	}
	if v.preparingAttachments(v.Model.DraftKey()) {
		v.Model.Error = "正在准备附件，请稍候"
		return
	}
	if v.Model.QueueEdits[v.Model.SessionID].ID != "" {
		v.saveQueued()
		return
	}
	key, _ := v.gitQuery()
	if v.gitLoading(key) && !v.Model.Active() && len(v.Model.Queue().Items) == 0 {
		v.Model.Error = "正在核对设备上的分支，请稍后提交"
		return
	}
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, method, turn, err := m.BeginMessage(steer)
	if err != nil {
		m.Error = err.Error()
		return
	}
	if method != "session.submit" {
		v.submitActive(in, method, turn)
		return
	}
	sent := v.beginSendMotion(in)
	if in.SessionID == "" {
		v.homeSend = &homeSendPresentation{in: in}
	}
	var prepare func() error
	if v.Saver != nil {
		state, _ := v.persistenceModel().EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Prepare: prepare, Method: "session.submit", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			m.SubmissionError(in, err)
			if h := v.homeSend; h != nil && h.in.OperationID == in.OperationID && v.homeSendPending() {
				h.state = domain.OperationIndeterminate
			} else if v.sent == sent {
				v.sent = nil
			}
			return
		}

		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil {
			if v.sent == sent {
				v.sent = nil
			}
			m.Error = err.Error()
			return
		}
		m.Accepted(in, op)
		v.acceptHomeSend(m, op)
		if op.State == "rejected" || op.State == "indeterminate" {
			if v.sent == sent {
				v.sent = nil
			}
			return
		}
		if v.sent == sent {
			sent.session = op.SessionID
			if sent.origin == "" && op.SessionID != "" && v.homeSend == nil {
				v.sessionEntry = &sessionEntryMotion{session: op.SessionID, operation: in.OperationID, workspace: in.WorkspaceID, epoch: sent.epoch}
			}
		}
	}}) {
		if v.sent == sent {
			v.sent = nil
		}
		m.Reject(in, "操作繁忙，请重试")
	}
}
func (v *View) interrupt() {
	if !v.Model.RunReadiness().Capabilities.Interrupt {
		return
	}
	if v.Controller == nil {
		return
	}
	d := v.Model.Details[v.Model.SessionID]
	for _, turn := range d.Turns {
		if turn.Status == "running" {
			v.do(app.Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": d.Session.ID, "turnId": turn.ID}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
				if err != nil {
					m.Error = err.Error()
				}
			}})
			return
		}
	}
}
func (v *View) createWorkspace() { v.beginWorkspace(v.Model.ConnectionID()) }

func (v *View) nextAttention() {
	if s, ok := v.attentionSession(); ok {
		v.navigateSession(s.ID, s.WorkspaceID)
		v.collapsedWorkspaces[s.WorkspaceID] = false
		v.Model.Collapsed[s.ID] = false
		v.devices = false
	}
}
func (v *View) do(job app.Job) bool {
	if v.parent != nil && v.parent.Model.SecondarySessionID != v.Model.SessionID {
		return false
	}
	if v.Controller == nil {
		return false
	}
	if job.ConnectionID == "" {
		job.ConnectionID = v.Model.ConnectionID()
	}
	job.ConnectionEpoch = v.Model.ConnectionEpoch
	if state := v.Model.DeviceStates[job.ConnectionID]; state != nil {
		job.ConnectionEpoch = state.ConnectionEpoch
	}
	if v.parent != nil && job.Apply != nil {
		apply := job.Apply
		session := v.Model.SessionID
		job.Apply = func(model *app.Model, body json.RawMessage, err error) {
			previous := model.Error
			model.Error = ""
			apply(model, body, err)
			if v.Model.SessionID == session {
				v.Model.Error = model.Error
			}
			model.Error = previous
		}
	}
	return v.Controller.Do(job)
}

func textBinding(bindings map[string]*editBinding, key, value string) *editBinding {
	b := bindings[key]
	if b == nil {
		b = &editBinding{Text: value, LastModel: value}
		bindings[key] = b
	} else if value != b.LastModel {
		b.Text = value
		b.LastModel = value
	}
	return b
}

func raised(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#2d2d2d")
	}
	return n.Hex("#ffffff")
}
func questionBodyHeight(c *n.Context) float32 { _, h := c.Size(); return min(240, h*.28) }
