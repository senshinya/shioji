// Package ui renders Shioji with MyGo's native components; no WebView is used.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"math"
	"slices"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/conversationui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type editBinding struct {
	Text      string
	LastModel string
}

type View struct {
	goalActions                      map[app.WorkKey]bool
	schedules                        *schedulePageState
	remoteUploadJobs                 map[string]*remoteUploadJob
	remoteUploadRemember             map[string]bool
	workerUpdateConfirm              *workerUpdateConfirmation
	interfaceZoomToast               bool
	imagePreviewScroll               func(n.InputEvent) bool
	globalSidebar                    *quickSidebar
	portForward                      *portForwardDraft
	devicePower                      map[string]*devicePowerUI
	backgroundChanges                map[string]backgroundChange
	feedback                         *conversationFeedback
	feedbackScope                    feedbackSource
	feedbackSelection                int
	feedbackDetail, feedbackOriginal bool
	feedbackStatusID                 string
	feedbackPinnedID                 string
	feedbackStatuses                 []conversationStatus
	feedbackGitWait                  string
	feedbackObservedError            string
	submissionCheckError             map[string]string
	draftSaveError                   string
	historyPage                      *conversationHistory
	sessionLoad                      *sessionLoading
	sessionStops                     map[string]*sessionStop
	historyBaseline                  bool
	historyTarget                    string
	workSourceTarget                 app.WorkKey
	deviceConnectionSave             *deviceConnectionSave
	pathNotice                       devicePathNotice
	sshExpectedDevice                string
	tailnet                          *tailnetUI
	sshTailnetPeer                   string
	commentTagEntries                map[string]time.Time

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
	runningGroup, runningReasoning, activityTurn                                                                    string
	reasoningStreams                                                                                                map[string]*reasoningStreamEntry
	contextInputs                                                                                                   map[app.WorkKey]*contextInputUI
	contextRowKeys                                                                                                  map[app.WorkKey]string
	requestActionsState                                                                                             map[app.WorkKey]*requestActionState
	workPanels                                                                                                      map[app.WorkKey]*workPanelUI
	workspaceTools                                                                                                  map[string]*workspaceToolsUI
	hookManagement                                                                                                  map[string]*hookManagementUI
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
	sessionReloads                                                                                                  map[string]*sessionReloadUI
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
	visibleCompletions                                                                                              map[string]app.CompletedReply
	attachmentStore                                                                                                 *attachments.Repository
	attachmentCache                                                                                                 *app.AttachmentCache
	attachmentImages                                                                                                map[string]*attachmentImage
	markdownImages                                                                                                  *markdownImageCache
	attachmentUploading                                                                                             map[string]bool
	attachmentPreparing                                                                                             map[string]int
	attachmentPreview                                                                                               *attachmentPreview
	navVisibility                                                                                                   map[string]*navVisibility
	workspaceEditor                                                                                                 *workspaceEditor
	rename                                                                                                          *sessionRename
	archive                                                                                                         *archiveUI
	archivePolicyPending                                                                                            map[string]string
	archiveChanges                                                                                                  map[archiveChangeKey]*archiveChange
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
	previousSends                                                                                                   []presentedSend
	sent                                                                                                            *sentMessageMotion
	sessionEntry                                                                                                    *sessionEntryMotion
	composerHadFocus                                                                                                bool
	focusComposer                                                                                                   bool
	queueCommands                                                                                                   map[app.WorkKey]*queueCommandState
	queueDrag                                                                                                       *queuedRowDrag
	queueSources                                                                                                    map[string]queuedMessageSource
	queueSourceSession                                                                                              string
	queueSourceEpoch                                                                                                string
	queueArrivals                                                                                                   []*queuedArrivalMotion
	queueDisplay                                                                                                    domain.QueueState
	composerHeightKey                                                                                               string
	composerInputHeight                                                                                             float32
	composerNaturalHeight                                                                                           float32
	composerChromeHeight                                                                                            float32
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
	navReveal                                                                                                       string
	fileLink                                                                                                        *fileLinkState
	md                                                                                                              markdown.Options
	mermaid                                                                                                         *mermaidCache
	permissionOpen                                                                                                  bool
	runMenu                                                                                                         runMenuPhase
	devices                                                                                                         bool
	selected                                                                                                        string
	selectedDevice                                                                                                  string
	deviceSelected                                                                                                  string
	deviceList                                                                                                      bool
	deviceTab                                                                                                       string
	deviceProfile                                                                                                   *deviceProfileEditor
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
	workspaceCreation                                                                                               *workspaceCreation
	importCompletion                                                                                                *conversationImportCompletion
	collapsedWorkspaces                                                                                             map[string]bool
	dockHeight                                                                                                      float32
	searchOpen                                                                                                      bool
	searchInput                                                                                                     n.Handle
	searchText, searchSent                                                                                          string
	searchCursor                                                                                                    string
	searchRequest                                                                                                   uint64
	searchPending, searchFailed                                                                                     int
	searchProblem                                                                                                   string
	searchResults                                                                                                   []domain.SearchResult
	searchList                                                                                                      n.ListState
	branchOpen                                                                                                      bool
	branchSearch                                                                                                    string
	branchList                                                                                                      n.ListState
	branchCheckouts                                                                                                 map[string]*branchCheckout
	gitLoads                                                                                                        map[string]*gitLoadState
	resizingInspector                                                                                               bool
	inspectorFocus                                                                                                  inspectorTabDrag
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
		edit := v.Model.ActiveQueueEdit(strings.TrimPrefix(b.key, "session:"))
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
			v.Model.Answers["source:"+key] = "preset"
			b.LastModel = b.Text
		}
	}
	for key, b := range v.customBindings {
		if b.Text != b.LastModel {
			v.useCustomQuestionAnswer(key, b.Text)
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

const checkPath = "M4 10l4 4 8-9"

var check = svg(checkPath)
var attention = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var arrow = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M12 21V3M4 11l8-8 8 8" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var stop = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><rect x="6" y="5" width="12" height="14" rx="2" fill="currentColor"/></svg>`))

const terminalIconPath = "M4 6l4 4-4 4M11 14h5"

var terminalIcon = svg(terminalIconPath)

const questionIconPath = "M17.5 13.3h-10l-5 4.2v-15h15z"

var questionIcon = svg(questionIconPath)

// Native thumb: 6 px, 2 px wider while dragged, and 3 px from the edge.
const conversationScrollbarGutter = 11

func (v *View) View(c *n.Context) {
	m := v.Model
	if v.Controller != nil {
		if items, ready := v.Controller.ForwardSnapshot(); ready {
			m.PortForwards = items
		}
	}
	m.Preferences = m.Preferences.Normalized()
	v.syncInterfaceZoom(c)
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
	if v.devices || v.workspaceEditor != nil || m.SessionID == "" {
		v.sessionLoad = nil
	}
	v.beginWorkHistoryFrame()
	defer v.cancelHiddenWorkHistory()
	if p := v.historyPage; p != nil && (p.session != m.SessionID || p.epoch != m.ConnectionEpoch || v.devices) {
		v.cancelPagedHistory()
	}
	v.syncDeviceConnectionSave()
	m.RefreshDevices()
	v.syncArchiveChanges()
	v.syncSessionStops()
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
			// Position cached history before its first visible layout. Waiting
			// for the worker read exposes the previous visit's reading position.
			state.ScrollToEnd()
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
	v.syncDeviceProfile()
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
			v.draftSaveError = err.Error()
		}
		if err == nil {
			v.draftSaveError = ""
		}
		defer v.queueState()
	}
	if !m.FirstRun {
		v.syncConversationFeedback(c)
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
	if v.archive == nil || !v.archive.open {
		v.requestGit(false)
	}
	if c.Shortcut(n.Cmd, n.KeyK) {
		v.toggleSearch(c)
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
		if !v.nextAttention() {
			v.reportFeedback(app.Feedback{Title: "无待处理会话", Kind: app.FeedbackInformation})
		}
	}
	if c.Shortcut(n.Cmd|n.Shift, n.KeyH) {
		if v.archive != nil && v.archive.open {
			v.closeArchive()
		} else if v.previousSession != "" {
			v.navigateSession(v.previousSession, v.previousWorkspace)
		}
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
			duration = -1
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
		width, _ := c.Size()
		navigationWidth := navigationSidebarWidth(width)
		v.sidebar(c, &t, navigationWidth)
		if v.schedulePageOpen() {
			v.schedulePage(c, &t)
			return
		}
		if v.archive != nil && v.archive.open {
			v.archivePage(c, &t)
			return
		}
		owner = v.focusedView()
		open := owner.Model.SessionID != "" && !v.devices && v.workspaceEditor == nil && m.Inspectors[owner.Model.SessionID] && (!m.FocusSecondary || m.SecondarySessionID == owner.Model.SessionID)
		if !open {
			v.resizingInspector = false
		}
		available := width - navigationWidth
		limit := available - inspectorMinConversationWidth
		inline := limit >= inspectorMinWidth
		if !inline {
			limit = available - 16
		}
		panelWidth := min(max(m.InspectorWidth, inspectorMinWidth), min(limit, inspectorMaxWidth))
		conversationWidth := available
		if open && inline {
			conversationWidth -= panelWidth
		}
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
						v.toolbar(c, &t, false)
						v.workspaceSettingsView(c, &t)
					}
				}, false)
			} else if m.SessionID == "" && v.homeSend == nil {
				v.toolbar(c, &t, false)
				v.homeView(c, &t)
			} else {
				v.parallelConversation(c, &t, conversationWidth)
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
			state.ScrollToEnd()
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
	v.archiveDialogs(c, &t)
	v.portForwardDialog(c, &t)
	v.fileLinkDialog(c, &t)
	v.attachmentDialog(c, &t)
	v.directoryDialog(c, &t)
	v.conversationImportDialog(c, &t)
	v.devicePathFeedback(c, &t)
	v.conversationMessageOverlay(c, &t)
}

func (v *View) home() {
	v.closeSchedulePage()
	if a := v.splitRoot().archive; a != nil {
		a.open = false
		a.viewing = ""
	}
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
	v.focusComposer = true
	// An empty new-session composer starts from the owning checkout again.
	// Keep explicit branch intent while an unsent draft or receipt is pending.
	if !v.Model.HasDraftContent("home") && !v.Model.HasPendingInput() {
		key, _ := v.gitQuery()
		delete(v.Model.GitEpochs, key)
		if state := v.gitLoads[key]; state != nil {
			state.tag = ""
		}
	}
	v.Model.FocusSecondary = false
	v.devices = false
	v.runMenu = runMenuClosed
	v.permissionOpen = false
	v.branchOpen = false
	v.searchOpen = false
}
func iconButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return control(c).Size(toolbarActionSize, toolbarActionSize).Radius(6).Label(label).Tooltip(label).TextColor(c.Theme().TextMuted).Children(func() { n.Icon(c, icon).Size(16, 16) })
}
func ghost(c *n.Context, label string, radius ...float32) n.Element {
	r := c.Theme().Radius
	if len(radius) > 0 {
		r = radius[0]
	}
	return controlFeedback(c, n.Button(c, label).Radius(r), r).FontWeight(400).Justify(n.Start).Background(n.Transparent).Border(0, n.Transparent).Height(28)
}
func (v *View) toolbar(c *n.Context, t *n.Theme, switchPane bool) {
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
	n.Row(c).Height(mainToolbarHeight).FillWidth().Padding(mainWindowTopInset, rightInset, mainToolbarBottomInset, 24).Gap(6).AlignItems(n.Center).DragWindow().Label("会话工具栏").Children(func() {
		if schedules := v.splitRoot().schedules; schedules != nil && schedules.session != "" && schedules.session == m.SessionID {
			if iconButton(c, conversationBackIcon, "返回定时任务").Clicked() {
				v.splitRoot().openSchedulePage()
			}
		}
		if a := v.splitRoot().archive; a != nil && a.viewing == m.SessionID && m.SessionID != "" {
			if iconButton(c, conversationBackIcon, "返回已归档").Clicked() {
				v.openArchive()
			}
		}
		if v.devices {
			n.Text(c, "设备").FontSize(13).Grow(1)
			add := iconButton(c, plus, "添加设备")
			v.choiceMenu(c, t, "add-device", add, []menuChoice{{id: "ssh", name: "通过 SSH", icon: commandTerminalIcon, choose: func() { v.dismissDeviceSetup(); v.sshCheck = v.sshCheck.show() }}, {id: "tailnet", name: "通过 Tailnet", icon: tailscaleMark, disabled: true}})
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
						n.Text(c, s.Title).SingleLine().Grow(1).MinWidth(0).TooltipWhenTruncated(s.Title).FontSize(13).FontWeight(400).ContentTransition(n.ContentTransition{Animation: &titleAnimation})
					}
				}
			})
		} else {
			n.Spacer(c).Grow(1)
		}
		v.currentDeviceControl(c, t)
		if switchPane {
			v.parallelSwitch(c, v.parent == nil)
		}
		if v.parent == nil {
			v.feedbackButton(c, t)
		} else if iconButton(c, closeIcon, "关闭并排会话").Clicked() {
			v.closeParallel()
		}
		if m.SessionID != "" && !v.devices {
			label := "展开侧栏"
			if m.Inspectors[m.SessionID] {
				label = "收起侧栏"
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
	n.Column(c).Fill().PaddingX(18).AlignItems(n.Center).Children(func() {
		n.Spacer(c).Grow(.84)
		workspaces := v.Model.Snapshot.Workspaces
		if len(workspaces) == 0 && v.Controller != nil && v.currentConnectionPhase() != app.ConnectionIdle {
			if v.connectionBlocker() != "" {
				v.notice(c, t)
			} else {
				label := "正在读取工作区"
				n.Text(c, label).FontSize(13).TextColor(t.TextMuted)
			}
		} else if !slices.ContainsFunc(workspaces, func(w domain.Workspace) bool { return !w.Archived }) {
			title := "从一个工作区开始"
			hasArchived := len(workspaces) > 0
			if hasArchived {
				title = "工作区已全部归档"
			}
			n.Text(c, title).FontSize(28)
			n.Row(c).Margin(18, 0, 0, 0).Gap(8).AlignItems(n.Center).Children(func() {
				if compactDialogPrimary(c, "创建工作区").Disabled(v.Controller == nil).Clicked() {
					v.createWorkspace()
				}
				if hasArchived && configurationAction(c, "查看已归档").Clicked() {
					v.openArchive()
				}
			})
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
					choices = append(choices, menuChoice{id: w.ID, name: w.Name, detail: v.Model.DeviceName(w.DeviceID), checked: w.ID == v.Model.WorkspaceID, choose: func() { v.Model.WorkspaceID = w.ID; v.home() }})
				}

				v.choiceMenu(c, t, "home-workspace", picker, choices)
				n.Text(c, "做些什么？").FixedLineHeight(48).FontSize(32).TextColor(t.TextMuted)
			})
			n.Column(c.Key("home-composer")).FillWidth().MaxWidth(768).Children(func() {
				v.composer(c, t, true)
				// Status changes must not participate in the centered home layout.
				n.Column(c.Key("home-notice")).Absolute().Left(0).Right(0).TopPercent(100).Padding(8, 0, 0, 0).Children(func() {
					v.notice(c, t)
				})
			})
		}
		n.Spacer(c).Grow(1.16)
	})
}
func (v *View) notice(c *n.Context, t *n.Theme) {
	v.conversationStatusPanel(c, t)
}
func (v *View) composer(c *n.Context, t *n.Theme, home bool) (goalShown bool) {
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
	stopped := v.sessionStopInFlight(m.SessionID)
	active := m.Active()
	bg := t.Background
	if t.Dark {
		bg = t.Surface
	}
	inputHeight := float32(42)
	inputSize, inputLeading := float32(14), float32(22)
	if home {
		inputHeight = 58
		inputSize, inputLeading = 15, 26
	}
	stack := n.Column(c.Key("composer-stack:" + stackKey)).Label("消息编辑区").FillWidth().MaxWidth(768).Gap(0).Children(func() {
		v.remoteAttachmentsPanel(c, t, key)
		if !home && !v.loadingBlocksComposer() {
			goalShown = v.sessionGoal(c, t)
			notice := t.Surface
			if t.Dark {
				notice = codeSurface(t)
			}
			sid := m.SessionID
			limit, item := conversationui.ComposerWarning(m.Details[sid], m.UsageLimitForSession(sid, c.Now()), m.Readiness.HarnessKind(m.Details[sid].Session.HarnessID))
			var retry func(n.Rect)
			if (limit.ID != "" || item != "") && v.canRetryMessage(sid) {
				retry = func(button n.Rect) {
					current, currentItem := conversationui.ComposerWarning(m.Details[sid], m.UsageLimitForSession(sid, c.Now()), m.Readiness.HarnessKind(m.Details[sid].Session.HarnessID))
					if current.ID == limit.ID && currentItem == item {
						width, _ := c.MeasureText(0, n.Span{Text: app.RetryMessageText, Font: t.Font, Size: userMessageFontSize})
						source := conversationui.MessageFlightOrigin(button, width+2*userMessagePaddingX, userMessageLeading+2*userMessagePaddingY)
						v.retryComposerWarning(sid, source)
					}
				}
			}
			conversationui.ComposerNotice(c.Key("usage-limit"), limit, item, notice, t.Text, t.TextMuted, 1, 76, 8, 28, retry)
		}
		// The queue backplate extends behind the editor's rounded top edge.
		v.queueDisclosure(c, t)
		binding = v.composerDocument(c, key)

		editID := m.ActiveQueueEdit(m.SessionID).ID
		if v.composerHeightKey != stackKey {
			v.composerHeightKey, v.composerNaturalHeight, v.composerEditID = stackKey, 0, editID
			v.composerChromeHeight = 0
			v.composerHeightUntil = time.Time{}
			v.composerInputHeight = inputHeight
		}
		if v.composerEditID != editID {
			v.composerEditID = editID
			v.composerHeightUntil = c.Now().Add(360 * time.Millisecond)
		}
		heightFrame := n.Column(c.Key("composer-height:"+stackKey)).Label("消息编辑器").FillWidth().Clip().Background(bg).Border(1, t.Border).Radius(16).Shadow(0, 4, 14, 0, n.RGBA(0, 0, 0, .04))
		queueResize := c.Now().Before(v.composerHeightUntil) || editID != ""
		resize := queueResize || !c.Preferences().ReduceMotion
		heightAnimation := motionAnimation(c, 180*time.Millisecond)
		if queueResize {
			heightAnimation = n.Animation{Duration: 360 * time.Millisecond, Ease: composerDescentEase}
		}
		// The editor viewport supplies the same animated height to the dock
		// and to the text clip. The attachment strip animates its own height.
		shownInputHeight, shownChromeHeight := v.composerInputHeight, v.composerChromeHeight
		if s, h := v.sent, v.homeSend; s != nil && s.steerInputHeight > 0 && s.phase == sentAwaitingRow && s.session == m.SessionID &&
			h != nil && h.state != domain.OperationIndeterminate && m.Connected && !m.UncertainSubmissions[h.in.OperationID] && !c.Preferences().ReduceMotion {
			// Keep the previous viewport through the reply-footer handoff. If
			// the empty editor shrinks first, the transcript follows it down
			// before scrolling up again for the inserted message.
			shownInputHeight = max(shownInputHeight, s.steerInputHeight)
		}
		if v.composerNaturalHeight > 0 {
			if resize {
				if s := v.sent; s != nil && s.steer && !s.queued && s.session == m.SessionID && s.phase == sentAnimating && s.started.Equal(c.Now()) && !m.HasDraftContent(key) {
					// The list is measuring its end this frame. Give it the
					// empty editor's height before positioning the new input.
					shownInputHeight = inputHeight
					heightFrame.Jump("input-height", shownInputHeight)
				} else {
					shownInputHeight = heightFrame.AnimateTo("input-height", shownInputHeight, heightAnimation)
				}
				shownChromeHeight = heightFrame.AnimateTo("chrome-height", shownChromeHeight, heightAnimation)
			} else {
				heightFrame.Jump("input-height", shownInputHeight)
				heightFrame.Jump("chrome-height", shownChromeHeight)
			}
		}
		attachmentHeight := float32(0)
		if !home {
			v.loadingComposerFrame(heightFrame)
		}
		heightFrame.Children(func() {
			var input composerInput
			composer := n.Column(c.Key("composer:"+stackKey)).FillWidth().Padding(10, 14, 8, 6).Gap(4)
			v.loadingComposerContent(composer, home)
			composer.Children(func() {
				if m.ActiveQueueEdit(m.SessionID).ID != "" {
					n.Column(c).Height(28)
				}
				binding = v.composerDocument(c, key)
				v.branchStrip(c, t, home)
				v.contextCommandNotice(c, t)
				if !waiting {
					attachmentHeight = v.draftAttachmentStrip(c, t)
				}
				n.Column(c.Key("composer-input-space")).Height(shownInputHeight).Shrink(0)
				// A zero-height origin fixed above the footer keeps outgoing editor
				// snapshots still while the composer changes its natural height.
				n.Column(c.Key("composer-input-anchor")).Absolute().Left(0).Right(0).Bottom(40).Height(0).Children(func() {
					// Put the scrollbar in the right inset and wrap the text
					// between matching insets inside the border.
					const textInset = 15
					inputKey := "input"
					if editID != "" {
						inputKey += ":edit:" + editID
					}
					options := richtext.EditorOptions{
						Label: v.composerLabel(), Placeholder: "描述任务…",
						Font:       n.Font{Family: t.Font, Size: inputSize, Weight: 400},
						NoMarkdown: true, PlainText: true, MaxLines: 7, LineHeight: inputLeading,
						Theme: &richtext.Theme{Muted: subtle(t), Selection: editorSelection(t), Padding: [2]float32{textInset, 0}},
					}
					options.ReadOnly = waiting || !home && v.loadingBlocksComposer()
					options.OnKey = func(keyCode n.Key, mods n.Modifiers) bool {
						k := composerKey{Key: keyCode, Mods: mods, binding: binding}
						v.publishComposer(key, binding)
						if v.mentionKey(k) {
							return true
						}
						if keyCode == n.KeyEscape && mods == 0 && editID != "" && v.pendingQueueCommand(m.SessionID) == nil && !binding.doc.Composing() {
							v.cancelQueuedEdit()
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
						options.Theme.Muted = subtle(t).Alpha(s.progress(c))
					}
					// Keep existing lines at the top as the viewport grows or
					// shrinks. The editor still lays out at its natural height
					// so transient clipping does not trigger internal scrolling.
					n.Column(c.Key(inputKey)).Absolute().Left(0).Right(0).Bottom(0).Height(shownInputHeight).Clip().
						Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}).
						Children(func() {
							input = composerInput{Element: richtext.EditorBase(c.Key("editor"), binding.doc, options).FocusRing(false).Absolute().Left(0).Right(0).Top(0).MinHeight(inputHeight), binding: binding}
						})

					if v.focusComposer && (home || !v.loadingBlocksComposer()) {
						input.Focus()
						v.focusComposer = false
					}
					input.Changed()
					// The send animation starts from the text column.
					v.inputBounds = input.Bounds()
					v.inputBounds.X += textInset
					v.inputBounds.W = max(0, v.inputBounds.W-2*textInset)
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
					permission := control(c).Height(28).PaddingX(6).Radius(6).Label("权限").Children(func() {
						inlineIconText(c, shieldIcon, m.PermissionName(cfg.Permission), n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 13, 6, t.TextMuted)
					})
					choices := []menuChoice{}
					for _, id := range m.RunReadiness().Permissions {
						id := id
						detail := m.PermissionDescription(id)
						choices = append(choices, menuChoice{id: id, name: m.PermissionName(id), detail: detail, checked: cfg.Permission == id,
							disabled: v.persistenceModel().PermissionChangePending(m.SessionID), choose: func() {
								v.persistenceModel().SelectPermission(key, m.RunReadiness(), cfg, id, v.do)
							}})
					}
					v.choiceMenu(c, t, "permission:"+key, permission, choices)
					n.Spacer(c).Grow(1)
					if !home {
						v.contextMeter(c, t, cfg)
					}
					v.runControl(c, t, key, cfg)
					empty := strings.TrimSpace(draft) == "" && len(m.Attachments[key]) == 0 && len(m.DraftSkills[key]) == 0
					if binding.departedToQueue && !binding.departedEdited {
						empty = true
					}
					icon := arrow
					iconSize := float32(28.0 * 14 / 24)
					iconKey := "primary-send-icon"
					label := "发送"
					editing := m.ActiveQueueEdit(m.SessionID).ID != ""
					queuing := len(m.Queue().Items) > 0 || active && (!stopped || !empty)
					disabled := v.currentConnectionPhase() != app.ConnectionIdle || !m.Connected || m.RunReadiness().State != "ready" || m.ModelUnavailable() || m.ReadOnlyConversationReason() != "" || empty || m.HasPendingInput() || v.persistenceModel().PermissionChangePending(m.SessionID) || v.homeSendPending() || m.ArchivedContext() != "" || v.preparingAttachments(m.DraftKey())
					if queuing {
						label = "加入队列"
						disabled = disabled || m.WorkerMinor < 2
					}
					if editing {
						icon = check
						iconSize = 18
						label = "保存编辑"
						disabled = disabled || v.pendingQueueCommand(m.SessionID) != nil
					}
					if fields := strings.Fields(draft); !editing && len(fields) > 0 && fields[0] == "/compact" {
						label = "压缩上下文"
						disabled = v.compactReason() != ""
					}
					canStop := active && !stopped && empty && !editing && (!v.homeSendPending() || m.ActiveTurn() != "")
					if canStop {
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
						motion := n.ElementTransition{Duration: motionAnimation(c, 120*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}
						if canStop {
							motion.Exit = nil
						}
						if stopped {
							motion.Enter = nil
						}
						n.Icon(c.Key(iconKey), icon).Size(iconSize, iconSize).Transition(motion)
					})
					if reason := m.ReadOnlyConversationReason(); reason != "" {
						send.Tooltip(reason)
					}
					if send.Clicked() {
						if canStop {
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
				changed := false
				if input.Valid() {
					// Measure the live editor even when its viewport clips it
					// or its entry transition has zero opacity.
					bounds := input.LayoutBounds()
					if bounds.H > 0 && math.Abs(float64(v.composerInputHeight-bounds.H)) > .5 {
						v.composerInputHeight = bounds.H
						changed = true
					}
				}
				if v.composerHeightKey == stackKey {
					chrome := r.H + 2 - shownInputHeight - attachmentHeight
					v.composerNaturalHeight = chrome + v.composerInputHeight + attachmentHeight
					if math.Abs(float64(v.composerChromeHeight-chrome)) > .5 {
						v.composerChromeHeight = chrome
						changed = true
					}
					if changed {
						invalidate()
					}
				}
			})
			if m.ActiveQueueEdit(m.SessionID).ID != "" {
				// Keep expanding editor contents below the pinned edit controls.
				n.Column(c.Key("queue-edit-header-surface")).Absolute().Left(1).Right(1).Top(1).Height(37).Radius(15, 15, 0, 0).Background(bg).IgnoreInput(true)
			}
			v.queueEditBar(c, t)
			if !home {
				v.loadingComposerSkeleton(c, t)
			}
		})
		if v.composerNaturalHeight > 0 && resize {
			heightFrame.Height(shownChromeHeight + shownInputHeight + attachmentHeight)
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
	return goalShown
}
func (v *View) runControl(c *n.Context, t *n.Theme, key string, cfg domain.RunConfig) {
	ready := v.Model.RunReadiness()
	descriptor := ready.Descriptor()
	agent := runAgentCaption(ready)
	summary := cfg.Model
	if summary == "" {
		summary = "未选择模型"
	}
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
	tip := agent + " · " + summary
	description := tip
	if missing {
		tip = cfg.Model + " 已不可用，请更换模型"
		description += "，模型已不可用，请更换模型"
	} else if !v.Model.Connected {
		description += "，设备未连接"
	} else if ready.State != domain.RuntimeReady {
		reason := "运行配置暂不可用"
		if ready.Error != "" {
			reason = feedbackDetails(ready.Error)
		} else if ready.State == domain.RuntimeRefreshing {
			reason = "正在刷新运行配置"
		}
		description += "，" + reason
	}
	trigger := control(c).Height(28).MaxWidth(390).MinWidth(0).Shrink(1).PaddingX(6).Radius(6).Label("运行配置").Description(description).Tooltip(tip)
	modelColor := t.TextMuted
	if missing {
		modelColor = t.Danger
	}
	modelColor = trigger.AnimateColor("model-availability", modelColor, motionAnimation(c, 180*time.Millisecond))
	trigger.Children(func() {
		font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
		model := cfg.Model
		if model == "" {
			model = "选择模型"
		}
		text := model
		if cfg.Effort != "" {
			text += " · " + cfg.Effort
		}
		n.Row(c).MinWidth(0).Gap(6).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, harnessIcon(descriptor), text, font, 20, t.TextMuted).Shrink(0)
			n.RichText(c, n.Span{Text: model, Color: modelColor}, n.Span{Text: strings.TrimPrefix(text, model)}).
				MinWidth(0).Shrink(1).Font(font.Family).FontSize(font.Size).FixedLineHeight(20).SingleLine().
				Ellipsis("…").TextColor(t.TextMuted).Label(summary)
			if cfg.Plan {
				inlineSymbol(c, planModeIcon, text, font, 14, t.TextMuted).Shrink(0)
			}
			if cfg.Fast {
				inlineSymbol(c, fastModeIcon, text, font, 14, t.TextMuted).Shrink(0)
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
								choice := control(c.Key(info.ID), 6).FillWidth().Height(28).PaddingX(8).Label(info.Name).Children(func() {
									n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
										n.Text(c, info.Name).MinWidth(0).Shrink(1).SingleLine().TooltipWhenTruncated(info.Name)
										systemProfileBadge(c, info)
									})
								})
								if isSystemAgentProfile(info) {
									choice.Description(systemProfileHelp)
								}
								if choice.Clicked() {
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
								choice := ghost(c, label).FillWidth().TextAlign(n.Start).Disabled(ready.State != domain.RuntimeReady)
								if label != model.ID {
									choice.Tooltip(model.ID)
								}
								if choice.Clicked() {
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
					if settingRow(c, "Agent", agent+" ›").Disabled(v.Model.HasPendingInput()).Clicked() {
						v.runMenu = runMenuHarnesses
					}
				} else {
					n.Row(c).Height(28).PaddingX(8).AlignItems(n.Center).Children(func() {
						n.Text(c, "Agent").Grow(1)
						n.Text(c, agent).SingleLine().MaxWidth(160).TooltipWhenTruncated(agent)
					})
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
			n.Text(c, value).SingleLine().MaxWidth(160).TooltipWhenTruncated(value)
		})
	})
}
func (v *View) conversation(c *n.Context, t *n.Theme, switchPane bool) {
	m := v.Model
	v.syncPagedHistory(c)
	v.syncSessionLoading(c)
	v.refreshConversationFind()
	// An empty detail keeps the dock mounted through first-turn hydration.
	// Its zero Session.ID also keeps the history-fold barrier unconsumed.
	detail := v.homeSendDetail(v.stoppingReplyDetail(m.Details[m.SessionID]))
	detail.Items = v.projectQueuedSends(detail.Items)
	viewKey := v.homeSendKey(m.SessionID)
	state := v.lists[m.SessionID]
	if h := v.homeSend; h != nil && h.in.SessionID == "" {
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
	var heldSteer *domain.Item
	detail.Items, heldSteer = v.holdSteerMessage(c, detail)
	rows := v.transcriptRows(detail.Items)
	rows = v.revealFindTarget(rows, reopening)
	rows = v.contextRows(rows)
	v.syncRunningGroup(c, rows, detail)
	rows = slices.DeleteFunc(rows, func(row transcriptRow) bool {
		return row.toolStatus && row.toolParent != v.runningGroup && v.toolMotions[row.toolParent] == nil
	})
	turns := make(map[string]domain.Turn, len(detail.Turns))
	for _, turn := range detail.Turns {
		turns[turn.ID] = turn
	}
	if heldSteer != nil {
		// Project the worker's cutoff without mounting its message or the next
		// running footer in the same frame as the old footer's transition.
		rows = append(rows, transcriptRow{item: *heldSteer, end: len(detail.Items)})
	}
	rows = withTurnFooters(rows, turns)
	if heldSteer != nil {
		rows = slices.DeleteFunc(rows, func(row transcriptRow) bool {
			return row.item.ID == heldSteer.ID || row.footer == heldSteer.TurnID && row.replyBefore == nil
		})
	}
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
	rows = v.queuedSendStatusRows(rows)
	rows = withToolUserGaps(rows)
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
		if row.footer != "" {
			if row.footerAfter != "" {
				return "reply-after:" + row.footerAfter
			}
			if key := v.toolArrivals.footerKeys[row.footer]; key != "" {
				return key
			}
			return "turn-footer:" + row.footer
		}
		return row.key
	}
	state.SkipAnchor = func(i int) bool {
		return i == 0 || i == historyEndRow || hasNewer && i == len(rows)+1 || i > 0 && i <= len(rows) && rows[i-1].toolGap
	}
	state.FollowEnd = !hasNewer
	v.positionWorkSource(c, state, rows)
	v.anchorHistoryRows(c, state, rows)
	v.anchorSentMessage(c, state, rows)
	v.positionFind(c, state, rows)
	rowBounds := map[string]n.Rect{}
	history := n.List(c.Key("history:"+viewKey), state, historyEndRow+1, func(row int) {
		if row == 0 {
			height := float32(transcriptStartInset)
			if v.hasHistoryPageControl() {
				height = 48 + 20 + 8
			}
			n.Column(c).Height(height).Padding(48, 18, 0, 18).AlignItems(n.Center).Children(func() { v.historyPageControl(c, t, false) })
			return
		}
		if row == historyEndRow {
			// Keep the structural key/index stable; the list measures the dock
			// into its end padding in this layout, before the first paint.
			n.Column(c).Height(0)
			return
		}
		if hasNewer && row == len(rows)+1 {
			height := float32(0)
			if v.historyPage != nil && v.historyPage.error != "" && v.historyPage.readNewer {
				height = 20 + 8
			}
			n.Column(c).FillWidth().Height(height).AlignItems(n.Center).Children(func() { v.historyPageControl(c, t, true) })
			return
		}
		entry := rows[row-1]
		if entry.toolGap {
			n.Column(c).Height(32 - 8)
			return
		}
		v.currentTranscriptRow = row
		i := entry.end
		item := entry.item
		bottom := float32(8)
		hasToolBody := entry.toolTitle && row < len(rows) && rows[row].toolParent == entry.group
		if !hasToolBody && i >= 0 && (i == len(detail.Items)-1 || (item.TurnID != "" && detail.Items[i+1].TurnID != item.TurnID)) {
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
			if next.footer != "" || next.restarting || next.preparing != "" || next.group != "" || next.item.Kind == "request" || next.item.Presentation == "compaction" || domain.ReasoningSummary(next.item) {
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
		if entry.footer != "" && row < len(rows) && rows[row].item.Presentation == domain.GoalContinuationPresentation {
			bottom = 8
		}
		// A compaction at the end of the transcript has no reply footer to
		// provide reading clearance above the dock.
		if row == len(rows) && item.Presentation == "compaction" {
			bottom = 32
		}
		// Inline and trailing live status use the same 24 DIP reading slot.
		// Keep its extra clearance on the disclosure clock.
		if entry.toolStatus {
			bottom = 4
		} else if entry.toolTitle && entry.group == v.runningGroup {
			bottom = 4 + 4*entry.toolReveal
		}
		if item.Presentation == "compaction" && row < len(rows) && rows[row].footer == item.TurnID && rows[row].replyBefore == nil && v.Model.Connected {
			turn := turns[item.TurnID]
			if turn.Status.Active() && v.runningActivity(turn).compaction == item.ID {
				bottom = 24 - 20
			}
		}
		if domain.ReasoningSummary(item) && v.runningReasoning == item.ID && domain.HistorySpacer(item) && row < len(rows) && rows[row].footer == item.TurnID && rows[row].replyBefore == nil && v.Model.Connected {
			bottom = 24 - 20
		}
		// A pending source takes the running footer's 24 DIP slot. Its
		// 20 DIP line needs only the remaining clearance, just like compaction.
		if item.Kind == "request" && row < len(rows) && rows[row].footer == item.TurnID && rows[row].replyBefore == nil && (item.Status == "pending" || item.Status == "submitting") && v.runningActivity(turns[item.TurnID]).waiting {
			bottom = 24 - 20
		}
		// A user message begins a new reading block even when steering keeps
		// the protocol turn identity. Match ordinary turn-to-user spacing.
		if row < len(rows) && rows[row].item.Kind == "user" {
			bottom = 32
		}
		if row < len(rows) && rows[row].toolGap {
			bottom = 8
		}
		if hasNewer && row == len(rows) && v.historyPage != nil && v.historyPage.error != "" && v.historyPage.readNewer {
			bottom = 8
		}
		n.Column(c).FillWidth().PaddingX(18).AlignItems(n.Center).Children(func() {
			content := n.Column(c).FillWidth().MinWidth(0).MaxWidth(768).Gap(8).Padding(0, 0, bottom, 0).ClipX().Selectable()
			if entry.footer == "" && entry.group == "" && (item.Kind == "user" || item.Kind == "assistant" || item.Kind == "command") {
				n.SelectionItem(content, item.ID)
			}
			v.sentMessagePose(c, content, item)
			if entry.toolParent != "" && !entry.toolStatus {
				content.Padding(0, 0, bottom, toolTimelineInset+toolGlyphSlot+toolTimelineGap).Draw(func(p *n.Painter, r n.Rect) {
					height := r.H
					if row == len(rows) || rows[row].toolParent != entry.toolParent || rows[row].toolStatus {
						height = 10
					}
					p.Fill(n.Rect{X: r.X + toolTimelineInset + toolGlyphSlot/2 - .5, Y: r.Y, W: 1, H: height}, t.Border, 0)
				})
				content.Opacity(entry.opacity)
				if !v.reduceMotion {
					content.Transform(n.Translation(0, -4*(1-entry.opacity)))
				}
			}
			if entry.group != "" || entry.toolStatus || entry.toolParent == "" && !entry.restarting && entry.opacity < 1 {
				content.Opacity(entry.opacity)
			}
			content.DrawOver(func(_ *n.Painter, r n.Rect) { rowBounds[entry.key] = r })
			v.historySuffixPose(c, content, entry)
			content.Children(func() {
				if entry.preparing != "" {
					v.homeSendStatus(c, t, entry.preparing)
				} else if entry.restarting {
					v.turnFooter(c, t, domain.Turn{Status: "running"})
				} else if entry.replyBefore != nil {
					v.replyBeforeInputFooter(c, t, turns[entry.footer], entry.replyBefore)
				} else if entry.footer != "" {
					v.turnFooter(c, t, turns[entry.footer])
				} else if entry.toolTitle {
					v.toolGroupTitle(c, t, entry, row)
				} else if entry.toolStatus {
					v.toolGroupProgress(c, t, entry)
				} else if entry.toolParent != "" {
					v.toolTimelineItem(c, t, entry, toolTimelineInset)
				} else {
					v.item(c, t, item)
				}
			})
		})
	}).Absolute().Left(0).Right(0).Top(0).Bottom(0).ScrollbarInsets(8, 0, 8, 0).Label("对话记录").Opacity(v.loadingHistoryOpacity()).IgnoreInput(v.loadingBlocksComposer())
	history.DrawOver(func(_ *n.Painter, _ n.Rect) {
		v.historyRowBounds = rowBounds
	})
	history.HandleInput(v.historyInput)
	n.VirtualTextSelection(history, func(key string) (string, bool) {
		for _, item := range detail.Items {
			if item.ID == key {
				if item.Kind == "user" || item.Kind == "command" {
					return v.messageCopyText(item), true
				}
				return item.Text, true
			}
		}
		return "", false
	})
	if !v.loadingBlocksComposer() {
		v.conversationOutline(c, t, state, rows)
	}
	v.prefetchHistory(c, state, rows)
	n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(mainToolbarHeight).Padding(0, conversationScrollbarGutter, 0, 0).Label("会话顶栏").Draw(func(p *n.Painter, r n.Rect) {
		// History is clipped to its reading column, so the gutter contains
		// only the pane background and native thumb, never overflowing text.
		p.Fill(n.Rect{X: r.X, Y: r.Y, W: max(0, r.W-conversationScrollbarGutter), H: r.H}, t.Background, 0)
	}).Children(func() { v.toolbar(c, t, switchPane) })
	v.conversationFindBar(c, t, state.Bounds(c).W)
	state.EndOverlayRelease = 0
	dock := n.Column(c.Key("conversation-dock")).Absolute().Left(0).Right(0).Bottom(8).PaddingX(18).AlignItems(n.Center).Gap(8).Label("会话底部面板")
	var goalDock bool
	dock.Children(func() {
		v.notice(c, t)
		v.pending(c, t, detail)
		goalDock = v.composer(c, t, false)
	})
	dock.Draw(func(p *n.Painter, r n.Rect) {
		width := max(0, r.W-conversationScrollbarGutter)
		if goalDock {
			// Cover the fixed stack while leaving history visible beside the
			// separately attached return control above it.
			p.Fill(n.Rect{X: r.X, Y: r.Y, W: width, H: r.H + 8}, t.Background, 0)
			return
		}
		// Cards paint their own opaque rounded surfaces; the composer also
		// covers its lower corners. Continue that cover to the pane bottom,
		// leaving the native scrollbar gutter uncovered.
		p.Fill(n.Rect{X: r.X, Y: r.Y + r.H, W: width, H: 8}, t.Background, 0)
	})
	// Native wrapping and question disclosure determine the complete dock height.
	invalidate := c.Services().Invalidate
	dock.DrawOver(func(_ *n.Painter, r n.Rect) {
		if math.Abs(float64(v.dockHeight-r.H)) > .5 {
			v.dockHeight = r.H
			invalidate()
		}
	})
	state.EndOverlay = dock
	v.sessionLoadingOverlay(c, t)
	if !v.loadingBlocksComposer() {
		v.returnToLatest(c, t, state, dock)
	}
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
			heading := control(c).AlignSelf(n.Start).MinHeight(20).Justify(n.Start).Label(label).Tooltip(label).ContextMenu(commandCopyMenu(c, item.Name))
			if heading.Children(func() {
				n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
					n.Text(c, commandPreview(label)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).Font("Menlo").FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).MinWidth(0).MaxLines(2).Ellipsis("…")
					if issue := toolIssueLabel(item); issue != "" {
						n.Text(c.Key("command-issue"), issue).SingleLine().Shrink(0).FontSize(transcriptCaptionSize).FixedLineHeight(20).TextColor(t.TextMuted).
							Transition(resultBlockMotion(c))
					}
					if item.Text != "" {
						angle := float32(0)
						if v.Model.Collapsed[v.commandOutputKey(item.ID, "")] {
							angle = -90
						}
						inlineSymbol(c.Key("command-chevron"), transcriptChevron, label, n.Font{Family: "Menlo", Size: transcriptCaptionSize}, 14, t.TextMuted, angle).Shrink(0).Transition(resultBlockMotion(c))
					}
				})
			}).Clicked() {
				key := v.commandOutputKey(item.ID, "")
				v.Model.Collapsed[key] = !v.Model.Collapsed[key]
			}
			v.commandOutput(c, t, item, heading)
		})
	case "activity", "reasoning":
		v.activityItem(c, t, item)
	case "request_result":
		v.requestResult(c, t, item)
	case "request":
		v.requestSource(c, t, item, false, v.replyStreams[item.ID])
	default:
		n.Text(c, item.Text).TextColor(t.TextMuted).FontSize(12).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
	}
}
func (v *View) turnFooter(c *n.Context, t *n.Theme, turn domain.Turn) {
	v.replyFooter(c, t, turn, "")
}

// Receipt and turn status share their full native subtree, so hydration can
// replace the label without restarting row entrance or content animation.
func (v *View) replyFooter(c *n.Context, t *n.Theme, turn domain.Turn, preparing string) {
	if (turn.Status.Active() || preparing != "") && v.stoppingTurn(turn.ID) {
		turn.Status = domain.ExecutionInterrupted
		preparing = ""
	}
	interrupted := turn.Status == domain.ExecutionInterrupted
	frameKey := "reply-footer"
	if interrupted {
		// Removing the running footer's parent also removes its exit layers.
		frameKey = "interrupted-reply-footer"
	}
	frame := n.Column(c.Key(frameKey)).Label("回复信息").FillWidth().MinHeight(24).Clip()
	replySummary := turn.ID != "" && (turn.Status == domain.ExecutionCompleted || turn.Status == domain.ExecutionInterrupted || turn.Status == domain.ExecutionAbandoned)
	state := string(turn.Status)
	if (turn.Status == "running" || turn.Status == "starting") && !v.Model.Connected {
		state = "disconnected"
	}
	activity := v.runningActivity(turn)
	liveEmptyReasoning := v.runningReasoning != "" && activity.hiddenReasoning && v.activityTurn == turn.ID
	delegated := activity.sending == "" && turn.Status.Active() && ((v.runningGroup != "" && v.activityTurn == turn.ID) || liveEmptyReasoning || v.Model.Connected && activity.compaction != "")
	waiting := activity.sending == "" && turn.Status.Active() && v.Model.Connected && activity.waiting
	if preparing != "" || turn.Status == "running" || turn.Status == "starting" {
		frame.MinHeight(0)
	}
	if preparing != "" || state == "starting" {
		state = "running"
	}
	// A tool-to-prose handoff already has its final footer height. A second
	// entrance/height animation would clip the new dot and move it on screen.
	if p := v.runningProgress; activity.compaction == "" && !waiting && state == "running" && p != nil && p.turn == turn.ID && p.handoff != nil {
		frame.Height(24).Children(func() {
			n.Column(c.Key("natural-content")).FillWidth().Children(func() {
				// Retain the running row's exit for a later insertion, with no
				// second entrance during the tool-to-prose handoff.
				replyFooterRow(c, "running").Transition(n.ElementTransition{
					Duration: steerFooterDuration, Ease: n.EaseOut, Exit: &n.Motion{Opacity: 0},
				}).Children(func() { v.runningStatus(c, t, turn) })
			})
		})
		return
	}
	replacement := ""
	if waiting {
		items := v.Model.Details[v.Model.SessionID].Items
		for i := len(items) - 1; i >= 0; i-- {
			item := items[i]
			if item.TurnID != turn.ID || item.WorkID != "" || domain.HistorySpacer(item) {
				continue
			}
			if item.Kind == "request" && (item.Status == "pending" || item.Status == "submitting") {
				replacement = item.ID
			}
			break
		}
	}
	if delegated && !waiting {
		if activity.compaction != "" {
			replacement = activity.compaction
			if key := v.contextRowKeys[v.Model.WorkKey(v.Model.SessionID, replacement)]; key != "" {
				replacement = key
			}
		} else if liveEmptyReasoning {
			replacement = v.runningReasoning
		} else if v.runningGroup != "" && v.activityTurn == turn.ID && v.Model.ToolDisclosure(v.runningGroup).Closed() && v.toolMotions[v.runningGroup] == nil {
			replacement = v.runningGroup
		}
	}
	wasDelegated := n.Local(frame, "was-delegated", func() bool { return false })
	if replacement != "" {
		*wasDelegated = true
		// The replacement summary already reveals its actual height. Give up
		// the old slot on that same clock, so status ownership alone cannot
		// change the transcript height or trigger bottom-follow scrolling.
		remaining := float32(0)
		if !v.reduceMotion && v.toolArrivals != nil {
			if arrival := v.toolArrivals.motions[replacement]; arrival != nil {
				progress, _ := arrival.sample(c.Now())
				remaining = 1 - progress
			}
		}
		frame.Height(frame.Jump("height", 24*remaining))
		return
	}
	duration := 180 * time.Millisecond
	if interrupted {
		duration = 0
	}
	initiallyVisible := !*wasDelegated && !delegated && !waiting
	v.transcriptContentHeight(c, frame, !delegated && !waiting, initiallyVisible, duration, func() float32 {
		row := replyFooterRow(c, state)
		if !interrupted {
			row.Transition(fadeMotion(c, 180*time.Millisecond))
		}

		row.Children(func() {
			if delegated || waiting {
				return
			}
			if preparing != "" {
				v.progressStatus(c, t, runningActivity{label: preparing}, nil)
				return
			}
			if state == "disconnected" {
				n.Text(c, "暂时无法查看回复进度").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
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
				if turn.UsageLimit != nil {
					label = "用量耗尽"
				}
			case "abandoned":
				label = "已结束 · 结果未确认"
			case "unknown":
				label = "执行状态待核实"
			}
			if replySummary {
				v.replyFooterSummary(c, t, turn, nil, label)
			} else if turn.Status == "running" || turn.Status == "starting" {
				v.runningStatus(c, t, turn)
			} else {
				animation := motionAnimation(c, 160*time.Millisecond)
				ink := t.TextMuted
				if turn.Status == domain.ExecutionFailed {
					ink = t.Danger
				}
				n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(ink).ContentTransition(n.ContentTransition{Animation: &animation})
				if turn.Status == domain.ExecutionFailed && turn.Error != "" && turn.UsageLimit == nil {
					n.Text(c, turn.Error).FontSize(11).FixedLineHeight(20).TextColor(t.Danger).SingleLine().MaxWidth(480).Tooltip(turn.Error).
						Transition(fadeMotion(c, 160*time.Millisecond))
				}
			}
			if !replySummary && !turn.Status.Active() && turn.DurationMS != nil {
				n.Text(c, fmt.Sprintf("%.1f 秒", float64(*turn.DurationMS)/1000)).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			}
		})
		return 0
	})
}

func (v *View) replyBeforeInputFooter(c *n.Context, t *n.Theme, turn domain.Turn, boundary *replyBoundary) {
	frame := n.Column(c.Key("reply-footer")).Label("回复信息").FillWidth().MinHeight(24).Clip()
	v.transcriptContentHeight(c, frame, true, true, steerFooterDuration, func() float32 {
		row := replyFooterRow(c, "reply-before-input")
		if h := v.steerFooterTransition(); h != nil && h.input == boundary.inputID {
			if tool := h.tool; tool != nil {
				p := h.fraction(c.Now())
				row.Opacity(p).IgnoreInput(true)
				if !tool.inline {
					n.Column(c.Key("outgoing-tool-progress")).Absolute().Top(0).Left(0).Right(0).Opacity(1 - p).IgnoreInput(true).Children(func() {
						v.toolProgressLine(c, t, tool.label, tool.elapsed, tool.shimmerAt, 1)
					})
				}
			} else {
				row.Transition(fadeMotion(c, steerFooterDuration)).IgnoreInput(true)
			}
		}
		row.Children(func() { v.replyFooterSummary(c, t, turn, boundary, "") })
		return 0
	})
}

func replyFooterRow(c *n.Context, state string) n.Element {
	return n.Row(c.Key("state:" + state)).FillWidth().Wrap().Gap(messageFooterGap).MinHeight(24).AlignItems(n.Baseline)
}

func (v *View) replyFooterSummary(c *n.Context, t *n.Theme, turn domain.Turn, boundary *replyBoundary, label string) {
	before := ""
	endedAt, elapsed := turn.CompletedAt, turn.DurationMS
	if boundary != nil {
		before = boundary.inputID
		endedAt, elapsed = boundary.end, nil
		// Imported history can only report the turn's opening timestamp for
		// these inputs. It cannot establish a later insertion time.
		if endedAt != nil && (endedAt.IsZero() || turn.StartedAt != nil && !endedAt.After(*turn.StartedAt)) {
			endedAt = nil
		}
		if endedAt != nil && boundary.start != nil && !boundary.start.IsZero() && !endedAt.Before(*boundary.start) {
			ms := endedAt.Sub(*boundary.start).Milliseconds()
			elapsed = &ms
		}
	}
	metricText := func(text string) n.Element {
		field := n.Text(c, text).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		if boundary == nil && turn.Status != domain.ExecutionInterrupted {
			field.ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 180*time.Millisecond)})
		}
		return field
	}
	copy := n.ButtonBase(c.Key("copy-reply:"+turn.ID+before)).Size(messageFooterActionWidth, 24).Justify(n.Start).Label("复制回复")
	copiedUntil := n.Local(copy, "copied-until", func() time.Time { return time.Time{} })
	if copy.Clicked() {
		items := v.stoppingReplyDetail(v.Model.Details[v.Model.SessionID]).Items
		if text := messageReplyText(items, turn.ID, before); text != "" {
			c.WriteClipboard(text)
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
	forking := boundary == nil && v.historyExit != nil && !v.historyExit.in.Restart && v.historyExit.in.SessionID == v.Model.SessionID &&
		v.historyExit.anchorKey == "turn-footer:"+turn.ID
	spinning := false
	if forking && v.historyExit.phase == historyDispatched {
		delay := v.historyExit.started.Add(350 * time.Millisecond).Sub(c.Now())
		spinning = delay <= 0
		if !spinning {
			c.After(delay)
		}
	}
	fork := n.ButtonBase(c.Key("fork:"+turn.ID+before)).Size(messageFooterActionWidth, 24).Justify(n.Start)
	if boundary != nil {
		fork.Label("轮次中途无法分叉").Tooltip("只支持在轮次边界分叉").Disabled(true)
	} else {
		fork.Label("从这里分叉").Tooltip("从这里分叉").
			Disabled(!v.historyReady(v.Model.Details[v.Model.SessionID].Session) || !v.Model.RunReadiness().Capabilities.Fork || v.historyBusy[v.Model.SessionID] || forking)
	}
	forkColor := textHoverColor(c, fork, t.TextMuted)
	fork.Children(func() {
		font := n.Font{Family: t.Font, Size: 11, Weight: 400}
		if spinning {
			serviceSpinner(c.Key("pending"), "回复", font, forkColor).Label("正在创建分叉")
		} else {
			inlineSymbol(c, forkIcon, "回复", font, 13, forkColor)
		}
	})
	if spinning {
		fork.Label("正在创建分叉").Tooltip("正在创建分叉")
	}
	if boundary == nil && fork.Clicked() {
		v.forkSession(v.Model.Details[v.Model.SessionID].Session, turn.ID)
	}
	ended, exact := "—", "结束时间未知"
	if endedAt != nil {
		local := endedAt.Local()
		ended = local.Format("15:04")
		exact = local.Format("2006-01-02 15:04:05")
	}
	metricText(ended).Label("结束时间：" + ended).Tooltip(exact)
	if boundary == nil && turn.Status != domain.ExecutionCompleted {
		animation := motionAnimation(c, 160*time.Millisecond)
		n.Text(c, label).Label("回复状态：" + label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: &animation})
	}
	duration := "用时 —"
	if elapsed != nil {
		seconds := *elapsed / 1000
		duration = fmt.Sprintf("用时 %d分%d秒", seconds/60, seconds%60)
	}
	metricText(duration)
	if boundary == nil {
		n.Column(c).MinHeight(24).Justify(n.Center).Label("本轮用量").Children(func() {
			metricText(fmt.Sprintf("输入 %s · 输出 %s · 缓存 %s", count(turn.InputTokens), count(turn.OutputTokens), count(turn.CachedInputTokens)))
		})
	}
}

func messageReplyText(items []domain.Item, turnID, before string) string {
	end := len(items)
	if before != "" {
		end = slices.IndexFunc(items, func(item domain.Item) bool { return item.ID == before })
		if end < 0 {
			return ""
		}
	}
	var parts []string
	for i := end - 1; i >= 0; i-- {
		item := items[i]
		if item.TurnID != turnID || item.WorkID != "" {
			continue
		}
		if acceptedReplyInput(item) {
			break
		}
		if item.Kind == "assistant" && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	slices.Reverse(parts)
	return strings.Join(parts, "\n\n")
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
	state.busy, state.err, state.decision = true, "", answer.Decision
	if !v.do(app.Job{Method: "request.respond", Input: protocol.AnswerInput{SessionID: r.SessionID, Answer: answer}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			for _, current := range m.Details[r.SessionID].Requests {
				if current.ID == r.ID && current.Revision == r.Revision {
					if current.State == "pending" || current.Kind == "questions" && current.State == "submitting" {
						state.busy = false
						state.err = err.Error()
					}
					break
				}
			}
		}
	}}) {
		state.busy = false
	}
}
func (v *View) submit() { v.sendMessage(false) }
func (v *View) sendMessage(steer bool) {
	if phase := v.currentConnectionPhase(); phase != app.ConnectionIdle {
		return
	}
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
		return
	}
	if v.Model.ActiveQueueEdit(v.Model.SessionID).ID != "" {
		v.saveQueued()
		return
	}
	key, _ := v.gitQuery()
	if v.gitLoading(key) && !v.Model.Active() && len(v.Model.Queue().Items) == 0 {
		v.feedbackGitWait = key
		return
	}
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, method, turn, err := m.BeginMessage(steer && !v.sessionStopInFlight(m.SessionID))
	if err != nil {
		m.Error = err.Error()
		return
	}
	v.dismissSubmissionFailure()
	if method != "session.submit" {
		v.submitActive(in, method, turn)
		return
	}
	v.submitNewTurn(in, n.Rect{})
}

func (v *View) canRetryMessage(sid string) bool {
	return v.Controller != nil && v.currentConnectionPhase() == app.ConnectionIdle &&
		(v.parent == nil || v.parent.Model.SecondarySessionID == sid) &&
		!v.homeSendPending() && v.Model.CanRetryMessage(sid)
}

func (v *View) retryComposerWarning(sid string, source n.Rect) {
	m := v.Model
	if m.SessionID != sid || !v.canRetryMessage(sid) {
		return
	}
	in, err := m.BeginRetryMessage(sid)
	if err != nil {
		m.Error = err.Error()
		return
	}
	v.dismissSubmissionFailure()
	v.submitNewTurn(in, source)
}

func (v *View) submitNewTurn(in domain.Submission, source n.Rect) {
	m := v.Model
	sent := v.beginPresentedSend(in, "")
	if source.W > 0 {
		sent.source = source
		sent.fromNotice = true
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
			v.presentedSendError(m, in, err)
			return
		}

		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil || op.ID != in.OperationID {
			v.presentedSendError(m, in, errors.New("设备未确认本次发送"))
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
func (v *View) nextAttention() bool {
	if s, ok := v.attentionSession(); ok {
		v.navigateSession(s.ID, s.WorkspaceID)
		v.collapsedWorkspaces[s.WorkspaceID] = false
		v.navReveal = s.ID
		v.Model.Collapsed[s.ID] = false
		v.devices = false
		return true
	}
	return false
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
	if job.Apply != nil {
		apply := job.Apply
		source := v.feedbackSource()
		source.connection = job.ConnectionID
		switch in := job.Input.(type) {
		case domain.Submission:
			source.session, source.workspace = in.SessionID, in.WorkspaceID
		case domain.HistoryChange:
			source.session = in.SessionID
		case protocol.HistoryOperationQuery:
			source.session = in.SessionID
		case protocol.AnswerInput:
			source.session = in.SessionID
		}
		for _, s := range v.persistenceModel().Snapshot.Sessions {
			if s.ID == source.session {
				source.workspace = s.WorkspaceID
				break
			}
		}
		job.Apply = func(model *app.Model, body json.RawMessage, err error) {
			previous := model.Error
			if v.parent != nil {
				model.Error = ""
			}
			feedbackCount := len(model.Feedbacks)
			apply(model, body, err)
			message := model.Error
			late := source.session != model.SessionID || source.workspace != model.WorkspaceID
			if message != "" && message != app.ModelUnavailableMessage && (message != previous || v.parent != nil) {
				title := actionFailureTitle(job.Method)
				if len(model.Feedbacks) == feedbackCount && (!v.feedbackErrorCovered(model, source) || late) && (title != "" || !isFeedbackTimeout(message)) {
					model.ReportFeedback(app.Feedback{ConnectionID: source.connection, SessionID: source.session, WorkspaceID: source.workspace, Title: title, Details: message})
				}
				if source == v.feedbackSource() {
					v.feedbackObservedError = message
				}
			}
			if v.parent != nil && v.Model.SessionID == source.session {
				v.Model.Error = model.Error
			}
			if v.parent != nil || late {
				model.Error = previous
			}
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
