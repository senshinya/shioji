package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/portforward"
)

func (a *mobileApp) forwardingDevice(id string) (savedMobileDevice, bool) {
	for _, device := range a.savedDevices.Devices {
		if id != "" && device.Endpoint.DeviceID == id {
			return device, true
		}
	}
	return savedMobileDevice{}, false
}

func (a *mobileApp) forwardDial(id string) portforward.Dial {
	root := a.splitRoot()
	type routePlan struct {
		device    savedMobileDevice
		preferred mobileclient.Endpoint
		signer    ssh.Signer
		tailnet   mobileclient.RouteDial
	}
	return func(ctx context.Context, target portforward.Target) (net.Conn, error) {
		// Resolve current enrolled routes per stream. The mapping keeps its
		// device ID even when a different session or device becomes visible.
		plan := make(chan routePlan, 1)
		mygo.RunOnMain(func() {
			device, _ := root.forwardingDevice(id)
			preferred := device.Endpoint
			if root.endpoint.DeviceID == id {
				preferred = root.activeEndpoint()
			} else if pane := root.parallelDevice; pane != nil && pane.endpoint.DeviceID == id {
				preferred = pane.activeEndpoint()
			}
			plan <- routePlan{device, preferred, root.signer, root.routeDial()}
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case current := <-plan:
			if current.signer == nil || current.device.Endpoint.DeviceID != id {
				return nil, errors.New("转发设备的 SSH 连接不可用")
			}
			return mobileclient.OpenDeviceTCP(ctx, current.device, current.preferred, current.signer, current.tailnet, target)
		}
	}
}

func (a *mobileApp) openPortForwards(parent *mygo.Window) {
	deviceID := a.endpoint.DeviceID
	if deviceID == "" && len(a.savedDevices.Devices) != 0 {
		deviceID = a.savedDevices.Devices[0].Endpoint.DeviceID
	}
	host, port, local := "127.0.0.1", "", ""
	if a.preview {
		port, local = "4173", "4173"
	}
	problem, pending, retrying := "", "", ""
	var w *mygo.Window
	create := func() {
		if pending != "" || !a.pageCurrent(w) {
			return
		}
		targetPort, _ := strconv.Atoi(strings.TrimSpace(port))
		target, err := (portforward.Target{Host: strings.TrimSpace(host), Port: targetPort}).Normalize()
		if err != nil {
			problem = err.Error()
			return
		}
		localPort := target.Port
		if entered := strings.TrimSpace(local); entered != "" {
			localPort, err = strconv.Atoi(entered)
			if err != nil || localPort < 1 || localPort > 65535 {
				problem = "本机端口须介于 1 和 65535"
				return
			}
		}
		manager := a.forwards
		device, known := a.forwardingDevice(deviceID)
		if manager == nil || !known {
			problem = "请选择已添加的远程设备"
			return
		}
		req := portforward.Request{ID: domain.NewID(), DeviceID: deviceID, Name: device.Endpoint.Address,
			Target: target, LocalPort: localPort}
		pending, problem = req.ID, ""
		dial := a.forwardDial(deviceID)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		unregister := w.OnClosed(cancel)
		go func() {
			mapping, err := manager.Create(ctx, req, dial)
			mygo.RunOnMain(func() {
				defer cancel()
				defer unregister()
				if !a.pageCurrent(w) {
					if mapping.ID == req.ID {
						go manager.CloseID(req.ID)
					}
					return
				}
				pending = ""
				if err != nil {
					problem = err.Error()
				}
				w.Invalidate()
			})
		}()
	}
	w = a.page("端口转发", func(c *ui.Context) {
		mobilePageBody(c, func() {
			device, available := a.forwardingDevice(deviceID)
			deviceName := "选择设备"
			if available {
				deviceName = mobileclient.DeviceLabel(device.Name, device.Endpoint.Address)
			}
			mobileSection(c, "新建")
			formChoice(c.Key("device"), "转发设备", deviceName, func(m *ui.Menu) {
				for _, choice := range a.savedDevices.Devices {
					id := choice.Endpoint.DeviceID
					if id != "" {
						menuChoice(m, mobileclient.DeviceLabel(choice.Name, choice.Endpoint.Address), id == deviceID, func() { deviceID = id })
					}
				}
			}).Disabled(pending != "")
			formRow(c.Key("target-host"), "远程监听地址", func() {
				formInput(c, "远程监听地址", "127.0.0.1", &host).Disabled(pending != "")
			})
			formRow(c.Key("target-port"), "远程端口", func() {
				formInput(c, "远程端口", "4173", &port).Disabled(pending != "")
			})
			formRow(c.Key("local-port"), "本机端口", func() {
				formInput(c, "本机端口", "同远程端口", &local).Disabled(pending != "")
			})
			if problem != "" {
				mobileMessage(c, problem)
			}
			title := "建立转发"
			if pending != "" {
				title = "正在建立…"
			}
			primaryAction(c.Key("create"), title, create).Margin(pageInset, 0, 0).
				Disabled(pending != "" || deviceID == "")
			mobileGroupHeading(c, "当前转发")
			var mappings []portforward.Mapping
			if a.forwards != nil {
				mappings = a.forwards.Snapshot()
			}
			for _, mapping := range mappings {
				a.mobileForwardRow(c.Key(mapping.ID), w, mapping, retrying == mapping.ID, func() {
					if retrying != "" {
						return
					}
					retrying = mapping.ID
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
						_ = a.forwards.Retry(ctx, mapping.ID)
						cancel()
						mygo.RunOnMain(func() {
							retrying = ""
							if a.pageCurrent(w) {
								w.Invalidate()
							}
						})
					}()
				}, func(err error) {
					if err != nil {
						problem = err.Error()
						w.Invalidate()
					}
				})
			}
			if len(mappings) == 0 {
				mobileMessage(c, "暂无转发")
			}
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) mobileForwardRow(c *ui.Context, w *mygo.Window, mapping portforward.Mapping, retrying bool, retry func(), result func(error)) {
	state := map[string]string{"available": "可用", "connecting": "连接中", "unavailable": "无法连接", "closing": "正在关闭"}[mapping.State]
	if retrying {
		state = "正在重试"
	}
	address := mapping.LocalAddress()
	if mapping.LocalPort == 0 {
		address = "正在分配本机端口"
	}
	ui.Column(c).Children(func() {
		ui.Row(c.Key("mapping")).AlignItems(ui.Center).Children(func() {
			var open func()
			if mapping.State == "available" {
				open = func() { a.openManagedForward(w, mapping.ID, result) }
			}
			mobileDetailRow(c.Key("destination"), address, a.deviceLabel(mapping.DeviceID, mapping.Name)+" · "+mapping.Target.Address(),
				"打开转发："+address, nil, open, nil).Grow(1).MinWidth(0)
			if mapping.State == "unavailable" {
				action(c.Key("retry"), "重试", retry).PaddingX(5).Disabled(retrying)
			}
			iconButton(c.Key("actions"), "转发操作："+address, moreSymbol, 17, nil).Menu(func(menu *ui.Menu) {
				if menu.Item("复制本机地址").Disabled(mapping.LocalPort == 0).Chosen() {
					mygo.Clipboard.WriteText(mapping.LocalAddress())
				}
				if menu.Item("复制目标地址").Chosen() {
					mygo.Clipboard.WriteText(mapping.Target.Address())
				}
			})
			iconButton(c.Key("stop"), "关闭转发："+address, closeSymbol, 16, func() {
				manager := a.forwards
				manager.CancelID(mapping.ID)
				go manager.CloseID(mapping.ID)
			}).Disabled(mapping.State == "closing")
		})
		if state != "" {
			label(c, state, captionTextSize, palette(c).secondary).Role(ui.RoleStatus).Padding(0, 0, 6)
		}
		if mapping.Error != "" && !retrying {
			mobileMessage(c, mapping.Error)
		}
	})
}

func (a *mobileApp) openManagedForward(w *mygo.Window, id string, result func(error)) {
	if a.forwardedPageOpen || !a.pageCurrent(w) {
		return
	}
	a.forwardedPageOpen = true
	ctx, cancel := context.WithCancel(context.Background())
	unregister := w.OnClosed(cancel)
	window, manager := w.NativeHandle(), a.forwards
	go func() {
		err := showManagedForward(ctx, window, manager, id, "")
		mygo.RunOnMain(func() {
			defer cancel()
			defer unregister()
			a.forwardedPageOpen = false
			if ctx.Err() == nil && a.pageCurrent(w) {
				result(err)
			}
		})
	}()
}

func showManagedForward(ctx context.Context, window uintptr, manager *portforward.Manager, id, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var address string
	if err := manager.Open(id, destination, func(value string) error {
		address = value
		return nil
	}); err != nil {
		return err
	}
	return uikit.Call(ctx, "openForwardedPage", map[string]any{"window": window, "url": address}, nil)
}
