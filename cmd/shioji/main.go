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
	controller := app.NewController(*dir)
	controller.ExpectedDeviceID = model.KnownDeviceID
	for id := range model.Pending {
		controller.RestoreOperations = append(controller.RestoreOperations, id)
	}
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
	mygo.App.WhenReady(func() {
		view := presentation.New(model, controller, *state)
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Shioji", Width: 1240, Height: 820, MinWidth: 850, MinHeight: 600, TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: &mygo.Point{X: 22, Y: 21}, Content: n.View(view.View)})
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
			if view.Saver != nil {
				view.Saver.Close()
			}
			cancel()
		})
	})
	return mygo.App.Run()
}
