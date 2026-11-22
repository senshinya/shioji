package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
	"shioji/internal/tailnet"
)

type mobileApp struct {
	sessionOpening            *sessionListTarget
	sessionLoadingTarget      string
	previewLatestHistory      *domain.SessionDetail
	newSessionSend            *mobileSendScroll
	queueActionsPage          *mygo.Window
	latestSessionEntry        string
	reduceConversationMotion  bool
	forkMotionTarget          mobileForkPosition
	composerKeyboardDismissed uintptr
	keyboardMotion            mobileKeyboardMotion
	previewConversationMotion string

	model                    *app.Model
	controller               *app.Controller
	cancel                   context.CancelFunc
	directory                string
	rootDirectory            string
	savedDevices             savedMobileDevices
	legacyMigrations         map[string]mobileLegacyReceipt
	legacyMigrationLoadError error
	workerPreparations       map[string]mobileServicePreparation
	workerPreparationError   error
	sessionPins              []app.SessionLocation
	pinLoadError             error
	navigationOrder          mobileclient.NavigationOrder
	navigationOrderLoadError error
	navigationOrderingOpen   bool
	sessionCatalogs          map[string]mobileclient.DeviceCatalog
	sessionCatalogCancel     context.CancelFunc
	sessionListNotice        string
	workspaceArchiveRequest  mobileRequest
	operationHistory         mobileclient.OperationFeedbackHistory
	sessionListOpening       *sessionListTarget
	attentionShortcut        *attentionShortcutScan
	attentionFeedback        attentionShortcutFeedback
	sessionGroupings         sessionGroupingSettings
	sessionListMenu          mobileContextMenu
	previewResources         *previewImageResources
	notifications            mobileNotifications
	tailnetNode              *tailnet.Node
	tailnetStatus            tailnet.Snapshot
	tailnetLastPeers         []tailnet.Peer
	tailnetPending           string
	forwards                 *portforward.Manager
	forwardedPageOpen        bool
	linkExport               *mobileLinkExport
	agentSignIns             map[string]*mobileAgentSignIn
	agentSettingsDrafts      map[[2]string]*mobileAgentSettingsDraft
	mcpDrafts                map[[2]string]*mobileToolEditor
	hookReceipts             map[mobileHookSlot]*mobileHookReceipts
	compactions              map[app.WorkKey]*mobileCompaction
	sessionStops             map[app.WorkKey]*mobileSessionStop
	queueSaves               map[app.WorkKey]string
	goalOperations           map[app.WorkKey]*mobileGoalOperation
	goalEntranceSession      app.WorkKey
	sessionReloads           map[mobileclient.SessionReloadTarget]*mobileSessionReload
	reloadStorageError       string
	windows                  map[*mygo.Window]bool
	pageDevice               map[*mygo.Window]uint64
	deviceSelection          uint64
	home                     *mygo.Window
	devices                  *mygo.Window
	settings                 *mygo.Window
	settingsQuery            string
	detail                   *mygo.Window
	chat                     *mygo.Window
	chatTitle, chatScope     string
	lastConversation         app.SessionLocation
	previousConversation     app.SessionLocation
	draftPage                *mygo.Window
	draftReturnSession       string
	stateWriter              *app.StateWriter
	draftEncodingFailed      bool
	catalogWriter            *app.StateWriter
	cachedGeneration         string
	cachedSequence           int64
	review                   *reviewState
	conversationEditor       *mobileDraftEditor
	parallel                 string
	parallelDevice           *mobileApp
	parallelOwner            *mobileApp
	parallelEditor           *mobileDraftEditor
	draftAttachmentUploads   map[mobileDraftUploadKey]*mobileDraftUpload
	endpoint                 mobileclient.Endpoint
	signer                   ssh.Signer
	setupError               string
	archive                  bool
	archivePages             map[uintptr]*mygo.Window
	archiveConfirmation      *mygo.Window
	archiveDeletion          *mobileArchiveDeletion
	filter                   mobileclient.WorkspaceLocation
	search                   string
	searching                bool
	searchAfterDetailBlur    bool
	prominentCompose         bool
	history                  map[string]*ui.ListState
	importCompletions        map[app.WorkKey]*mobileImportCompletion
	historyJump              transcriptJump
	replyActivation          uint64
	find                     *mobileConversationFind
	findShortcutPage         *mygo.Window
	parallelFind             *mobileConversationFind
	parallelFindQueries      map[mobileFindLocation]string
	parallelFindHandle       ui.Handle
	toolTimelines            map[app.WorkKey]*mobileToolTimeline
	historyRequests          map[app.WorkKey]*mobileHistoryRequest
	transcriptImages         *mobileImageCache
	displayAttachmentCaches  map[string]*app.AttachmentCache
	preview                  bool
	requestReveal            mobileRequestReveal
	previewRoute             string
	previewDeviceName        string
	previewReviewBoard       bool
	previewDisabledSkills    map[string]bool
	previewSkillRevision     int
	previewSkillFailure      bool
	previewHooks             *mobilePreviewHooks
	previewCompaction        *mobilePreviewCompaction
	previewReload            *mobilePreviewReload
	previewFind              bool
	previewReferenceKeyboard bool
	uiConfigured             bool
	widthReported            bool
	wide                     bool
	zoomFeedback             *mygo.Window
}

func main() {
	var a *mobileApp
	var initialNotification string
	mygo.App.OnNotificationClick(func(id string) {
		if a == nil {
			initialNotification = id
			return
		}
		a.notifications.toOpen = id
		a.openQueuedNotification()
	})
	mygo.App.WhenReady(func() {
		a = &mobileApp{model: app.NewModel(), windows: map[*mygo.Window]bool{}, pageDevice: map[*mygo.Window]uint64{}, history: map[string]*ui.ListState{},
			sessionReloads: make(map[mobileclient.SessionReloadTarget]*mobileSessionReload)}
		a.notifications.toOpen = initialNotification
		base, err := os.UserConfigDir()
		if err == nil {
			a.directory = filepath.Join(base, "ShiojiMobile")
			err = os.MkdirAll(a.directory, 0700)
		}
		if err != nil {
			a.setupError = err.Error()
			a.directory = ""
		}
		a.rootDirectory = a.directory
		if isSimulator() && len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--preview") {
			a.preview = true
			a.previewRoute = strings.TrimPrefix(os.Args[1], "--preview-")
			a.seedPreview()
		} else if a.directory != "" {
			a.load()
			a.loadSavedDevices()
			a.loadLegacyMigrations()
			a.loadWorkerPreparations()
			a.loadSessionPins()
			a.loadSessionBrowsing()
			a.loadSessionReloads()
			a.loadNotificationRoutes()
			a.stateWriter = a.newDraftWriter()
			a.catalogWriter = app.NewStateWriter(filepath.Join(a.directory, "catalog.json"))
		}
		mygo.Theme.SetSource(mygo.ThemeSource(a.model.Preferences.Appearance))
		a.start()
	})
	mygo.App.Run()
}

func (a *mobileApp) start() {
	mygo.App.OnDidBecomeActive(func() {
		a.replyActivation++
		a.refreshSessionCatalogs()
		a.refresh()
	})
	a.home = a.page("", a.homeView)
	a.sessionListMenu.listen(a, a.home)
	a.devices = a.page("", a.devicesView)
	a.settings = a.page("", a.settingsView)
	mygo.SetBarItems(a.devices, mygo.BarItem{Title: "添加设备", Symbol: "plus", OnClick: func() {
		a.openPair(a.devices, mobileclient.Endpoint{})
	}})
	if _, err := mygo.NewTabs(
		mygo.TabItem{Title: "会话", Icon: toolbarImage(conversationsSymbol), Window: a.home},
		mygo.TabItem{Title: "设备", Icon: toolbarImage(deviceSymbol), Window: a.devices},
		mygo.TabItem{Title: "设置", Icon: toolbarImage(gearSymbol), Window: a.settings},
	); err != nil {
		log.Fatal(err)
	}
	a.startTailnet()
	if a.endpoint.Address != "" {
		a.connect(a.endpoint)
		a.refreshSessionCatalogs()
	}
	if !a.preview {
		a.forwards = portforward.New(context.Background(), func(_ []portforward.Mapping) { mygo.RunOnMain(a.refresh) })
	}
	a.configureUIKit()
}

func (a *mobileApp) page(title string, draw func(*ui.Context)) *mygo.Window {
	var w *mygo.Window
	owner := a.deviceSelection
	w = mygo.NewWindow(mygo.WindowOptions{Title: title, Content: ui.View(func(c *ui.Context) {
		c.SetZoom(a.splitRoot().model.Preferences.InterfaceZoom)
		if font := a.splitRoot().model.Preferences.InterfaceFont; font != "" && font != "system-ui" {
			theme := *c.Theme()
			theme.Font = font + ", system-ui"
			c.SetTheme(&theme)
		}
		c.ReduceMotion(a.model.Preferences.ReduceMotion)
		if w != nil && !a.pageCurrent(w) {
			mobilePageBody(c, func() { mobileMessage(c, "执行设备已切换，请返回重新打开此页面。") })
			return
		}
		draw(c)
		a.showZoomFeedback(c, w)
		if a.searchAfterDetailBlur && w == a.detail {
			a.searchAfterDetailBlur = false
			c.Root().Focus(ui.FocusOptions{PreventScroll: true})
			a.showSessionSearch()
		}
		if a.attentionFeedback.source == w && a.attentionFeedback.message != "" {
			message := a.attentionFeedback.message
			a.attentionFeedback = attentionShortcutFeedback{}
			c.AddToast(ui.Toast{ID: "attention-shortcut", Title: message, Timeout: 2500 * time.Millisecond})
		}
	}), Hidden: true})
	a.windows[w] = true
	a.pageDevice[w] = owner
	w.OnClosed(func() { delete(a.windows, w); delete(a.pageDevice, w) })
	return w
}

func (a *mobileApp) rootPage(w *mygo.Window) bool {
	return w != nil && (w == a.home || w == a.devices || w == a.settings || w == a.detail)
}

func (a *mobileApp) pageCurrent(w *mygo.Window) bool {
	if a.parallelOwner != nil && a.parallelOwner.parallelDevice != a {
		return false
	}
	owner, ok := a.pageDevice[w]
	return ok && (a.rootPage(w) || owner == a.deviceSelection || a.archivePages[w.NativeHandle()] == w)
}

func (a *mobileApp) resetDeviceNavigation(done func(error)) {
	roots := []uintptr{a.home.NativeHandle(), a.devices.NativeHandle(), a.settings.NativeHandle()}
	if a.detail != nil {
		roots = append(roots, a.detail.NativeHandle())
	}
	a.callUIKit("resetDeviceNavigation", map[string][]uintptr{"roots": roots}, done)
}

func (a *mobileApp) refresh() {
	a.collectOperationFeedback()
	for w := range a.windows {
		if w != a.detail || a.draftPage == nil {
			w.Invalidate()
		}
	}
}

func (a *mobileApp) selectSession(id string) {
	a.selectConversation(id, transcriptJump{})
}

func (a *mobileApp) selectConversation(id string, jump transcriptJump) {
	a.cancelSessionListOpening()
	session := a.sessionByID(id)
	if session.ID != "" {
		location := app.SessionLocation{DeviceID: a.model.Snapshot.DeviceID, WorkspaceID: session.WorkspaceID, SessionID: session.ID}
		if location != a.lastConversation {
			a.previousConversation, a.lastConversation = a.lastConversation, location
		}
	}
	if a.model.SessionID != id {
		a.closeConversationFind()
		if a.parallelOwner != nil {
			a.suspendParallelFind()
		}
		a.historyJump = transcriptJump{}
		if old := a.model.SessionID; old != "" && old != a.parallel {
			a.releaseMarkdownImages(old)
		}
		delete(a.toolTimelines, a.model.WorkKey(id, ""))
		a.model.PruneHistoryCache(id, a.parallel)
		a.replaceReview(nil)
		if pane := a.parallelDevice; pane != nil {
			pane.replaceReview(nil)
		}
		a.model.SessionID = id
		if session.ID != "" {
			a.model.WorkspaceID = session.WorkspaceID
		}
		if id == a.parallel {
			a.setParallel("")
		}
		a.save()
		if a.controller != nil && jump.item == "" {
			a.controller.Select(id)
		}
	}
	if jump.item != "" {
		a.closeConversationFind()
		jump.ready = jump.ready || a.controller == nil
		a.historyJump = jump
		if a.controller != nil && !jump.ready {
			model := a.model
			a.controller.SelectHistory(id, jump.thread, jump.item, func(err error) {
				active := &a.historyJump
				if a.model != model || active.session != id || active.thread != jump.thread || active.item != jump.item {
					return
				}
				active.ready = true
				if err != nil {
					active.err = err.Error()
				}
				a.refresh()
			})
		}
	}
	a.refresh()
}

func (a *mobileApp) openSession(id string) { a.openSessionFrom(a.home, id) }

func (a *mobileApp) openSessionFrom(parent *mygo.Window, id string) {
	a.openConversationFrom(parent, id, transcriptJump{})
}

func (a *mobileApp) openConversationFrom(parent *mygo.Window, id string, jump transcriptJump) {
	session := a.sessionByID(id)
	if !a.pageCurrent(parent) || session.ID == "" {
		return
	}
	if jump.item != "" || a.latestSessionEntry != id {
		a.latestSessionEntry = ""
	}
	entering := a.chat == nil || a.model.SessionID != id || parent == a.home
	if entering && jump.item == "" && session.Status.Active() {
		a.history[id] = &ui.ListState{FollowEnd: true}
		if a.model.Connected && !hasLatestSessionHistory(a.model.Details[id]) {
			a.latestSessionEntry = id
			if a.model.SessionID == id && a.controller != nil {
				a.controller.Select(id)
			}
			if preview := a.previewLatestHistory; preview != nil && preview.Session.ID == id {
				a.previewLatestHistory = nil
				time.AfterFunc(2*time.Second, func() {
					mygo.RunOnMain(func() {
						a.model.Details[id] = *preview
						a.refresh()
					})
				})
			}
		}
	}
	if a.wide && parent == a.home && id == a.model.SessionID {
		if review := a.currentSplitReview(); review.right {
			review.owner.replaceReview(nil)
		}
	}
	a.selectConversation(id, jump)
	if a.chat != nil {
		// The shared conversation page can still be on the stack when an
		// explicit navigation target comes from a newly connected device.
		a.pageDevice[a.chat] = a.deviceSelection
		a.updateChatHeader()
		if a.sessionLoadingTarget != id {
			a.callUIKit("showPage", map[string]uintptr{"window": a.chat.NativeHandle()}, nil)
		}
		return
	}
	if a.wide && parent == a.home {
		return
	}
	a.pushConversation(parent)
}

func (a *mobileApp) updateChatHeader() {
	if a.chat == nil || a.sessionOpening != nil {
		return
	}
	s := a.session()
	scope := a.conversationHeaderScope(s)
	if s.Title == "" || s.Title == a.chatTitle && scope == a.chatScope {
		return
	}
	if s.Title != a.chatTitle {
		a.chat.SetTitle(s.Title)
	}
	a.chatTitle, a.chatScope = s.Title, scope
	a.callUIKit("conversationTitle", nativePageOptions{Window: a.chat.NativeHandle(), Title: s.Title, Subtitle: scope, InlineScope: isTablet(), ReduceMotion: a.model.Preferences.ReduceMotion}, nil)
}

func (a *mobileApp) pushConversation(parent *mygo.Window) {
	delete(a.toolTimelines, a.model.WorkKey(a.model.SessionID, ""))
	session := a.session()
	title, scope := session.Title, a.conversationHeaderScope(session)
	opening := a.sessionOpening
	if opening != nil {
		title, scope = opening.title, opening.scope
	}
	w := a.page(title, func(c *ui.Context) {
		a.conversation(c, a.chat, false)
	})
	a.chat, a.chatTitle, a.chatScope = w, title, scope
	w.OnClosed(func() {
		if a.find != nil && a.find.owner == w {
			a.closeConversationFind()
		}
		a.chat, a.chatTitle, a.chatScope = nil, "", ""
		a.sessionLoadingTarget = ""
		if opening != nil && a.sessionOpening == opening {
			a.sessionOpening = nil
			a.cancelSessionListOpening()
		}
		if isTablet() {
			a.callUIKit("tabsHidden", map[string]bool{"hidden": false}, nil)
		}
		a.refresh()
	})
	mygo.SetBarItems(w,
		mygo.BarItem{Title: "会话活动", Icon: toolbarImage(moreSymbol), OnClick: func() {
			if a.sessionOpening == nil {
				a.openActivity(w, a.model.SessionID)
			}
		}},
	)
	a.configureNativePage(w, nativePageOptions{Title: title, Subtitle: scope, InlineScope: isTablet()}, func(err error) {
		if err != nil || (a.wide && parent == a.home) {
			w.Close()
		} else {
			parent.Push(w)
			if isTablet() {
				a.callUIKit("tabsHidden", map[string]bool{"hidden": true}, nil)
			}
		}
	})
}

func (a *mobileApp) openDraft(parent *mygo.Window) {
	a.cancelSessionListOpening()
	if a.draftPage == nil && parent == a.home && a.filter.WorkspaceID != "" {
		a.openSessionListTarget(sessionListTarget{location: app.SessionLocation{
			DeviceID: a.filter.DeviceID, WorkspaceID: a.filter.WorkspaceID,
		}})
		return
	}
	a.presentDraft(parent, false)
}

func (a *mobileApp) presentDraft(parent *mygo.Window, startAtTop bool) {
	if a.draftPage != nil {
		return
	}
	current := a.workspace(a.model.WorkspaceID)
	if current.ID == "" || current.Archived {
		a.model.WorkspaceID = ""
		for _, ws := range a.model.Snapshot.Workspaces {
			if !ws.Archived {
				a.model.WorkspaceID = ws.ID
				break
			}
		}
	}
	a.draftReturnSession = a.model.SessionID
	a.closeConversationFind()
	a.model.SessionID = ""
	a.rebindHomeDraftAttachments()
	a.loadGit(a.model.WorkspaceID)
	w := a.page("新建会话", func(c *ui.Context) {
		a.newConversationView(c, startAtTop)
		startAtTop = false
	})
	a.draftPage = w
	w.OnClosed(func() {
		a.draftPage = nil
		if a.model.SessionID == "" && a.draftReturnSession != "" {
			a.selectSession(a.draftReturnSession)
		}
		a.draftReturnSession = ""
		a.save()
		a.refresh()
	})
	if isTablet() {
		a.callUIKit("presentForm", map[string]uintptr{"window": w.NativeHandle()}, func(err error) {
			if err != nil {
				w.Close()
			}
		})
	} else {
		a.pushNative(parent, w)
	}
}

func (a *mobileApp) session() domain.Session { return a.sessionByID(a.model.SessionID) }

func (a *mobileApp) sessionByID(id string) domain.Session {
	for _, session := range a.model.Snapshot.Sessions {
		if session.ID == id {
			return session
		}
	}
	return domain.Session{}
}

func (a *mobileApp) workspace(id string) domain.Workspace {
	for _, ws := range a.model.Snapshot.Workspaces {
		if ws.ID == id {
			return ws
		}
	}
	return domain.Workspace{}
}

type cachedCatalog struct {
	Snapshot  domain.Snapshot
	Readiness domain.Readiness
}

func readCachedCatalog(directory, deviceID string) cachedCatalog {
	var cached cachedCatalog
	if deviceID != "" {
		if data, err := os.ReadFile(filepath.Join(directory, "catalog.json")); err == nil &&
			json.Unmarshal(data, &cached) == nil && cached.Snapshot.DeviceID == deviceID {
			cached.Readiness = redactRuntimeReadiness(cached.Readiness)
			return cached
		}
	}
	return cachedCatalog{}
}

func (a *mobileApp) load() {
	data, err := os.ReadFile(filepath.Join(a.directory, "device.json"))
	if err == nil {
		err = json.Unmarshal(data, &a.endpoint)
	}
	if err != nil && !os.IsNotExist(err) {
		a.setupError = err.Error()
	}

	a.loadDeviceFiles()
}

func (a *mobileApp) loadDeviceFiles() {
	if err := a.model.Load(filepath.Join(a.directory, "state.json")); err != nil && !os.IsNotExist(err) {
		a.setupError = err.Error()
	}
	if cached := readCachedCatalog(a.directory, a.endpoint.DeviceID); cached.Snapshot.DeviceID != "" {
		observed := cached.Snapshot.ObservedAt
		// A dated disk cache must not set this launch's worker clock.
		cached.Snapshot.ObservedAt = time.Time{}
		a.model.ApplySnapshot(cached.Snapshot)
		a.model.Snapshot.ObservedAt = observed
		a.model.Readiness = cached.Readiness
	}
}

func (a *mobileApp) newDraftWriter() *app.StateWriter {
	writer := app.NewStateWriter(filepath.Join(a.directory, "state.json"))
	var failed atomic.Bool
	writer.SetInvalidate(func() {
		_, err := writer.Result()
		next := err != nil
		if failed.Swap(next) == next {
			return
		}
		// A synchronous flush can be waiting for the writer on the UI thread.
		go mygo.RunOnMain(func() {
			if a.stateWriter == writer {
				a.refresh()
			}
		})
	})
	return writer
}

func (a *mobileApp) draftSaveFailed() bool {
	if a.draftEncodingFailed {
		return true
	}
	if a.stateWriter == nil {
		return false
	}
	_, err := a.stateWriter.Result()
	return err != nil
}

func (a *mobileApp) save() error {
	if a.stateWriter == nil {
		return nil
	}
	for _, state := range a.model.DeviceStates {
		state.Readiness = redactRuntimeReadiness(state.Readiness)
	}
	data, err := a.model.EncodeState()
	if a.draftEncodingFailed != (err != nil) {
		a.draftEncodingFailed = err != nil
		a.refresh()
	}
	if err != nil {
		return err
	}
	a.stateWriter.Queue(data)
	return nil
}

func (a *mobileApp) flushDrafts() error {
	if err := a.save(); err != nil {
		return err
	}
	if a.stateWriter == nil {
		return nil
	}
	return a.stateWriter.FlushLatest()
}

func (a *mobileApp) identity() (ssh.Signer, error) {
	if a.signer != nil {
		return a.signer, nil
	}
	secrets := mygo.Secrets.Service("ai.shioji.mobile")
	const keyID = "execution-device-key"
	privateKey, err := secrets.Get(keyID)
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, err
	}
	if len(privateKey) == 0 {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := secrets.Set(keyID, key); err != nil {
			return nil, err
		}
		privateKey = key
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("钥匙串中的 SSH 密钥格式不正确")
	}
	a.signer, err = ssh.NewSignerFromKey(ed25519.PrivateKey(privateKey))
	return a.signer, err
}

func (a *mobileApp) connect(endpoint mobileclient.Endpoint) {
	signer, err := a.identity()
	if err != nil {
		a.setupError = err.Error()
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.releaseMarkdownImages("")
	a.retireAgentSignIns()
	a.queueSaves = nil
	a.model.Connected = false
	a.model.ConnectionPhase = app.ConnectionOpening
	a.model.ConnectionError = ""
	a.model.DevicePaths = nil
	var c *app.Controller
	if paths := a.authenticatedPaths(a.savedDevice(endpoint.DeviceID), signer); endpoint.DeviceID != "" && len(paths) != 0 {
		c = app.NewAuthenticatedDeviceController(a.directory, endpoint.DeviceID, endpoint.RouteID(), paths)
		c.AuthenticatedRuntimeArtifact = bundledWorkerRuntimeArtifact
		if path, err := bundledWorkerPath(); err == nil {
			if build, err := mobileclient.InspectWorkerArtifact(path); err == nil && build.Lifecycle == 1 {
				c.AuthenticatedWorkerBuild = &build
				c.AuthenticatedWorkerArtifact = bundledWorkerArtifact
			}
		}
	} else {
		c = app.NewController(a.directory)
		c.ExpectedDeviceID = endpoint.DeviceID
		route := a.routeDial()
		c.Dial = func(ctx context.Context) (net.Conn, error) { return endpoint.DialRoute(ctx, signer, route) }
	}
	c.RestoreOperations = a.model.RestoredSubmissionQueries()["local"]
	a.controller = c
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	c.SetInvalidate(func() {
		mygo.RunOnMain(func() {
			if a.controller == c {
				a.applyUpdates(c)
			}
		})
	})
	if a.model.SessionID != "" {
		c.Select(a.model.SessionID)
	}
	if a.parallel != "" {
		c.Do(app.Job{Method: "$visible", Input: []string{a.parallel}})
	}
	go c.Run(ctx)
}

func (a *mobileApp) applyUpdates(controller *app.Controller) {
	oldID := a.model.SessionID
	oldEpoch := a.model.ConnectionEpoch
	for {
		select {
		case update := <-controller.Updates:
			update(a.model)
		default:
			a.model.Readiness = redactRuntimeReadiness(a.model.Readiness)
			a.model.Snapshot.PendingRequests = mobileclient.LivePendingRequests(a.model)
			if a.parallelOwner != nil {
				a.model.Preferences = a.parallelOwner.model.Preferences
				a.parallelOwner.home.Invalidate()
			}
			if oldEpoch != a.model.ConnectionEpoch {
				a.releaseMarkdownImages("")
			}
			if oldID != "" && a.model.SessionID == "" && a.sessionByID(oldID).ID == "" {
				controller.Select("")
				if a.chat != nil && a.sessionOpening == nil {
					a.callUIKit("popPage", map[string]uintptr{"window": a.chat.NativeHandle()}, nil)
				}
			}
			if a.endpoint.DeviceID == "" && a.model.Connected && a.model.Snapshot.DeviceID != "" {
				a.endpoint.DeviceID = a.model.Snapshot.DeviceID
				a.storeEndpoint(a.endpoint)
				if e := a.rememberDevice(a.endpoint, true); e != nil {
					a.setupError = e.Error()
				}
			}
			if a.model.Connected && a.endpoint.DeviceID != "" && a.model.Snapshot.DeviceID == a.endpoint.DeviceID {
				a.splitRoot().observeNavigationOrder(a.model.Snapshot)
				a.splitRoot().updateArchivedBrowseScope(a.model.Snapshot)
			}
			if a.model.SessionID != oldID && a.model.SessionID != "" {
				controller.Select(a.model.SessionID)
				if a.draftPage != nil {
					draft := a.draftPage
					id := a.model.SessionID
					a.goalEntranceSession = a.model.WorkKey(id, "")
					draft.OnClosed(func() { a.openSession(id) })
					if isTablet() {
						a.callUIKit("dismissForm", map[string]uintptr{"window": draft.NativeHandle()}, nil)
					} else {
						draft.Pop()
					}
				}
			}
			a.resumeAgentSignIns()
			a.reconcileQueueEdits()
			a.save()
			a.saveCatalog()
			a.deliverNotifications()
			a.completeSessionListOpening()
			a.syncSessionStops()
			a.updateChatHeader()
			a.refresh()
			return
		}
	}
}

func (a *mobileApp) saveCatalog() {
	if a.catalogWriter == nil || !a.model.Connected || a.endpoint.DeviceID == "" {
		return
	}
	snapshot := a.model.Snapshot
	if snapshot.DeviceID != a.endpoint.DeviceID ||
		snapshot.Generation == a.cachedGeneration && a.model.Sequence == a.cachedSequence {
		return
	}
	cached := cachedCatalog{Snapshot: snapshot, Readiness: redactRuntimeReadiness(a.model.Readiness)}
	cached.Snapshot.Operations = nil
	cached.Snapshot.Sequence = a.model.Sequence
	if data, err := json.Marshal(cached); err == nil {
		a.cachedGeneration, a.cachedSequence = snapshot.Generation, cached.Snapshot.Sequence
		a.catalogWriter.Queue(data)
	}
}

func (a *mobileApp) storeEndpoint(endpoint mobileclient.Endpoint) bool {
	if a.directory == "" {
		a.setupError = "无法保存设备配置"
		return false
	}
	data, err := json.Marshal(endpoint)
	if err == nil {
		path := filepath.Join(a.directory, "device.json")
		temp := path + ".tmp"
		err = os.WriteFile(temp, data, 0600)
		if err == nil {
			err = os.Rename(temp, path)
		}
	}
	if err != nil {
		a.setupError = err.Error()
		return false
	}
	a.endpoint = endpoint
	return true
}

func (a *mobileApp) submit(steer bool, editor *mobileDraftEditor) {
	if a.draftSaveFailed() {
		return
	}
	m := a.model
	target := a.draftTarget()
	parallel := editor != nil && editor.target.session != target.session
	if parallel {
		target = a.parallelDraftTarget()
	}
	if editor != nil && (editor.model != m || target.key == "" || editor.target != target || editor.pending()) {
		return
	}
	if editor != nil && editor.Publish(m, target.key) {
		a.save()
	}
	if a.tryCompactionCommand(editor) {
		return
	}
	if session := a.sessionByID(target.session); session.ID != "" {
		compact := a.compaction(session)
		if compact.phase == mobileCompactionSending && !session.Status.Active() {
			m.Error = "压缩操作接纳后即可发送"
			return
		}
		if work := compact.action(session); steer && work != nil && work.State.Active() {
			m.Error = "压缩期间可以将消息加入待发队列"
			return
		}
	}
	begin := m.BeginMessage
	if parallel {
		begin = func(steer bool) (domain.Submission, string, string, error) {
			return m.BeginMessageInSession(target.session, steer)
		}
	}
	in, method, turn, err := begin(steer)
	if err != nil {
		m.Error = err.Error()
		return
	}
	if editor != nil {
		switch method {
		case "turn.enqueue":
			editor.queueMotion.arrival, editor.queueMotion.arrivalSource = in.OperationID, editor.inputBounds
		case "turn.steer", "session.submit":
			editor.sendScroll.begin(in.OperationID, m.ConnectionEpoch, editor.inputBounds, method == "turn.steer", false)
			if in.SessionID == "" {
				motion := editor.sendScroll
				motion.workspace = in.WorkspaceID
				a.newSessionSend = &motion
				editor.sendScroll.operation = ""
			}
		}
	}
	if a.previewConversationMotion != "" {
		a.previewAcceptMotion(in, method, turn)
		return
	}
	a.deliverSubmission(in, method, turn)
}

func (a *mobileApp) deliverSubmission(in domain.Submission, method, turn string) {
	var input any = in
	if method == "turn.steer" {
		input = protocol.SteerInput{Input: in, TurnID: turn}
	}
	a.deliverInput(in, method, input, "消息未发送，草稿已保留")
}

func (a *mobileApp) deliverInput(in domain.Submission, method string, input any, unsent string) {
	m := a.model
	a.splitRoot().operationHistory.AcknowledgeSubmissionFailures(app.SessionLocation{
		DeviceID: a.endpoint.DeviceID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
	})
	if err := a.save(); err != nil {
		m.Reject(in, unsent)
		a.refresh()
		return
	}
	job := app.Job{Method: method, Input: input, ConnectionEpoch: m.ConnectionEpoch, Apply: func(m *app.Model, data json.RawMessage, err error) {
		var operation domain.Operation
		if err == nil {
			err = json.Unmarshal(data, &operation)
		}
		if err != nil {
			m.SubmissionError(in, err)
		} else {
			m.Accepted(in, operation)
		}
	}}
	controller, writer := a.controller, a.stateWriter
	if controller == nil || writer == nil {
		m.Reject(in, "执行设备暂时无法接纳提交")
		a.save()
		a.refresh()
		return
	}
	go func() {
		err := writer.FlushLatest()
		mygo.RunOnMain(func() {
			switch {
			case err != nil:
				m.Reject(in, unsent)
			case a.controller != controller || !m.Connected || !controller.Do(job):
				m.Reject(in, "执行设备暂时无法接纳提交")
			default:
				return
			}
			a.save()
			a.refresh()
		})
	}()
}
