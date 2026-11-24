package ui

import (
	"encoding/json"
	"errors"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"time"
)

type directoryPhase uint8

const (
	directoryLoading directoryPhase = iota
	directoryReady
	directoryFailed
	directoryClosing
)

type directoryPicker struct {
	key, device, epoch, title           string
	input, selected, message, requested string
	page                                domain.DirectoryPage
	history                             []string
	scroll                              map[string]*n.ScrollState
	hidden                              bool
	loadedHidden                        bool
	phase                               directoryPhase
	closingFrom                         directoryPhase
	request                             uint64
	index                               int
	closed                              time.Time
	valid                               func() bool
	apply                               func([]string)
}

var openWorkspaceDirectoryDialog = mygo.Dialog.Open
var directoryUpIcon = svg("M10 17V3M4 9l6-6 6 6")
var directoryHomeIcon = svg("M2 9l8-7 8 7M4 8v10h12V8M8 18v-6h4v6")
var directoryHiddenIcon = svg("M2 10s3-5 8-5 8 5 8 5-3 5-8 5-8-5-8-5zM12.5 10a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0M3 3l14 14")
var directoryVisibleIcon = svg("M2 10s3-5 8-5 8 5 8 5-3 5-8 5-8-5-8-5zM12.5 10a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0")

const (
	directoryListInset   = 8
	directoryRowIconSize = 16
	// Center the 28-DIP targets over the directory rows' 16-DIP icon column.
	directoryActionInset = configurationDialogInset - (configurationControlHeight-directoryRowIconSize)/2
	// The back and folder paths begin at x=7 and x=2 in their 20-unit viewBoxes.
	directoryNavigationInset = directoryActionInset - (7-2)*directoryRowIconSize/20
)

const directoryHeaderHeight = 44

func (v *View) chooseRemoteWorkspaceDirectory(extra bool) {
	key, request := v.remoteWorkspaceConnection, v.remoteWorkspaceRequest
	title := "选择主目录"
	if extra {
		title = "选择附加目录"
	}
	v.chooseWorkspaceDirectory(key, title, v.remoteWorkspacePath, extra, func() bool {
		return v.remoteWorkspaceOpen && !v.remoteWorkspaceBusy && v.remoteWorkspaceConnection == key && v.remoteWorkspaceRequest == request
	}, func(paths []string) {
		if !extra {
			v.remoteWorkspacePath = paths[0]
			if strings.TrimSpace(v.remoteWorkspaceName) == "" {
				v.remoteWorkspaceName = path.Base(paths[0])
			}
			return
		}
		current := strings.TrimSpace(v.remoteWorkspaceExtra)
		for _, folder := range paths {
			duplicate := folder == v.remoteWorkspacePath
			for _, old := range strings.Split(current, "\n") {
				if strings.TrimSpace(old) == folder {
					duplicate = true
				}
			}
			if duplicate {
				continue
			}
			if current != "" {
				current += "\n"
			}
			current += folder
		}
		v.remoteWorkspaceExtra = current
	}, func(err error) { v.remoteWorkspaceError = err.Error() })
}

func (v *View) chooseWorkspaceDirectory(key, title, current string, multiple bool, valid func() bool, apply func([]string), failed func(error)) {
	if key == "local" {
		paths, err := openWorkspaceDirectoryDialog(mygo.OpenDialogOptions{Parent: v.nativeWindow, Title: title, DefaultPath: current, Directory: true, Multiple: multiple})
		if !valid() {
			return
		}
		if err != nil {
			failed(err)
		} else if len(paths) > 0 {
			apply(paths)
		}
		return
	}
	state := v.deviceState(key)
	p := &directoryPicker{key: key, device: v.Model.ConnectionDeviceID(key), epoch: state.ConnectionEpoch, title: title, valid: valid, apply: apply}
	v.directoryPicker = p
	start := ""
	if path.IsAbs(current) {
		start = path.Dir(current)
		p.selected = current
	}
	v.readDirectory(p, start, 0, 0)
}
func (v *View) directoryCurrent(p *directoryPicker) bool {
	return v.directoryPicker == p && p.phase != directoryClosing && p.valid() && v.Model.ConnectionDeviceID(p.key) == p.device && v.deviceState(p.key).ConnectionEpoch == p.epoch
}
func (v *View) readDirectory(p *directoryPicker, name string, offset, direction int) {
	if !v.directoryCurrent(p) {
		return
	}
	p.request++
	request := p.request
	p.phase, p.message = directoryLoading, ""
	if offset == 0 {
		p.input = name
		p.requested = name
		p.index += direction
	}
	state := v.deviceState(p.key)
	if !state.Connected {
		p.phase, p.message = directoryFailed, "设备已断开"
		return
	}
	if state.WorkerMinor < 23 {
		p.phase, p.message = directoryFailed, "更新设备执行服务后才能浏览目录"
		return
	}
	q := protocol.DirectoryQuery{Path: name, Offset: offset, Hidden: p.hidden}
	if !v.do(app.Job{ConnectionID: p.key, Method: "device.directories", Input: q, Apply: func(_ *app.Model, body json.RawMessage, err error) {
		if !v.directoryCurrent(p) || p.request != request {
			return
		}
		if !v.deviceState(p.key).Connected {
			err = errors.New("设备已断开")
		}
		var page domain.DirectoryPage
		if err == nil && (json.Unmarshal(body, &page) != nil || !path.IsAbs(page.Path) || !path.IsAbs(page.Home)) {
			err = errors.New("设备返回的目录无效")
		}
		if err != nil {
			p.phase, p.message = directoryFailed, err.Error()
			return
		}
		if offset > 0 {
			if page.Path != p.page.Path {
				p.phase, p.message = directoryFailed, "目录已改变，请重新读取"
				return
			}
			// Directory contents may change between offset-based pages. Keep
			// one native row per device path if the next page overlaps.
			entries := append([]domain.DirectoryEntry(nil), p.page.Entries...)
			seen := make(map[string]bool, len(entries)+len(page.Entries))
			for _, entry := range entries {
				seen[entry.Path] = true
			}
			for _, entry := range page.Entries {
				if !seen[entry.Path] {
					entries = append(entries, entry)
					seen[entry.Path] = true
				}
			}
			page.Entries = entries
		}
		found := p.selected == page.Path
		for _, entry := range page.Entries {
			found = found || entry.Path == p.selected
		}
		if !found {
			p.selected = ""
		}
		p.page, p.input, p.phase = page, page.Path, directoryReady
		p.loadedHidden = q.Hidden
	}}) {
		p.phase, p.message = directoryFailed, "设备操作繁忙，请重试"
	}
}
func (v *View) navigateDirectory(p *directoryPicker, name string, direction int) {
	if p.page.Path != "" && p.page.Path != name {
		p.history = append(p.history, p.page.Path)
	}
	p.selected = ""
	v.readDirectory(p, name, 0, direction)
}
func (v *View) closeDirectoryPicker(p *directoryPicker) {
	if v.directoryPicker != p || p.phase == directoryClosing {
		return
	}
	p.closingFrom = p.phase
	p.phase, p.closed = directoryClosing, v.frameTime
}
func (v *View) directoryDialog(c *n.Context, t *n.Theme) {
	p := v.directoryPicker
	if p == nil {
		return
	}
	if !p.valid() {
		v.directoryPicker = nil
		return
	}
	closing := p.phase == directoryClosing
	if closing && (c.Preferences().ReduceMotion || c.Now().Sub(p.closed) >= 160*time.Millisecond) {
		v.directoryPicker = nil
		return
	}
	if !closing && (v.deviceState(p.key).ConnectionEpoch != p.epoch || !v.deviceState(p.key).Connected) {
		p.phase, p.message = directoryFailed, "设备连接已改变，请重新打开目录选择器"
	}
	open := true
	n.DialogBase(c.Key("directory-picker-dialog"), &open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, dialogBackdropOpacity))
		width, height := c.Size()
		w, h := min(float32(640), width-48), min(float32(444), height-48)
		dialogSurface(panel, t).Width(w).Height(h).Clip().Label("远程文件夹选择器")
		n.Inert(panel, closing)
		panel.Children(func() {
			n.Row(c).FillWidth().Height(directoryHeaderHeight).Padding(0, directoryActionInset, 0, configurationDialogInset).Gap(12).AlignItems(n.Center).Children(func() {
				n.Row(c).Grow(1).Gap(12).AlignItems(n.Baseline).Children(func() {
					n.Text(c, p.title).FontSize(14).FontWeight(500).FixedLineHeight(20).Grow(1)
					name := v.Model.DeviceName(p.device)
					inlineIconText(c, monitor, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 16, 8, t.TextMuted)
				})
				if iconButton(c, closeIcon, "关闭目录选择器").Clicked() {
					v.closeDirectoryPicker(p)
				}
			})
			v.directoryLocation(c, t, p)
			n.Column(c).FillWidth().MinHeight(0).Grow(1).Children(func() {
				configurationTabPanel(c.Key("directory-page"), strconv.Itoa(p.index), p.index, configurationPanelFill, func() { v.directoryContents(c, t, p) }, false)
			})
			n.Row(c).FillWidth().Height(52).Padding(8, configurationDialogInset).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Justify(n.SpaceBetween).AlignItems(n.Center).Children(func() {
				if configurationAction(c, "取消").Label("取消选择目录").MarginStart(-8).Clicked() {
					v.closeDirectoryPicker(p)
				}
				label := "选择文件夹"
				if p.title == "选择附加目录" {
					label = "添加文件夹"
				}
				canChoose := p.phase == directoryReady && v.directoryCurrent(p) && p.input == p.page.Path
				if n.PrimaryButton(c, label).Height(32).PaddingX(14).Radius(6).FontSize(13).Disabled(!canChoose).Clicked() {
					selected := p.selected
					if selected == "" {
						selected = p.page.Path
					}
					if p.valid() {
						p.apply([]string{selected})
					}
					v.closeDirectoryPicker(p)
				}
			})
		})
		instance := n.Local(panel, "directory-instance", func() *directoryPicker { return nil })
		if *instance != p {
			*instance = p
			panel.Jump("visibility", 0)
		}
		target, duration := float32(1), 180*time.Millisecond
		if p.phase == directoryClosing {
			target, duration = 0, 160*time.Millisecond
			c.After(duration - c.Now().Sub(p.closed))
		}
		alpha := panel.AnimateTo("visibility", target, motionAnimation(c, duration))
		backdrop.Opacity(alpha)
	})
	if !open {
		v.closeDirectoryPicker(p)
	}
}
func (v *View) directoryLocation(c *n.Context, t *n.Theme, p *directoryPicker) {
	n.Row(c).FillWidth().Height(40).Padding(0, directoryActionInset, 4, directoryNavigationInset).Gap(4).AlignItems(n.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		loading := p.visiblePhase() == directoryLoading
		if iconButton(c, deviceBackIcon, "返回上一目录").Disabled(len(p.history) == 0 || loading).Clicked() {
			previous := p.history[len(p.history)-1]
			p.history = p.history[:len(p.history)-1]
			p.selected = ""
			v.readDirectory(p, previous, 0, -1)
		}
		if iconButton(c, directoryUpIcon, "上一级目录").Disabled(p.page.Path == "" || p.page.Path == "/" || loading).Clicked() {
			v.navigateDirectory(p, p.page.Parent, -1)
		}
		if iconButton(c, directoryHomeIcon, "用户目录").Disabled(p.page.Home == "" || loading).Clicked() {
			v.navigateDirectory(p, p.page.Home, -1)
		}
		n.Row(c).MinWidth(0).Grow(1).Height(28).MarginStart(4).PaddingX(8).Gap(8).Radius(5).Background(t.Background).Border(1, t.Border).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, workspaceIcon, p.input, n.Font{Family: "monospace", Size: 12, Weight: 400}, 16, t.TextMuted)
			field := n.TextInputBase(c, &p.input).MinWidth(0).Grow(1).Height(26).Font("monospace").FontSize(12).FixedLineHeight(20).Label("远程目录路径")
			composing := field.Composing()
			field.OnTextKey(func(k n.TextKey) bool {
				if k.Key == n.KeyEnter && k.Mods == 0 && !composing {
					p.input = k.Text()
					v.navigateDirectory(p, strings.TrimSpace(p.input), 1)
					return true
				}
				return false
			})
			if field.OverlayShortcut(n.Cmd|n.Shift, n.KeyG) {
				field.Focus()
			}
		})
		hiddenIcon := directoryHiddenIcon
		if p.hidden {
			hiddenIcon = directoryVisibleIcon
		}
		toggle := iconButton(c, hiddenIcon, "显示隐藏文件夹").Disabled(loading || p.page.Path == "")
		if p.hidden {
			toggle.Background(t.Selection).Tooltip("不显示隐藏文件夹")
		}
		if toggle.Clicked() {
			p.hidden = !p.hidden
			p.selected = ""
			v.readDirectory(p, p.page.Path, 0, 0)
		}
	})
}
func (v *View) directoryContents(c *n.Context, t *n.Theme, p *directoryPicker) {
	if p.visiblePhase() == directoryFailed || p.page.Path == "" || len(p.page.Entries) == 0 || (p.visiblePhase() == directoryLoading && p.requested != p.page.Path) {
		n.Column(c).Fill().Center().Gap(12).Children(func() {
			if p.visiblePhase() == directoryFailed {
				n.Icon(c, workspaceIcon).Size(16, 16).TextColor(t.TextMuted)
				n.Text(c, p.message).MaxWidth(400).FontSize(13).FixedLineHeight(20).TextColor(t.TextMuted)
				if configurationAction(c, "重试").Clicked() {
					v.readDirectory(p, p.input, 0, 0)
				}
			} else if p.visiblePhase() == directoryLoading {
				n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
					serviceSpinner(c.Key("directory-spinner"), "正在读取", n.Font{Family: t.Font, Size: 13, Weight: 400}, t.TextMuted)
					n.Text(c, "正在读取").FontSize(13).FixedLineHeight(20).TextColor(t.TextMuted)
				})
			} else {
				n.Text(c, "此文件夹为空").FontSize(13).FixedLineHeight(20).TextColor(t.TextMuted)
			}
		})
		return
	}
	key := p.page.Path + ":" + strconv.FormatBool(p.loadedHidden)
	if p.scroll == nil {
		p.scroll = make(map[string]*n.ScrollState)
	}
	scroll := p.scroll[key]
	if scroll == nil {
		scroll = &n.ScrollState{}
		p.scroll[key] = scroll
	}
	n.Scroll(c.Key("folders:"+key)).TrackScroll(scroll).Fill().Padding(6, directoryListInset).Children(func() {
		n.Column(c).FillWidth().FocusGroup(n.Vertical).Children(func() {
			for _, entry := range p.page.Entries {
				row := control(c.Key(entry.Path), 5).FillWidth().Height(32).Radius(5).PaddingX(configurationDialogInset - directoryListInset).AlignItems(n.Center).Label("文件夹：" + entry.Name).Disabled(p.visiblePhase() == directoryLoading)
				if p.selected == entry.Path {
					row.Background(t.Selection)
				}
				row.Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: t.Font, Size: 13, Weight: 400}
						inlineSymbol(c, workspaceIcon, entry.Name, font, directoryRowIconSize, t.TextMuted)
						n.Text(c, entry.Name).FontSize(13).FixedLineHeight(20).Grow(1).SingleLine().Ellipsis("…")
						inlineSymbol(c, disclosureRight, entry.Name, font, directoryRowIconSize, t.TextMuted)
					})
				})
				if row.Clicked() || row.Focused() {
					p.selected = entry.Path
				}
				if row.DoubleClicked() || row.Shortcut(0, n.KeyEnter) || row.Shortcut(0, n.KeyRight) {
					v.navigateDirectory(p, entry.Path, 1)
				}
			}
			if p.page.More {
				if configurationAction(c, "载入更多").Disabled(p.visiblePhase() == directoryLoading).Clicked() {
					v.readDirectory(p, p.page.Path, p.page.Next, 0)
				}
			}
		})
	})
}

func (p *directoryPicker) visiblePhase() directoryPhase {
	if p.phase == directoryClosing {
		return p.closingFrom
	}
	return p.phase
}
