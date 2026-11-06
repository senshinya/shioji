package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if !*noStart {
			if err := app.EnsureWorker(ctx, *dir, *workerBin); err != nil {
				controller.Updates <- func(m *app.Model) { m.Error = err.Error() }
			}
		}
		controller.Run(ctx)
	}()
	mygo.App.SetName("Shioji")
	mygo.Theme.SetSource(mygo.ThemeSource(model.Preferences.Appearance))
	mygo.App.WhenReady(func() {
		view := presentation.New(model, controller, *state)
		settings := presentation.NewSettings(view)
		view.OpenSettings = settings.Show
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Shioji", Width: 1240, Height: 820, MinWidth: 850, MinHeight: 600, TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: presentation.WindowTrafficLightPosition(), Content: n.View(view.View)})
		view.AttachWindow(window, settings)
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Shioji", Submenu: []*mygo.MenuItem{
				{Label: "关于 Shioji", Click: func(*mygo.MenuItem, *mygo.Window) { settings.OpenAbout() }},
				mygo.Separator(),
				{Label: "设置…", Accelerator: "Cmd+,", Click: func(*mygo.MenuItem, *mygo.Window) { settings.Show() }},
				mygo.Separator(), {Role: mygo.RoleServices}, {Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(), {Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleFileMenu}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleViewMenu}, {Role: mygo.RoleWindowMenu},
		})
		mygo.App.SetMenu(menu)
		window.OnClose(func(e *mygo.CloseEvent) {
			if err := view.Flush(); err != nil {
				e.PreventDefault()
				model.Error = "无法保存草稿：" + err.Error()
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if err := view.Flush(); err != nil {
				e.PreventDefault()
				mygo.Dialog.Error("无法保存草稿", err.Error())
			}
		})
		window.OnClosed(func() {
			settings.Close()
			if view.Saver != nil {
				view.Saver.Close()
			}
			cancel()
		})
	})
	return mygo.App.Run()
}
