package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

const mobileScheduleListRefresh = 10 * time.Second

type mobileScheduleSource struct {
	device          savedMobileDevice
	data            mobileclient.DeviceSchedules
	cached          []byte
	online, reading bool
	err             string
}

func (a *mobileApp) scheduleSource(device savedMobileDevice) *mobileScheduleSource {
	id := device.Endpoint.DeviceID
	source := &mobileScheduleSource{device: device, data: mobileclient.DeviceSchedules{DeviceID: id, Address: device.Endpoint.Address}}
	if dir, err := a.deviceDirectory(device); err == nil {
		if raw, err := os.ReadFile(filepath.Join(dir, "schedule-catalog.json")); err == nil {
			var cached mobileclient.DeviceSchedules
			if json.Unmarshal(raw, &cached) == nil && cached.DeviceID == id {
				source.data, source.cached = cached, raw
			}
		}
	}
	return source
}

func (a *mobileApp) cacheSchedules(source *mobileScheduleSource) {
	dir, err := a.deviceDirectory(source.device)
	if err != nil {
		return
	}
	data := source.data
	data.Tasks = slices.Clone(data.Tasks)
	for i, task := range data.Tasks {
		if task.Latest != nil {
			latest := *task.Latest
			latest.Error = ""
			data.Tasks[i].Latest = &latest
		}
	}
	if raw, err := json.Marshal(data); err == nil && !bytes.Equal(raw, source.cached) {
		if app.WriteState(filepath.Join(dir, "schedule-catalog.json"), raw) == nil {
			source.cached = raw
		}
	}
}

type mobileScheduleSelection struct {
	workspace mobileclient.WorkspaceLocation
	upgrade   bool
	deadline  time.Time
}

func (a *mobileApp) openSchedules(parent *mygo.Window, deviceID string) {
	var sources []*mobileScheduleSource
	var w *mygo.Window
	var cancel context.CancelFunc
	var opening *mobileScheduleSelection
	var nextRefresh time.Time
	var notice string
	var readProblem string
	var scope mobileclient.WorkspaceLocation
	var status mobileclient.ScheduleStatus
	if a.preview {
		task := a.previewSchedule().Schedule
		task.Text = ""
		sources = []*mobileScheduleSource{{online: true, data: mobileclient.DeviceSchedules{
			DeviceID: task.DeviceID, Address: a.deviceName(), Minor: protocol.ScheduleMinor,
			Workspaces: a.model.Snapshot.Workspaces, Tasks: []domain.Schedule{task},
		}}}
	} else {
		for _, device := range a.savedDevices.Devices {
			if id := device.Endpoint.DeviceID; id != "" && (deviceID == "" || deviceID == id) {
				sources = append(sources, a.scheduleSource(device))
			}
		}
	}
	read := func() {
		if a.preview || len(sources) == 0 {
			w.Invalidate()
			return
		}
		if cancel != nil {
			cancel()
		}
		nextRefresh = time.Now().Add(mobileScheduleListRefresh)
		signer, err := a.identity()
		if err != nil {
			readProblem = "无法读取设备连接身份"
			w.Invalidate()
			return
		}
		readProblem = ""
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		active, dial := a.activeEndpoint(), a.routeDial()
		for _, source := range sources {
			source.reading = true
			go func() {
				call, done := context.WithTimeout(ctx, 15*time.Second)
				defer done()
				data, err := mobileclient.ReadDeviceSchedules(call, source.device, active, signer, dial)
				mygo.RunOnMain(func() {
					if ctx.Err() != nil || !a.pageCurrent(w) {
						return
					}
					source.reading, source.online = false, err == nil
					if err == nil {
						source.data, source.err = data, ""
						if data.Minor >= protocol.ScheduleMinor {
							a.cacheSchedules(source)
						}
						if scope.DeviceID == data.DeviceID && !slices.ContainsFunc(data.Workspaces, scope.Includes) {
							scope = mobileclient.WorkspaceLocation{}
						}
					} else {
						source.err = err.Error()
					}
					w.Invalidate()
				})
			}()
		}
		w.Invalidate()
	}
	open := func(selection mobileScheduleSelection) {
		if opening != nil || !a.pageCurrent(w) {
			return
		}
		if !a.preview {
			if err := a.openDeviceFromResults(w, selection.workspace.DeviceID); err != nil {
				notice = err.Error()
				w.Invalidate()
				return
			}
		}
		selection.deadline = time.Now().Add(15 * time.Second)
		notice, opening = "", &selection
		w.Invalidate()
	}
	remove := func(task domain.Schedule) {
		for _, source := range sources {
			if source.data.DeviceID == task.DeviceID {
				source.data.Tasks = slices.DeleteFunc(source.data.Tasks, func(row domain.Schedule) bool { return row.ID == task.ID })
				if !a.preview {
					a.cacheSchedules(source)
				}
				break
			}
		}
		read()
	}
	w = a.page("定时任务", func(c *ui.Context) {
		if opening != nil {
			selection := *opening
			switch {
			// The SSH handshake sets Connected before the Worker admits commands.
			case a.preview || a.model.Connected && a.model.ConnectionEpoch != "" && selection.workspace.DeviceID == a.model.Snapshot.DeviceID:
				opening = nil
				workspace := a.workspace(selection.workspace.WorkspaceID)
				switch {
				case selection.upgrade || !a.preview && a.model.WorkerMinor < protocol.ScheduleMinor:
					a.openWorker(w)
				case workspace.ID == "" || workspace.DeviceID != selection.workspace.DeviceID:
					notice = "工作区已变化，请重新选择"
				case workspace.Archived:
					notice = "请选择未归档的工作区"
				default:
					a.openScheduleEditor(w, domain.Schedule{WorkspaceID: workspace.ID}, read)
				}
			case !c.Now().Before(selection.deadline):
				opening, notice = nil, "无法连接任务所属设备"
			default:
				c.After(selection.deadline.Sub(c.Now()))
			}
		}
		if !nextRefresh.IsZero() && opening == nil && c.WindowActive() && mygo.App.IsActive() {
			if !c.Now().Before(nextRefresh) && !slices.ContainsFunc(sources, func(source *mobileScheduleSource) bool { return source.reading }) {
				read()
			}
			if remaining := nextRefresh.Sub(c.Now()); remaining > 0 {
				c.After(remaining)
			}
		}
		mobilePageBody(c, func() {
			workspaceLabel := "全部工作区"
			for _, source := range sources {
				for _, workspace := range source.data.Workspaces {
					if scope != (mobileclient.WorkspaceLocation{}) && scope.Includes(workspace) {
						workspaceLabel = workspace.Name + " · " + a.deviceLabel(source.data.DeviceID, source.data.Address)
					}
				}
			}
			ui.Row(c.Key("schedule-filters")).Gap(8).Wrap().Children(func() {
				scheduleFilter(c.Key("workspaces"), "工作区筛选", workspaceLabel, func(m *ui.Menu) {
					menuChoice(m, "全部工作区", scope == (mobileclient.WorkspaceLocation{}), func() { scope = mobileclient.WorkspaceLocation{} })
					for _, source := range sources {
						for _, workspace := range source.data.Workspaces {
							if !workspace.Archived && workspace.DeviceID == source.data.DeviceID {
								location := mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}
								menuChoice(m, workspace.Name+" · "+a.deviceLabel(source.data.DeviceID, source.data.Address), scope == location, func() { scope = location })
							}
						}
					}
				})
				scheduleFilter(c.Key("states"), "状态筛选", scheduleFilterName(status), func(m *ui.Menu) {
					for _, state := range []mobileclient.ScheduleStatus{"", mobileclient.ScheduleEnabled, mobileclient.SchedulePaused, mobileclient.ScheduleOffline} {
						menuChoice(m, scheduleFilterName(state), state == status, func() { status = state })
					}
				})
			})
			if opening != nil {
				mobileMessage(c, "正在连接任务所属设备…")
			} else if notice != "" {
				mobileMessage(c, notice)
			} else if readProblem != "" {
				mobileMessage(c, readProblem)
			}
			mobileSection(c, "已安排")
			count, reading, incomplete := 0, false, false
			for _, source := range sources {
				if scope.DeviceID != "" && scope.DeviceID != source.data.DeviceID {
					continue
				}
				reading = reading || source.reading
				incomplete = incomplete || source.err != "" || source.online && source.data.Minor < protocol.ScheduleMinor
				for _, row := range mobileclient.BrowseDeviceSchedules(source.data, scope, status, source.online) {
					if source.reading && !source.online && status == mobileclient.ScheduleOffline {
						continue
					}
					count++
					task := row.Task
					timing := scheduleSummary(task)
					if row.Status == mobileclient.ScheduleEnabled && !task.NextAt.IsZero() {
						timing += " · 下次 " + mobileScheduleDate(task.NextAt, task.Zone)
					}
					scope := row.Workspace.Name + " · " + a.deviceLabel(source.data.DeviceID, source.data.Address)
					stateLabel := scheduleFilterName(row.Status)
					if source.reading && !source.online {
						stateLabel = "正在核对"
					}
					mobileDetailRow(c.Key([2]string{task.DeviceID, task.ID}), task.Name, timing+"\n"+scope, task.Name, clockSymbol, func() {
						if opening == nil && a.pageCurrent(w) {
							a.openSchedule(w, task, scope, read, func() { remove(task) })
						}
					}, func() {
						oneLine(c, stateLabel, captionTextSize, palette(c).secondary).Shrink(0).PassThrough()
					}).MinHeight(78).PaddingY(10).Value(stateLabel)
				}
			}
			if count == 0 {
				switch {
				case reading:
					mobileMessage(c, "正在载入定时任务…")
				case incomplete:
					mobileMessage(c, "部分设备的任务暂不可读")
				case scope != (mobileclient.WorkspaceLocation{}) || status != "":
					mobileMessage(c, "此筛选下没有定时任务")
				default:
					mobileMessage(c, "还没有定时任务")
				}
			}
			for _, source := range sources {
				if source.online && source.data.Minor < protocol.ScheduleMinor {
					mobileRow(c.Key("upgrade:"+source.data.DeviceID), a.deviceLabel(source.data.DeviceID, source.data.Address), "更新执行服务后可管理该设备的任务", "需更新", serverSymbol, func() {
						open(mobileScheduleSelection{workspace: mobileclient.WorkspaceLocation{DeviceID: source.data.DeviceID}, upgrade: true})
					})
				} else if source.err != "" {
					mobileRow(c.Key("failure:"+source.data.DeviceID), a.deviceLabel(source.data.DeviceID, source.data.Address), source.err, "读取失败", infoSymbol, nil)
				}
			}
		})
	})
	mygo.SetBarItems(w, mygo.BarItem{Title: "新建定时任务", Symbol: "plus", OnClick: func() {
		if opening != nil {
			return
		}
		var workspaces []domain.Workspace
		address := map[string]string{}
		for _, source := range sources {
			if source.online && source.data.Minor >= protocol.ScheduleMinor {
				address[source.data.DeviceID] = a.deviceLabel(source.data.DeviceID, source.data.Address)
				for _, workspace := range source.data.Workspaces {
					if !workspace.Archived && workspace.DeviceID == source.data.DeviceID {
						workspaces = append(workspaces, workspace)
					}
				}
			}
		}
		if len(workspaces) == 0 {
			notice = "连接有未归档工作区的执行设备后再新建任务"
			w.Invalidate()
			return
		}
		if len(workspaces) == 1 {
			workspace := workspaces[0]
			open(mobileScheduleSelection{workspace: mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}})
			return
		}
		var picker *mygo.Window
		var selected *mobileScheduleSelection
		picker = a.page("选择工作区", func(c *ui.Context) {
			mobilePageBody(c, func() {
				for _, workspace := range workspaces {
					location := mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}
					mobileRow(c.Key(location), workspace.Name, address[workspace.DeviceID], "", folderSymbol, func() {
						selected = &mobileScheduleSelection{workspace: location}
						picker.Pop()
					})
				}
			})
		})
		picker.OnClosed(func() {
			if selected != nil {
				open(*selected)
			}
		})
		a.pushNative(w, picker)
	}})
	w.OnClosed(func() {
		if cancel != nil {
			cancel()
		}
	})
	read()
	a.pushNative(parent, w)
}

func scheduleFilter(c *ui.Context, title, value string, menu func(*ui.Menu)) {
	iconAction(c, value, title, nil, 0, pickerSymbol, pickerIconSize, controlTextSize, 400, nil).
		Radius(pillRadius).Background(palette(c).inset).MaxWidthPercent(57).Value(value).PopoverMenuButton(menu)
}

func scheduleFilterName(status mobileclient.ScheduleStatus) string {
	switch status {
	case mobileclient.ScheduleEnabled:
		return "已启用"
	case mobileclient.SchedulePaused:
		return "已暂停"
	case mobileclient.ScheduleOffline:
		return "设备离线"
	case mobileclient.ScheduleArchived:
		return "工作区已归档"
	default:
		return "全部状态"
	}
}
