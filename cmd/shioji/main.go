package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/appupdate"
	"shioji/internal/permissionflow"
	"shioji/internal/transport"
	presentation "shioji/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, "Library", "Application Support", "Shioji")
	dir := flag.String("worker-state", filepath.Join(base, "worker"), "Local worker directory")
	state := flag.String("client-state", filepath.Join(base, "client", "state.json"), "Client state file")
	exe, _ := os.Executable()
	workerBin := flag.String("worker-bin", filepath.Join(filepath.Dir(exe), "shioji-worker"), "Bundled local worker executable")
	noStart := flag.Bool("no-start-worker", false, "Connect without starting a worker")
	networkBin := flag.String("network-bin", "", "Signed network host for an isolated development profile")
	flag.Parse()
	model := app.NewModel()
	if err = model.Load(*state); err != nil {
		model.Error = "无法读取草稿：" + err.Error()
	}
	restored := map[string][]string{}
	for id := range model.Pending {
		key := model.PendingDevices[id]
		if key == "" {
			key = "local"
		}
		restored[key] = append(restored[key], id)
	}
	controller := app.NewDeviceController(*dir, model.LocalDeviceID, model.Connections, *workerBin, restored)
	controller.SetWorkerAutoUpdate(!*noStart)
	nodeDir := filepath.Join(filepath.Dir(*state), "tailnet")
	clientDone, err := transport.RegisterNetworkClient(nodeDir)
	if err != nil {
		return err
	}
	defer clientDone()
	controller.AttachTailnet(transport.NewNetworkClient(nodeDir, *workerBin, *networkBin))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		if !*noStart {
			if err := app.EnsureWorker(ctx, *dir, *workerBin); err != nil {
				select {
				case controller.Updates <- func(m *app.Model) { m.Error = err.Error() }:
				case <-ctx.Done():
				}
			}
		}
		controller.Run(ctx)
	}()
	mygo.App.SetName("Shioji")
	appupdate.Configure(func(session updater.Session) { presentation.OpenAppUpdate(model, session) })
	mygo.Theme.SetSource(mygo.ThemeSource(model.Preferences.Appearance))
	mygo.App.WhenReady(func() {
		view := presentation.New(model, controller, *state)
		settings := presentation.NewSettings(view)
		settings.AppUpdates = &presentation.AppUpdateControls{Enabled: mygo.Updater.Enabled, Check: updater.CheckForUpdates, AutomaticChecks: updater.AutomaticChecks, SetAutomaticChecks: updater.SetAutomaticChecks}
		offUpdates := updater.OnChange(settings.Invalidate)
		permissionGuide := permissionflow.New()
		settings.AuthorizeFullDiskAccess = permissionGuide.Open
		settings.FullDiskAccessGranted = permissionflow.FullDiskAccessGranted
		settings.BackgroundRuntimeAvailable = permissionGuide.RuntimeAvailable
		view.OpenSettings = settings.Show
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Shioji", Width: 1240, Height: 820, MinWidth: 850, MinHeight: 600, TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: presentation.WindowTrafficLightPosition(), Content: n.View(view.View)})
		view.AttachWindow(window, settings)
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Shioji", Submenu: []*mygo.MenuItem{
				{Label: "关于 Shioji", Click: func(*mygo.MenuItem, *mygo.Window) { settings.OpenAbout() }},
				updater.MenuItem(),
				mygo.Separator(),
				{Label: "设置…", Accelerator: "Cmd+,", Click: func(*mygo.MenuItem, *mygo.Window) { settings.Show() }},
				mygo.Separator(), {Role: mygo.RoleServices}, {Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(), {Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleFileMenu}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleViewMenu}, {Role: mygo.RoleWindowMenu},
		})
		mygo.App.SetMenu(menu)
		quitting := false
		closed := false
		var shutdown quitBarrier
		mygo.App.OnActivate(func(_ bool) {
			if !quitting && !closed && !window.IsVisible() {
				window.Show()
				window.Focus()
			}
		})
		window.OnClose(func(e *mygo.CloseEvent) {
			if err := view.Flush(); err != nil {
				e.PreventDefault()
				model.Error = "无法保存草稿：" + err.Error()
				quitting = false
				return
			}
			if items, _ := controller.ForwardSnapshot(); !quitting && len(items) > 0 {
				e.PreventDefault()
				window.Hide()
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if shutdown.started {
				return
			}
			if !closed {
				if err := view.Flush(); err != nil {
					e.PreventDefault()
					mygo.Dialog.Error("无法保存草稿", err.Error())
					return
				}
			}
			quitting = true
			permissionGuide.Close()
			offUpdates()
		})
		mygo.App.OnWillQuit(func(e *mygo.QuitEvent) {
			if !shutdown.request(cancel, controllerDone, mygo.App.Quit) {
				e.PreventDefault()
			}
		})
		window.OnClosed(func() {
			closed = true
			view.CloseTransient()
			settings.Close()
			if view.Saver != nil {
				view.Saver.Close()
			}
			cancel()
		})
	})
	err = mygo.App.Run()
	cancel()
	<-controllerDone
	return err
}
