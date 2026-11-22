package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
)

type savedMobileDevice = mobileclient.SavedDevice

type savedMobileDevices struct {
	Active  string              `json:"active"`
	Devices []savedMobileDevice `json:"devices"`
}

func (a *mobileApp) loadSavedDevices() {
	data, err := os.ReadFile(filepath.Join(a.rootDirectory, "devices.json"))
	if err == nil {
		err = json.Unmarshal(data, &a.savedDevices)
	}
	if err != nil && !os.IsNotExist(err) {
		a.setupError = err.Error()
		return
	}
	if os.IsNotExist(err) {
		if a.endpoint.Address != "" && a.endpoint.DeviceID != "" {
			a.savedDevices.Active = a.endpoint.DeviceID
			a.savedDevices.Devices = []savedMobileDevice{{Endpoint: a.endpoint}}
		}
		return
	}
	for _, d := range a.savedDevices.Devices {
		if d.Endpoint.DeviceID != a.savedDevices.Active {
			continue
		}
		path, err := a.deviceDirectory(d)
		if err != nil {
			a.setupError = err.Error()
			return
		}
		a.directory = path
		a.endpoint = d.Endpoint
		a.model = a.model.NewDeviceSelection()
		a.loadDeviceFiles()
		return
	}
}

func (a *mobileApp) deviceDirectory(d savedMobileDevice) (string, error) {
	dir, err := mobileDeviceDirectory(a.rootDirectory, d.Slot)
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0700)
}

func mobileDeviceDirectory(root, slot string) (string, error) {
	if slot == "" {
		return root, nil
	}
	if filepath.Base(slot) != slot || slot == "." || slot == ".." {
		return "", fmt.Errorf("保存的设备目录无效")
	}
	return filepath.Join(root, "devices", slot), nil
}

func (a *mobileApp) rememberDevice(endpoint mobileclient.Endpoint, activate bool) error {
	if a.rootDirectory == "" || endpoint.DeviceID == "" {
		return fmt.Errorf("尚未确认设备身份")
	}
	next := a.savedDevices
	next.Devices = append([]savedMobileDevice(nil), next.Devices...)
	found := false
	for i, entry := range next.Devices {
		if entry.Endpoint.DeviceID == endpoint.DeviceID {
			merged, err := entry.WithRoute(endpoint)
			if err != nil {
				return err
			}
			next.Devices[i] = merged
			found = true
			break
		}
	}
	if !found {
		slot := domain.NewID()
		if len(next.Devices) == 0 && a.directory == a.rootDirectory &&
			(a.endpoint.DeviceID == "" || a.endpoint.DeviceID == endpoint.DeviceID) {
			slot = ""
		}
		d := savedMobileDevice{Slot: slot, Endpoint: endpoint}
		if _, err := a.deviceDirectory(d); err != nil {
			return err
		}
		next.Devices = append(next.Devices, d)
	}
	if activate {
		next.Active = endpoint.DeviceID
	}
	return a.saveDeviceRegistry(next)
}

func (a *mobileApp) saveDeviceRegistry(next savedMobileDevices) error {
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := app.WriteState(filepath.Join(a.rootDirectory, "devices.json"), encoded); err != nil {
		return err
	}
	a.savedDevices = next
	return nil
}

func (a *mobileApp) switchSavedDevice(endpoint mobileclient.Endpoint) error {
	a.collectOperationFeedback()
	if endpoint.DeviceID != a.endpoint.DeviceID {
		if err := a.flushDrafts(); err != nil {
			return fmt.Errorf("当前设备的草稿尚未保存，请重试保存后再切换")
		}
		if err := a.closeParallelDevice(); err != nil {
			return err
		}
	}
	if err := a.rememberDevice(endpoint, true); err != nil {
		return err
	}
	if endpoint.DeviceID == a.endpoint.DeviceID || a.endpoint.DeviceID == "" && a.directory == a.rootDirectory {
		if a.endpoint.DeviceID != endpoint.DeviceID {
			a.deviceSelection++
		}
		saved := a.savedDevice(endpoint.DeviceID)
		a.endpoint = saved.Endpoint
		if a.controller != nil && a.controller.ExpectedDeviceID == endpoint.DeviceID &&
			a.controller.UpdateAuthenticatedDevicePaths(a.authenticatedPaths(saved, a.signer)) {
			a.refresh()
			return nil
		}
		a.connect(a.endpoint)
		return nil
	}
	for _, d := range a.savedDevices.Devices {
		if d.Endpoint.DeviceID != endpoint.DeviceID {
			continue
		}
		path, err := a.deviceDirectory(d)
		if err != nil {
			return err
		}
		a.closeConversationFind()
		a.suspendParallelFind()
		a.cacheSessionCatalog()
		a.cancelSessionListOpening()
		a.requestReveal = mobileRequestReveal{}
		if a.cancel != nil {
			a.cancel()
		}
		a.retireAgentSignIns()
		a.controller = nil
		if a.stateWriter != nil {
			a.stateWriter.Close()
		}
		if a.catalogWriter != nil {
			a.catalogWriter.Close()
		}
		prefs := a.model.Preferences
		a.deviceSelection++
		a.directory, a.endpoint = path, d.Endpoint
		a.model = a.model.NewDeviceSelection()
		a.loadDeviceFiles()
		a.model.Preferences = prefs
		a.stateWriter = a.newDraftWriter()
		a.catalogWriter = app.NewStateWriter(filepath.Join(path, "catalog.json"))
		a.cachedGeneration, a.cachedSequence = "", 0
		a.replaceReview(nil)
		a.parallel = ""
		a.history = map[string]*ui.ListState{}
		a.connect(endpoint)
		a.refreshSessionCatalogs()
		a.save()
		a.refresh()
		return nil
	}
	return fmt.Errorf("设备连接不存在")
}

// Results pages can stay on the navigation stack across the device selection
// they initiate. Other pages keep their original device scope.
func (a *mobileApp) openDeviceFromResults(w *mygo.Window, id string) error {
	if id == a.endpoint.DeviceID {
		return nil
	}
	for _, device := range a.savedDevices.Devices {
		if device.Endpoint.DeviceID == id {
			if err := a.switchSavedDevice(device.Endpoint); err != nil {
				return err
			}
			a.pageDevice[w] = a.deviceSelection
			return nil
		}
	}
	return fmt.Errorf("此执行设备已移除")
}

func (a *mobileApp) savedDevice(id string) savedMobileDevice {
	for _, device := range a.savedDevices.Devices {
		if device.Endpoint.DeviceID == id {
			return device
		}
	}
	return savedMobileDevice{Endpoint: a.endpoint}
}

func (a *mobileApp) authenticatedPaths(device savedMobileDevice, signer ssh.Signer) []app.AuthenticatedDevicePath {
	var paths []app.AuthenticatedDevicePath
	route := a.routeDial()
	for _, endpoint := range device.Endpoints() {
		if endpoint.DeviceID != device.Endpoint.DeviceID || endpoint.Validate() != nil {
			continue
		}
		host, port, _ := net.SplitHostPort(endpoint.Address)
		number, _ := strconv.Atoi(port)
		paths = append(paths, app.AuthenticatedDevicePath{
			ID: endpoint.RouteID(), Endpoint: transport.SSHEndpoint{User: endpoint.User, Host: host, Port: number},
			Dial:   func(ctx context.Context) (net.Conn, error) { return endpoint.DialRoute(ctx, signer, route) },
			Worker: endpoint.WorkerSSH(signer, route),
		})
	}
	return paths
}

func (a *mobileApp) activeEndpoint() mobileclient.Endpoint {
	for _, state := range a.model.DevicePaths {
		if state.Phase != app.PathActive {
			continue
		}
		for _, endpoint := range a.savedDevice(a.endpoint.DeviceID).Endpoints() {
			if endpoint.RouteID() == state.ID {
				return endpoint
			}
		}
	}
	return a.endpoint
}

func (a *mobileApp) connectionName() string {
	if a.activeEndpoint().TailnetPeerID != "" {
		return "Tailnet"
	}
	return "直接 SSH"
}

func (a *mobileApp) tailnetObservation(endpoint mobileclient.Endpoint, active bool, now time.Time) mobileclient.TailnetObservation {
	return mobileclient.TailnetRouteObservation(endpoint, a.tailnetStatus, a.tailnetLastPeers, active, now)
}

func (a *mobileApp) routeDial() mobileclient.RouteDial {
	if a.tailnetNode == nil {
		return nil
	}
	return a.tailnetNode.DialPeer
}

func (a *mobileApp) startTailnet() {
	if a.preview || a.rootDirectory == "" || a.tailnetNode != nil {
		return
	}
	node := tailnet.NewOutboundNode(filepath.Join(a.rootDirectory, "Tailnet"), func() ([]byte, error) {
		return mygo.Secrets.Service("ai.shioji.mobile").Get("tailnet-identity-key")
	})
	a.tailnetNode = node
	if a.controller != nil {
		a.controller.UpdateAuthenticatedDevicePaths(a.authenticatedPaths(a.savedDevice(a.endpoint.DeviceID), a.signer))
	}
	go func() {
		node.Run(context.Background(), func(snapshot tailnet.Snapshot) {
			mygo.RunOnMain(func() { a.applyTailnetSnapshot(snapshot) })
		})
		mygo.RunOnMain(func() {
			a.tailnetNode, a.tailnetPending = nil, ""
			a.refresh()
		})
	}()
}

func (a *mobileApp) applyTailnetSnapshot(snapshot tailnet.Snapshot) {
	wasRunning := a.tailnetStatus.State == "Running"
	switch snapshot.State {
	case "Running":
		a.tailnetLastPeers = snapshot.Peers
	case "NotStarted":
		a.tailnetLastPeers = nil
	}
	a.tailnetStatus = snapshot
	if snapshot.State == "Running" && !wasRunning {
		a.refreshSessionCatalogs()
	}
	if snapshot.OperationID == a.tailnetPending {
		a.tailnetPending = ""
	}
	a.refresh()
}

func (a *mobileApp) openTailnet(parent *mygo.Window) { a.openTailnetPage(parent, nil) }

func (a *mobileApp) openTailnetPage(parent *mygo.Window, selectPeer func(tailnet.Peer)) {
	title := "Tailnet"
	if selectPeer != nil {
		title = "选择 Tailnet 设备"
	}
	message := ""
	refreshID := ""
	var w *mygo.Window
	send := func(kind string) {
		if a.tailnetPending != "" || a.tailnetNode == nil && (!a.preview || kind != "refresh") {
			return
		}
		hostname := "shioji-iphone"
		if isTablet() {
			hostname = "shioji-ipad"
		}
		id := domain.NewID()
		message = ""
		if !a.preview && !a.tailnetNode.Do(tailnet.Action{Kind: kind, Hostname: hostname, ID: id}) {
			message = "网络操作繁忙，请重试"
			w.Invalidate()
			return
		}
		a.tailnetPending = id
		if kind == "refresh" {
			refreshID = id
			time.AfterFunc(10*time.Second, func() {
				mygo.RunOnMain(func() {
					if a.tailnetPending == id {
						a.tailnetPending = ""
						message = "刷新未确认，请重试"
						a.refresh()
					}
				})
			})
		}
		if a.preview {
			time.AfterFunc(170*time.Millisecond, func() {
				mygo.RunOnMain(func() {
					if a.tailnetPending == id {
						updated := a.tailnetStatus
						updated.OperationID = id
						a.applyTailnetSnapshot(updated)
					}
				})
			})
		}
		w.Invalidate()
	}
	w = a.page(title, func(c *ui.Context) {
		n := a.tailnetStatus
		busy := a.tailnetPending != ""
		refreshing := busy && a.tailnetPending == refreshID
		mobilePageWithFooter(c, func() {
			deviceLabel := "Shioji iPhone"
			if isTablet() {
				deviceLabel = "Shioji iPad"
			}
			mobilePageContext(c, deviceLabel, "应用内节点")
			state := map[string]string{"Running": "已连接", "Stopped": "已暂停", "Offline": "网络离线",
				"NeedsLogin": "等待授权", "NeedsMachineAuth": "等待管理员批准", "Starting": "正在连接"}[n.State]
			if state == "" {
				state = "等待加入网络"
			}
			detail := "在浏览器完成授权后，将返回 Shioji 查看可见设备。"
			if n.State == "NeedsMachineAuth" {
				detail = "网络管理员批准这台设备后即可连接。"
			} else if n.Authorizing {
				detail = "在浏览器授权后，返回 Shioji 查看连接状态。"
			} else if n.Address != "" {
				detail = strings.Trim(n.Network+" · "+n.Hostname, " ·")
			}
			remoteStatus(c, networkSymbol, state, detail)
			if selectPeer == nil {
				mobileMetadataField(c.Key("local-tailnet-address"), "Tailnet 地址", n.Address, "复制本机 Tailnet 地址")
			}
			if n.State == "Running" || n.State == "Stopped" {
				active := n.State == "Running"
				mobileToggle(c, "保持连接", &active, func() {
					if active {
						send("resume")
					} else {
						send("pause")
					}
				})
			}
			if n.Error != "" {
				mobileMessage(c, n.Error)
			}
			if message != "" {
				mobileMessage(c, message)
			}
			if n.State == "Running" {
				ui.Row(c.Key("network-peers-heading")).MinHeight(mobileTapSize(c)).Margin(10, 0, 0).AlignItems(ui.Center).Children(func() {
					oneLine(c, "可见设备", captionTextSize, palette(c).secondary).FontWeight(640).Grow(1)
					button := iconButton(c.Key("refresh-peers"), "刷新网络设备", refreshSymbol, inlineIconSize, func() { send("refresh") }).
						Disabled(busy || a.tailnetNode == nil && !a.preview)
					if refreshing {
						button.Value("刷新中")
					}
				})
				if len(n.Peers) == 0 {
					mobileMessage(c, "当前网络没有可见设备。")
				}
				now := c.Now()
				for _, peer := range n.Peers {
					subtitle, status := peer.Address, "离线"
					var open func()
					if selectPeer == nil {
						open = func() { a.openTailnetPeer(w, peer.ID) }
					}
					if peer.Online {
						status = "在线"
						if selectPeer != nil {
							open = func() {
								if current, allowed := a.currentTailnetPeer(peer.ID); allowed {
									selectPeer(current)
									w.Pop()
								}
							}
						}
					}
					observed := mobileclient.TailnetPeerObservation(peer, true, now)
					if observed.Current {
						status = observed.Latency
						subtitle += " · " + observed.Path
					} else if observed.Measured {
						subtitle += " · " + observed.Latency
					}
					mobileDetailRow(c.Key(peer.ID), peer.Name, subtitle, peer.Name, deviceSymbol, open, func() {
						label(c, status, captionTextSize, palette(c).secondary).PassThrough()
					}).Value(subtitle + "，" + status)
				}
			}
		}, func() {
			switch {
			case busy && !refreshing:
				mobileMessage(c, "正在处理…")
			case n.Authorizing:
				if n.AuthURL != "" && n.State != "NeedsMachineAuth" {
					primaryAction(c, "在浏览器授权", func() {
						current := a.tailnetStatus
						if current.OperationID != n.OperationID || !current.Authorizing || current.AuthURL == "" || a.tailnetPending != "" {
							return
						}
						if err := mygo.Shell.OpenExternal(current.AuthURL); err != nil {
							message = "无法打开授权页面"
							w.Invalidate()
						}
					})
				}
				action(c, "取消加入", func() { send("cancel") }).Disabled(a.tailnetNode == nil)
			case n.State == "Running" || n.State == "Stopped":
				secondaryAction(c, "退出当前 Tailnet", func() {
					go func() {
						result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: w, Type: mygo.MessageWarning,
							Message: "退出 Tailnet？", Detail: "通过 Tailnet 建立的连接会断开。重新加入时需要授权。",
							Buttons: []string{"取消", "退出网络"}, DestructiveButtons: []int{1}})
						mygo.RunOnMain(func() {
							if err == nil && result.Button == 1 && a.pageCurrent(w) &&
								(a.tailnetStatus.State == "Running" || a.tailnetStatus.State == "Stopped") {
								send("logout")
							}
						})
					}()
				}).Disabled(a.tailnetNode == nil)
			case n.State == "Offline" && a.tailnetNode == nil:
				primaryAction(c, "重新启动节点", a.startTailnet)
			case n.State == "Offline":
				primaryAction(c, "恢复连接", func() { send("resume") }).Disabled(a.tailnetNode == nil)
			case n.State == "Starting" || n.State == "NeedsMachineAuth":
				action(c, "暂停连接", func() { send("pause") }).Disabled(a.tailnetNode == nil)
			default:
				primaryAction(c, "在浏览器授权", func() {
					if a.tailnetNode == nil {
						return
					}
					prefs := mygo.Secrets.Service("ai.shioji.mobile")
					key, err := prefs.Get("tailnet-identity-key")
					clear(key)
					if errors.Is(err, mygo.ErrSecretNotFound) {
						key := make([]byte, 32)
						if _, err := rand.Read(key); err != nil {
							message = "无法生成节点密钥"
							return
						}
						err = prefs.Set("tailnet-identity-key", key)
						clear(key)
						if err != nil {
							message = "无法保存节点密钥"
							return
						}
					} else if err != nil {
						message = "无法读取节点密钥"
						return
					}
					send("join")
				}).Disabled(a.tailnetNode == nil && !a.preview)
			}
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openPair(parent *mygo.Window, draft mobileclient.Endpoint) {
	host, port, _ := net.SplitHostPort(draft.Address)
	directHost := ""
	if draft.TailnetPeerID == "" {
		directHost = host
	}
	if port == "" {
		port = "22"
	}
	if draft.WorkerPath == "" {
		draft.WorkerPath = mobileclient.DefaultWorkerPath
	}
	message := ""
	editingWorkerPath := draft.WorkerPath != mobileclient.DefaultWorkerPath
	var w *mygo.Window
	check := func() {
		in := draft
		address, service := strings.TrimSpace(host), strings.TrimSpace(port)
		in.Address = net.JoinHostPort(address, service)
		in.User, in.WorkerPath = strings.TrimSpace(in.User), strings.TrimSpace(in.WorkerPath)
		n, _ := strconv.Atoi(service)
		if address == "" || in.User == "" || n < 1 || n > 65535 {
			message = "请填写主机、端口和 SSH 用户名"
			w.Invalidate()
			return
		}
		message = ""
		a.openHostVerification(w, in)
	}
	pairInput := func(c *ui.Context, title, placeholder string, value *string) ui.Element {
		return formInput(c, title, placeholder, value).Capitalize(ui.CapitalizeNone).Autocorrect(false)
	}
	w = a.page("添加设备", func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			p := palette(c)
			transport := "直接 SSH"
			if draft.TailnetPeerID != "" {
				transport = "Tailnet"
			}
			formChoice(c, "连接方式", transport, func(m *ui.Menu) {
				menuChoice(m, "直接 SSH", draft.TailnetPeerID == "", func() {
					if draft.TailnetPeerID != "" {
						draft.TailnetPeerID, host = "", directHost
					}
				})
				menuChoice(m, "通过 Tailnet 连接", draft.TailnetPeerID != "", func() {
					a.openTailnetPage(w, func(peer tailnet.Peer) {
						if draft.TailnetPeerID == "" {
							directHost = host
						}
						draft.TailnetPeerID, host = peer.ID, peer.Address
						w.Invalidate()
					})
				})
			}).MinHeight(49)
			if draft.TailnetPeerID == "" {
				formRow(c, "主机", func() {
					pairInput(c, "主机", "mac.local", &host).ReturnKey(ui.ReturnNext)
				}).MinHeight(49)
			} else {
				formRow(c, "主机", func() { oneLine(c, host, 15, p.secondary).MaxWidthPercent(58) }).MinHeight(49)
			}
			formRow(c, "端口", func() {
				pairInput(c, "端口", "22", &port).ReturnKey(ui.ReturnNext)
			}).MinHeight(49)
			formRow(c, "用户名", func() {
				pairInput(c, "用户名", "设备上的用户名", &draft.User).ReturnKey(ui.ReturnGo).OnSubmit(check)
			}).MinHeight(49)
			if editingWorkerPath {
				formRow(c, "执行服务路径", func() {
					pairInput(c, "执行服务路径", mobileclient.DefaultWorkerPath, &draft.WorkerPath).
						ReturnKey(ui.ReturnGo).OnSubmit(check)
				})
			}
			label(c, "连接前核对主机的 SSH 公钥指纹。", 13, p.secondary).Padding(10, 0)
			if message != "" {
				mobileMessage(c, message)
			}
		}, func() {
			primaryAction(c, "检查连接", check)
		})
	})
	mygo.SetBarItems(w, mygo.BarItem{Title: "执行服务路径", Icon: toolbarImage(moreSymbol), OnClick: func() {
		editingWorkerPath = !editingWorkerPath
		w.Invalidate()
	}})
	a.pushNative(parent, w)
}

func (a *mobileApp) openHostVerification(parent *mygo.Window, draft mobileclient.Endpoint) {
	var key mobileclient.HostKeyInfo
	expected, problem := "", ""
	busy, confirming := false, false
	var w *mygo.Window
	route := a.routeDial()
	scan := func() {
		if busy {
			return
		}
		if a.preview {
			key = mobileclient.HostKeyInfo{Fingerprint: "SHA256:mS6bQ1EiR4uJvWRbUZYqASfMJgzeoLaM98ib5qEqOg8", Type: "ssh-ed25519"}
			w.Invalidate()
			return
		}
		busy, problem = true, ""
		w.Invalidate()
		go func(in mobileclient.Endpoint) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			value, err := in.InspectHostKey(ctx, route)
			mygo.RunOnMain(func() {
				if !a.windows[w] {
					return
				}
				busy = false
				if err != nil {
					problem = err.Error()
					key = mobileclient.HostKeyInfo{}
				} else {
					key = value
				}
				w.Invalidate()
			})
		}(draft)
	}
	proceed := func() {
		if !confirming {
			confirming = true
			return
		}
		in := draft
		in.HostFingerprint = strings.TrimSpace(expected)
		if in.HostFingerprint != key.Fingerprint || in.Validate() != nil {
			problem = "请核实设备上的完整 SHA256 指纹"
			return
		}
		problem = ""
		a.openSSHCredentials(w, in, key.Type)
	}
	w = a.page("核对主机身份", func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			p := palette(c)
			mobileDetailRow(c, "主机", "", "主机", deviceSymbol, nil, func() {
				oneLine(c, draft.Address, 11.5, p.secondary).Font("monospace").MaxWidthPercent(64)
			})
			keyType := "—"
			if key.Type != "" {
				keyType = strings.ToUpper(strings.TrimPrefix(key.Type, "ssh-"))
			}
			mobileRow(c, "密钥类型", "", keyType, permissionSymbol, nil)
			label(c, "通过你已有的可信渠道取得主机公钥指纹，核对完全一致后继续。", 13, p.secondary).Padding(10, 0, 18)
			if key.Fingerprint != "" {
				label(c, key.Fingerprint, 13, p.ink).Font("monospace").Selectable().
					Padding(10, 12).Radius(10).Background(p.inset)
			}
			if confirming {
				formRow(c, "设备上核实的指纹", func() {
					formInput(c, "设备上核实的指纹", "SHA256:…", &expected).
						Capitalize(ui.CapitalizeNone).Autocorrect(false).ReturnKey(ui.ReturnGo).OnSubmit(proceed)
				})
			}
			if busy {
				mobileMessage(c, "正在检查连接…")
			}
			if problem != "" {
				mobileMessage(c, problem)
			}
			if !busy && key.Fingerprint == "" {
				action(c, "重新读取指纹", scan)
			}
		}, func() {
			trust := "信任主机，继续配置 SSH"
			if confirming {
				trust = "连接"
			}
			ui.Column(c).Gap(8).Children(func() {
				primaryAction(c, trust, proceed).Disabled(busy || key.Fingerprint == "")
				secondaryAction(c, "取消", func() { w.Pop() })
			})
		})
	})
	scan()
	a.pushNative(parent, w)
}
