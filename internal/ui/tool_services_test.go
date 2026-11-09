package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strconv"
	"testing"
	"time"
)

func serviceUI(t *testing.T) (*View, *deviceAgentEditor, *n.Tester) {
	m := deviceUIModel()
	m.DeviceStates["local"].WorkerMinor = 22
	v := New(m, app.NewController(""), "")
	info := setupCatalog()[0]
	info.Capabilities.ToolServices = true
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	e.setup = false
	e.tab = "MCP"
	v.devices = true
	v.deviceSelected = "local"
	v.toolServicesFor("local", info.ID, e).open = true
	e.workerMinor = 22
	ui := n.NewTester(func(c *n.Context) {
		v.frameTime = c.Now()
		v.syncToolServiceAuthorizations(c)
		n.Column(c).Width(560).Height(560).Children(func() { v.agentToolServices(c, c.Theme(), "local", e, e.catalog[0], false) })
	}, 850, 700)
	ui.Advance(time.Second)
	return v, e, ui
}
func TestToolServiceRefreshKeepsRowsAndRejectsOtherConnectionEpoch(t *testing.T) {
	v, _, ui := serviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	catalog := domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "docs", Name: "Documentation", State: domain.ToolServiceReady, Tools: 2}}, CanRefresh: true}
	body, _ := json.Marshal(catalog)
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("Documentation") {
		t.Fatal(ui.Texts())
	}
	if err := ui.Click("刷新 MCP"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job = welcomeJob(t, v.Controller, "runtime.tools.refresh")
	ui.Frame()
	if !ui.HasText("Documentation") {
		t.Fatal("refresh cleared existing rows")
	}
	v.Model.DeviceStates["local"].ConnectionEpoch = "another"
	catalog.Services[0].Name = "Late stale result"
	body, _ = json.Marshal(catalog)
	job.Apply(v.Model, body, nil)
	ui.Frame()
	if ui.HasText("Late stale result") {
		t.Fatal("late query leaked into new connection")
	}
}
func TestToolServiceAuthorizationStartsOnceAndQueriesLostReply(t *testing.T) {
	v, _, ui := serviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	catalog := domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "private", Name: "Private", State: domain.ToolServiceAuthorizationRequired, CanAuthorize: true}}, CanRefresh: true}
	body, _ := json.Marshal(catalog)
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("授权 Private"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job = welcomeJob(t, v.Controller, "runtime.tools.auth.start")
	job.Apply(v.Model, nil, assertConnectionLost{})
	ui.Frame()
	ui.Advance(2 * time.Second)
	query := welcomeJob(t, v.Controller, "runtime.tools.auth.status")
	if query.Input == nil {
		t.Fatal("missing status query")
	}
	for len(v.Controller.Jobs) > 0 {
		if next := <-v.Controller.Jobs; next.Method == "runtime.tools.auth.start" {
			t.Fatal("lost start was replayed")
		}
	}
}

type assertConnectionLost struct{}

func (assertConnectionLost) Error() string { return "connection lost" }

func TestToolServiceRowsKeepBaselineWhenActionsAppear(t *testing.T) {
	v, _, ui := serviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	catalog := domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "docs", Name: "Documentation", State: domain.ToolServiceReady, Tools: 2}, {ID: "private", Name: "Private", State: domain.ToolServiceAuthorizationRequired, CanAuthorize: true}}}
	b, _ := json.Marshal(catalog)
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	first, _ := ui.Find("Documentation")
	second, _ := ui.Find("Private")
	if delta := second.Y - first.Y; delta < 39 || delta > 41 {
		t.Fatal("actions move the text baseline", first, second)
	}
}

func TestToolAuthorizationCompletionListsWithoutOptionalReload(t *testing.T) {
	v, e, ui := serviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	body, _ := json.Marshal(domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "private", Name: "Private", State: domain.ToolServiceAuthorizationRequired, CanAuthorize: true}}})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	u := v.toolServicesFor("local", e.openID, e)
	u.authorization = &domain.ToolServiceAuthorization{ID: "flow", HarnessID: e.openID, ServiceID: "private", Phase: domain.SignInWaiting}
	v.devices = true
	v.deviceSelected = "local"
	v.applyToolAuthorization(u, domain.ToolServiceAuthorization{ID: "flow", HarnessID: e.openID, ServiceID: "private", Phase: domain.SignInCompleted})
	ui.Frame()
	select {
	case got := <-v.Controller.Jobs:
		if got.Method != "runtime.tools.list" {
			t.Fatal("unsupported refresh after successful authorization", got.Method)
		}
	default:
		t.Fatal("authorization did not recheck discovery")
	}
}

func TestToolServicesUseAcceptedConfigurationAndInvalidateOldResults(t *testing.T) {
	v, e, ui := serviceUI(t)
	old := v.toolServicesFor("local", e.openID, e)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	e.options[e.openID] = domain.RuntimeOptions{Directory: "/pending/config"}
	e.draft = newRuntimeOptionDraft(e.options[e.openID])
	if toolServiceDraftCurrent(e, e.openID) {
		t.Fatal("onboarding's staged configuration mistaken for accepted runtime")
	}
	e.catalog[0].Installation.Options = e.options[e.openID]
	next := v.toolServicesFor("local", e.openID, e)
	if next == old {
		t.Fatal("accepted configuration changed but old MCP state retained")
	}
	b, _ := json.Marshal(domain.ToolServiceCatalog{Services: []domain.ToolService{{ID: "stale", Name: "Stale server"}}})
	job.Apply(v.Model, b, nil)
	ui.Frame()
	if ui.HasText("Stale server") {
		t.Fatal("previous runtime populated new configuration")
	}
}

func TestToolServicesAbsentFromWelcomeAndAddDeviceConfiguration(t *testing.T) {
	for _, add := range []bool{false, true} {
		v, e, _ := serviceUI(t)
		e.setup = true
		e.tab = "常规"
		f := &welcomeFlow{key: "local", agents: e, width: 560, step: welcomeAgentOptions}
		v.welcome = f
		ui := n.NewTester(func(c *n.Context) {
			n.Column(c).Width(560).Children(func() { v.welcomeAgentOptions(c, c.Theme(), f, add) })
		}, 1000, 820)
		ui.Advance(time.Second)
		if ui.HasText("MCP") {
			t.Fatal("MCP exposed during device onboarding", add, ui.Texts())
		}
	}
}

func TestManyToolServicesStayInBoundedScrollableList(t *testing.T) {
	v, _, ui := serviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	catalog := domain.ToolServiceCatalog{}
	for i := 0; i < 24; i++ {
		name := "Server " + strconv.Itoa(i)
		catalog.Services = append(catalog.Services, domain.ToolService{ID: name, Name: name, State: domain.ToolServiceReady, Tools: 2})
	}
	body, _ := json.Marshal(catalog)
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	viewport, ok := ui.Find("MCP 服务列表")
	if !ok || viewport.H < 300 || viewport.H > 560 {
		t.Fatal("MCP list does not use its available tab height", viewport)
	}
	ui.Scroll(viewport.X+30, viewport.Y+30, 0, 2000)
	ui.Advance(time.Second)
	last, ok := ui.Find("Server 23")
	if !ok || last.Y >= viewport.Y+viewport.H || last.Y < viewport.Y-1 {
		t.Fatal("cannot reach the last service inside the list", viewport, last)
	}
}
